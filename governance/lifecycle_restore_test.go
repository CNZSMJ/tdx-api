package governance

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/collector/lifecycle"
)

func TestDataLifecycleRestoreRunnerRecordsExplicitRestore(t *testing.T) {
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(t.TempDir(), "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	runner, err := NewDataLifecycleRestoreRunner(DataLifecycleRestoreConfig{
		Store: store,
		Paths: collectorpkg.ResolveGovernancePaths(t.TempDir()),
		Now:   func() time.Time { return time.Date(2026, 4, 25, 20, 0, 0, 0, time.Local) },
		Execute: func(ctx context.Context, req DataLifecycleRestoreRequest) (lifecycle.RestoreResult, error) {
			if req.SegmentID != "seg-1" || req.Mode != "temporary_query_restore" {
				t.Fatalf("unexpected request: %+v", req)
			}
			return lifecycle.RestoreResult{TargetDBPath: req.TargetPath, TableName: "TradeHistory", RowCount: 3}, nil
		},
	})
	if err != nil {
		t.Fatalf("new restore runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "manual", DataLifecycleRestoreRequest{
		SegmentID:  "seg-1",
		Mode:       "temporary_query_restore",
		TargetPath: filepath.Join(t.TempDir(), "restore.db"),
	})
	if err != nil {
		t.Fatalf("run restore: %v", err)
	}
	if run.JobName != string(collectorpkg.GovernanceJobDataLifecycleRestore) || run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !strings.Contains(run.Details, "rows_restored=3") || !strings.Contains(run.TargetWindow, "seg-1") {
		t.Fatalf("missing restore details: %+v", run)
	}
}
