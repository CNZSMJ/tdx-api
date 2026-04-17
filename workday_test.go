package tdx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/base/maps"
	"xorm.io/core"
	"xorm.io/xorm"
)

func TestNormalizeWorkdayModelsCanonicalizesAndDeduplicatesDates(t *testing.T) {
	rows := []*WorkdayModel{
		{Unix: time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local).Unix(), Date: "20260415"},
		{Unix: time.Date(2026, 4, 16, 0, 0, 0, 0, time.Local).Unix(), Date: "20260416"},
		{Unix: time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local).Unix(), Date: "20260416"},
	}

	got, changed, err := normalizeWorkdayModels(rows)
	if err != nil {
		t.Fatalf("normalizeWorkdayModels returned error: %v", err)
	}
	if !changed {
		t.Fatalf("expected normalizeWorkdayModels to report a change")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 canonical rows, got %d", len(got))
	}

	wantUnix := []int64{
		time.Date(2026, 4, 15, 15, 0, 0, 0, time.Local).Unix(),
		time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local).Unix(),
	}
	for i, row := range got {
		if row.Unix != wantUnix[i] {
			t.Fatalf("row %d unix = %d, want %d", i, row.Unix, wantUnix[i])
		}
	}
}

func TestWorkdayLoadCacheRepairsLegacyDuplicateDates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workday.db")
	engine, err := xorm.NewEngine("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite engine: %v", err)
	}
	defer engine.Close()
	engine.SetMapper(core.SameMapper{})
	engine.DB().SetMaxOpenConns(1)

	if err := engine.Sync2(new(WorkdayModel)); err != nil {
		t.Fatalf("sync workday schema: %v", err)
	}
	if _, err := engine.Insert(
		&WorkdayModel{Unix: time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local).Unix(), Date: "20260415"},
		&WorkdayModel{Unix: time.Date(2026, 4, 16, 0, 0, 0, 0, time.Local).Unix(), Date: "20260416"},
		&WorkdayModel{Unix: time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local).Unix(), Date: "20260416"},
	); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	w := &Workday{db: engine, cache: maps.NewBit()}
	count, err := w.loadCache()
	if err != nil {
		t.Fatalf("loadCache repair failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 repaired rows, got %d", count)
	}

	rows := []*WorkdayModel(nil)
	if err := engine.Asc("Unix").Find(&rows); err != nil {
		t.Fatalf("read repaired rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after repair, got %d", len(rows))
	}
	if rows[0].Unix != time.Date(2026, 4, 15, 15, 0, 0, 0, time.Local).Unix() {
		t.Fatalf("unexpected first repaired unix: %d", rows[0].Unix)
	}
	if rows[1].Unix != time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local).Unix() {
		t.Fatalf("unexpected second repaired unix: %d", rows[1].Unix)
	}

	results, err := engine.QueryString("pragma index_list('workday')")
	if err != nil {
		t.Fatalf("query index_list: %v", err)
	}
	var dateIndexExists bool
	for _, row := range results {
		if row["name"] == "UQE_workday_Date" {
			dateIndexExists = true
			break
		}
	}
	if !dateIndexExists {
		t.Fatalf("expected UQE_workday_Date index to exist after repair")
	}
}
