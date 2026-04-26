package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestRunDataLifecycleMaintenanceArchivesAndPrunesTempHotStore(t *testing.T) {
	originalDir := databaseDir
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRunner := dataLifecycleMaintenance
	defer func() {
		databaseDir = originalDir
		governanceStore = originalStore
		governancePaths = originalPaths
		dataLifecycleMaintenance = originalRunner
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	t.Setenv("TDX_LIFECYCLE_ENABLE", "")
	t.Setenv("TDX_LIFECYCLE_ALLOW_PRUNE", "")
	t.Setenv("TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS", "1")
	t.Setenv("TDX_LIFECYCLE_MAX_CANDIDATES", "1")
	t.Setenv("TDX_LIFECYCLE_MIN_FREE_BYTES", "1")
	t.Setenv("TDX_LIFECYCLE_SAFETY_MARGIN_BYTES", "1")

	createWorkdayFixture(t, filepath.Join(tmp, "workday.db"))
	mustCreateWebSQLite(t, filepath.Join(tmp, "trade", "sh600000.db"), []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`CREATE INDEX idx_trade_history_date ON TradeHistory(TradeDate, Seq)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})

	initDataLifecycleMaintenanceRunner()
	if dataLifecycleMaintenance == nil {
		t.Fatalf("data lifecycle maintenance runner was not initialized")
	}
	run, err := runDataLifecycleMaintenanceWithContext(context.Background(), "manual-test")
	if err != nil {
		t.Fatalf("run lifecycle maintenance: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("unexpected governance run: %+v", run)
	}
	rows, err := countWebRows(filepath.Join(tmp, "trade", "sh600000.db"), "TradeHistory")
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("hot rows = %d, want 1", rows)
	}
}

func TestLifecycleMaintenanceDefaultCandidateBudgetMatchesSteadyState(t *testing.T) {
	t.Setenv("TDX_LIFECYCLE_MAX_CANDIDATES", "")

	if got := lifecycleEnvInt("TDX_LIFECYCLE_MAX_CANDIDATES", 400); got != 400 {
		t.Fatalf("default lifecycle candidates = %d, want 400", got)
	}
}

func TestRunDataLifecycleMaintenanceWithRetryRetriesLockConflicts(t *testing.T) {
	attempts := 0
	delays := make([]time.Duration, 0, 2)
	run := func(trigger string) (*collectorpkg.GovernanceRunRecord, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("%w: test lock", collectorpkg.ErrGovernanceLockHeld)
		}
		return &collectorpkg.GovernanceRunRecord{}, nil
	}
	after := func(delay time.Duration) <-chan time.Time {
		delays = append(delays, delay)
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	err := runDataLifecycleMaintenanceWithRetry("scheduled-test", run, []time.Duration{time.Minute, 2 * time.Minute}, after)
	if err != nil {
		t.Fatalf("retry lifecycle maintenance: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(delays) != 2 || delays[0] != time.Minute || delays[1] != 2*time.Minute {
		t.Fatalf("retry delays = %v, want [1m 2m]", delays)
	}
}

func createWorkdayFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open workday db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE workday(date TEXT NOT NULL)`); err != nil {
		t.Fatalf("create workday table: %v", err)
	}
	for day := time.Date(2023, 1, 2, 0, 0, 0, 0, time.Local); !day.After(time.Date(2026, 4, 24, 0, 0, 0, 0, time.Local)); day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		if _, err := db.Exec(`INSERT INTO workday(date) VALUES(?)`, day.Format("20060102")); err != nil {
			t.Fatalf("insert workday: %v", err)
		}
	}
}

func countWebRows(dbPath, table string) (int64, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int64
	err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
	return count, err
}
