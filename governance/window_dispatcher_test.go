package governance

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestWindowDispatcherExecutesHighestPriorityEligibleWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 21, 30, 0, 0, time.Local)
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDataLifecycleMaintenance, "20260428"),
			JobName:      string(collectorpkg.GovernanceJobDataLifecycleMaintenance),
			TargetWindow: "20260428",
			DueAt:        now.Add(-30 * time.Minute),
			Priority:     7,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-30 * time.Minute),
		},
		{
			WindowKey:    collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"),
			JobName:      string(collectorpkg.GovernanceJobDailyAudit),
			TargetWindow: "20260427,20260428",
			DueAt:        now.Add(-150 * time.Minute),
			Priority:     4,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-150 * time.Minute),
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}

	executed := make([]string, 0, 1)
	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			executed = append(executed, window.WindowKey)
			return WindowExecutionResult{
				Status:  collectorpkg.GovernanceWindowStatusPassed,
				Summary: "audit completed",
				RunID:   "run-audit",
			}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not run an eligible window")
	}
	if len(executed) != 1 || executed[0] != collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428") {
		t.Fatalf("executed windows = %+v, want daily audit first", executed)
	}
	audit, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"))
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit.RunID != "run-audit" {
		t.Fatalf("audit run id = %q, want run-audit", audit.RunID)
	}
}

func TestWindowDispatcherDefersWindowUntilDependencyIsTerminal(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 19, 0, 0, 0, time.Local)
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    closeKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: "20260427,20260428",
			DueAt:        now.Add(-time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusRunning,
			StartedAt:    now.Add(-time.Hour),
			LeaseOwner:   "close-sync",
			LeaseUntil:   now.Add(time.Hour),
		},
		{
			WindowKey:     collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"),
			JobName:       string(collectorpkg.GovernanceJobDailyAudit),
			TargetWindow:  "20260427,20260428",
			DueAt:         now,
			Priority:      4,
			Status:        collectorpkg.GovernanceWindowStatusQueued,
			DependencyKey: closeKey,
			EnqueuedAt:    now,
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("execute should not run while dependency is active: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher ran a dependency-blocked window")
	}

	audit, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"))
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusWaitingDependency {
		t.Fatalf("audit window = %+v, want waiting_dependency", audit)
	}
}

func TestWindowDispatcherTerminatesWindowWithMissingDependency(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	auditKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428")
	missingCloseKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	window := collectorpkg.GovernanceWindowRecord{
		WindowKey:     auditKey,
		JobName:       string(collectorpkg.GovernanceJobDailyAudit),
		TargetWindow:  "20260427,20260428",
		DueAt:         now.Add(-time.Hour),
		Priority:      4,
		Status:        collectorpkg.GovernanceWindowStatusWaitingDependency,
		DependencyKey: missingCloseKey,
		EnqueuedAt:    now.Add(-time.Hour),
	}
	if err := store.UpsertWindow(&window); err != nil {
		t.Fatalf("seed audit window: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("missing dependency should not execute: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher should not execute a missing-dependency window")
	}

	audit, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusTerminalFailed {
		t.Fatalf("audit window = %+v, want terminal_failed", audit)
	}
}
