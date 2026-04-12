package collector

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreEnsureSchema(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "collector.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	for _, bean := range []interface{}{
		new(SchemaVersion),
		new(PhaseStateRecord),
		new(TaskRunRecord),
		new(ValidationRunRecord),
		new(OperationLogRecord),
		new(CollectCursorRecord),
		new(CollectGapRecord),
		new(ScheduleRunRecord),
	} {
		ok, err := store.HasTable(bean)
		if err != nil {
			t.Fatalf("check table: %v", err)
		}
		if !ok {
			t.Fatalf("expected table for %T", bean)
		}
	}

	count, err := store.engine.Where("Version = ?", SchemaVersionCurrent).Count(new(SchemaVersion))
	if err != nil {
		t.Fatalf("count schema version: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one schema version row, got %d", count)
	}
}

func TestGateReport(t *testing.T) {
	report := NewGateReport("phase_0a",
		GateCheck{Name: "docs", Blocking: true, Status: CheckPassed},
		GateCheck{Name: "tests", Blocking: true, Status: CheckPassed},
	)
	if !report.CanCommit() {
		t.Fatalf("expected commit gate to pass")
	}

	report.Checks[1].Status = CheckFailed
	if report.CanCommit() {
		t.Fatalf("expected commit gate to fail")
	}
}

func TestStoreInterruptRunningScheduleRuns(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "collector.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	seed := []ScheduleRunRecord{
		{ScheduleName: "collector_startup_catchup", Status: "running", StartedAt: time.Date(2026, 4, 11, 0, 0, 0, 0, time.Local)},
		{ScheduleName: "collector_startup_catchup", Status: "passed", StartedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.Local), EndedAt: time.Date(2026, 4, 10, 1, 0, 0, 0, time.Local)},
		{ScheduleName: "collector_daily_reconcile", Status: "running", StartedAt: time.Date(2026, 4, 11, 1, 0, 0, 0, time.Local)},
	}
	for i := range seed {
		record := seed[i]
		if err := store.AddScheduleRun(&record); err != nil {
			t.Fatalf("seed schedule run: %v", err)
		}
	}

	endedAt := time.Date(2026, 4, 11, 2, 0, 0, 0, time.Local)
	updated, err := store.InterruptRunningScheduleRuns("collector_startup_catchup", "superseded by newer run", endedAt)
	if err != nil {
		t.Fatalf("interrupt running schedule runs: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 interrupted startup run, got %d", updated)
	}

	run, err := store.LatestScheduleRun("collector_startup_catchup")
	if err != nil {
		t.Fatalf("latest startup run: %v", err)
	}
	if run == nil || run.Status != "interrupted" {
		t.Fatalf("expected latest startup run to be interrupted, got %+v", run)
	}
	if !run.EndedAt.Equal(endedAt) {
		t.Fatalf("expected ended_at=%v, got %v", endedAt, run.EndedAt)
	}
	if run.Details != "superseded by newer run" {
		t.Fatalf("unexpected details: %q", run.Details)
	}

	other, err := store.LatestScheduleRun("collector_daily_reconcile")
	if err != nil {
		t.Fatalf("latest reconcile run: %v", err)
	}
	if other == nil || other.Status != "running" {
		t.Fatalf("expected reconcile run to remain running, got %+v", other)
	}
}

func TestStoreSeedTradeHistoryCoverageStarts(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "collector.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	for _, record := range []CollectCursorRecord{
		{Domain: tradeHistoryDomain, AssetType: string(AssetTypeStock), Instrument: "sh600000", Cursor: "20260401"},
		{Domain: tradeHistoryDomain, AssetType: string(AssetTypeETF), Instrument: "sh510300", Cursor: "20181228"},
		{Domain: tradeHistoryCoverageStartDomain, AssetType: string(AssetTypeStock), Instrument: "sh600001", Cursor: "20190101"},
	} {
		record := record
		if err := store.UpsertCollectCursor(&record); err != nil {
			t.Fatalf("seed cursor: %v", err)
		}
	}

	inserted, err := store.SeedTradeHistoryCoverageStarts("20190101")
	if err != nil {
		t.Fatalf("seed trade coverage starts: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("expected 2 seeded coverage-start cursors, got %d", inserted)
	}

	stockStart, err := store.GetCollectCursor(tradeHistoryCoverageStartDomain, string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("load stock coverage-start cursor: %v", err)
	}
	if stockStart == nil || stockStart.Cursor != "20190101" {
		t.Fatalf("unexpected stock coverage-start cursor: %#v", stockStart)
	}

	etfStart, err := store.GetCollectCursor(tradeHistoryCoverageStartDomain, string(AssetTypeETF), "sh510300", "")
	if err != nil {
		t.Fatalf("load etf coverage-start cursor: %v", err)
	}
	if etfStart == nil || etfStart.Cursor != "20181228" {
		t.Fatalf("unexpected etf coverage-start cursor: %#v", etfStart)
	}
}

func TestStoreSeedLiveCaptureCoverageStarts(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "collector.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	for _, record := range []CollectCursorRecord{
		{Domain: liveCaptureDomain, AssetType: string(AssetTypeStock), Instrument: "sh600000", Cursor: "20260401"},
		{Domain: liveCaptureDomain, AssetType: string(AssetTypeETF), Instrument: "sh510300", Cursor: "20181228"},
		{Domain: liveCaptureCoverageStartDomain, AssetType: string(AssetTypeStock), Instrument: "sh600001", Cursor: "20190101"},
	} {
		record := record
		if err := store.UpsertCollectCursor(&record); err != nil {
			t.Fatalf("seed cursor: %v", err)
		}
	}

	inserted, err := store.SeedLiveCaptureCoverageStarts("20190101")
	if err != nil {
		t.Fatalf("seed live coverage starts: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("expected 2 seeded live coverage-start cursors, got %d", inserted)
	}

	stockStart, err := store.GetCollectCursor(liveCaptureCoverageStartDomain, string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("load stock live coverage-start cursor: %v", err)
	}
	if stockStart == nil || stockStart.Cursor != "20190101" {
		t.Fatalf("unexpected stock live coverage-start cursor: %#v", stockStart)
	}

	etfStart, err := store.GetCollectCursor(liveCaptureCoverageStartDomain, string(AssetTypeETF), "sh510300", "")
	if err != nil {
		t.Fatalf("load etf live coverage-start cursor: %v", err)
	}
	if etfStart == nil || etfStart.Cursor != "20181228" {
		t.Fatalf("unexpected etf live coverage-start cursor: %#v", etfStart)
	}
}
