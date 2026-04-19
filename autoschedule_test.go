package tdx

import (
	"path/filepath"
	"testing"
	"time"

	"xorm.io/core"
	"xorm.io/xorm"
)

func TestCodesConstructorDoesNotStartAutoRefreshCron(t *testing.T) {
	db, err := xorm.NewEngine("sqlite", filepath.Join(t.TempDir(), "codes.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMapper(core.SameMapper{})
	db.DB().SetMaxOpenConns(1)

	if err := db.Sync2(new(CodeModel), new(UpdateModel)); err != nil {
		t.Fatalf("sync schema: %v", err)
	}
	if _, err := db.Insert(&UpdateModel{Key: "codes", Time: time.Now().Add(12 * time.Hour).Unix()}); err != nil {
		t.Fatalf("insert update marker: %v", err)
	}
	if _, err := db.Insert(&CodeModel{Name: "浦发银行", Exchange: "sh", Code: "600000"}); err != nil {
		t.Fatalf("insert code row: %v", err)
	}

	codes, err := NewCodes(&Client{}, db)
	if err != nil {
		t.Fatalf("new codes: %v", err)
	}
	if codes.task != nil {
		t.Fatalf("expected constructor not to start auto-refresh cron")
	}
}

func TestWorkdayConstructorDoesNotStartAutoRefreshCron(t *testing.T) {
	db, err := xorm.NewEngine("sqlite", filepath.Join(t.TempDir(), "workday.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMapper(core.SameMapper{})
	db.DB().SetMaxOpenConns(1)

	if err := db.Sync2(new(WorkdayModel)); err != nil {
		t.Fatalf("sync schema: %v", err)
	}
	canonical := canonicalWorkdayTime(time.Now())
	if _, err := db.Insert(&WorkdayModel{Unix: canonical.Unix(), Date: canonical.Format("20060102")}); err != nil {
		t.Fatalf("insert workday row: %v", err)
	}

	workday, err := NewWorkday(&Client{}, db)
	if err != nil {
		t.Fatalf("new workday: %v", err)
	}
	if workday.task != nil {
		t.Fatalf("expected constructor not to start auto-refresh cron")
	}
}
