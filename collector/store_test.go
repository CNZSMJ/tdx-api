package collector

import (
	"path/filepath"
	"testing"
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
