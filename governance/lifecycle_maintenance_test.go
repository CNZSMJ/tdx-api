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

func TestDataLifecycleMaintenanceRunnerRecordsGovernanceRun(t *testing.T) {
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(t.TempDir(), "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	runner, err := NewDataLifecycleMaintenanceRunner(DataLifecycleMaintenanceConfig{
		Store: store,
		Paths: paths,
		Now:   func() time.Time { return time.Date(2026, 4, 25, 19, 35, 0, 0, time.Local) },
		Execute: func(ctx context.Context, runID string) (lifecycle.MaintenanceResult, error) {
			return lifecycle.MaintenanceResult{
				Status:             "passed",
				HotCutoffDate:      "20260401",
				ProcessedSegments:  2,
				PrunedSegments:     2,
				RowsArchived:       100,
				BytesReleased:      2048,
				SelectedCandidates: 2,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "scheduled-19:35")
	if err != nil {
		t.Fatalf("run maintenance: %v", err)
	}
	if run.JobName != string(collectorpkg.GovernanceJobDataLifecycleMaintenance) || run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !strings.Contains(run.Details, "processed_segments=2") || !strings.Contains(run.Details, "hot_cutoff=20260401") {
		t.Fatalf("missing lifecycle details: %s", run.Details)
	}
	lock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("read lock metadata: %v", err)
	}
	if lock != nil {
		t.Fatalf("lock metadata remained after completed maintenance run: %+v", lock)
	}
}

func TestDataLifecycleMaintenanceRunnerMarksPartialResult(t *testing.T) {
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(t.TempDir(), "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	runner, err := NewDataLifecycleMaintenanceRunner(DataLifecycleMaintenanceConfig{
		Store: store,
		Paths: collectorpkg.ResolveGovernancePaths(t.TempDir()),
		Now:   time.Now,
		Execute: func(ctx context.Context, runID string) (lifecycle.MaintenanceResult, error) {
			return lifecycle.MaintenanceResult{Status: "partial", Reason: "hot pruning disabled", ProcessedSegments: 1}, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	run, err := runner.Run(context.Background(), "manual")
	if err != nil {
		t.Fatalf("run maintenance: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial || !strings.Contains(run.Reason, "hot pruning disabled") {
		t.Fatalf("unexpected partial run: %+v", run)
	}
}
