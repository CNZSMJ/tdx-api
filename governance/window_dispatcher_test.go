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

func TestWindowDispatcherDefersWindowUntilDependencyIsSuccessful(t *testing.T) {
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

func TestWindowDispatcherDoesNotRunWindowWhenDependencyTerminalFailed(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 19, 0, 0, 0, time.Local)
	targetWindow := "20260427,20260428"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	auditKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, targetWindow)
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    closeKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: targetWindow,
			DueAt:        now.Add(-time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusTerminalFailed,
			LastError:    "collector process restarted before governance run finished",
		},
		{
			WindowKey:     auditKey,
			JobName:       string(collectorpkg.GovernanceJobDailyAudit),
			TargetWindow:  targetWindow,
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
			t.Fatalf("execute should not run after failed dependency: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher ran a dependency-failed window")
	}

	audit, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusWaitingDependency {
		t.Fatalf("audit window = %+v, want waiting_dependency", audit)
	}
}

func TestWindowDispatcherExpiresStaleRunningWindowAfterRunInterrupted(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	targetWindow := "20260427,20260428"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    closeKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-2 * time.Hour),
		Priority:     3,
		Status:       collectorpkg.GovernanceWindowStatusRunning,
		StartedAt:    now.Add(-2 * time.Hour),
		LeaseOwner:   "old-dispatcher",
		LeaseUntil:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed close window: %v", err)
	}
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        "run-close",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusInterrupted,
		Reason:       "collector process restarted before governance run finished",
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed close run: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("expired running window should be terminalized, not executed: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not recover the expired running window")
	}

	window, err := store.GetWindowByKey(closeKey)
	if err != nil {
		t.Fatalf("get close window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusTerminalFailed {
		t.Fatalf("close window = %+v, want terminal_failed", window)
	}
	if window.LeaseOwner != "" || !window.LeaseUntil.IsZero() {
		t.Fatalf("close window lease was not cleared: %+v", window)
	}
	if window.RunID != "run-close" {
		t.Fatalf("close window run id = %q, want run-close", window.RunID)
	}
	if window.EndedAt.IsZero() || window.LastError != "collector process restarted before governance run finished" {
		t.Fatalf("close window did not mirror interrupted run: %+v", window)
	}

	run, err := store.GetRunByRunID("run-close")
	if err != nil {
		t.Fatalf("get close run: %v", err)
	}
	if run == nil || run.Status != collectorpkg.GovernanceRunStatusInterrupted || run.Reason != "collector process restarted before governance run finished" {
		t.Fatalf("close run = %+v, want prior interrupted run preserved", run)
	}
}

func TestWindowDispatcherMirrorsCompletedRunWhenRecoveringExpiredWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	targetWindow := "20260427,20260428"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    closeKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-2 * time.Hour),
		Priority:     3,
		Status:       collectorpkg.GovernanceWindowStatusRunning,
		StartedAt:    now.Add(-2 * time.Hour),
		LeaseOwner:   "old-dispatcher",
		LeaseUntil:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed close window: %v", err)
	}
	runEndedAt := now.Add(-30 * time.Second)
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        "run-close-passed",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusPassed,
		TargetWindow: targetWindow,
		Details:      "daily_close_sync completed",
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      runEndedAt,
	}); err != nil {
		t.Fatalf("seed close run: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("expired running window should mirror the completed run, not execute: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not recover the expired running window")
	}

	window, err := store.GetWindowByKey(closeKey)
	if err != nil {
		t.Fatalf("get close window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("close window = %+v, want passed", window)
	}
	if window.RunID != "run-close-passed" {
		t.Fatalf("close window run id = %q, want run-close-passed", window.RunID)
	}
	if window.LastError != "" {
		t.Fatalf("close window last error = %q, want empty", window.LastError)
	}
	if !window.EndedAt.Equal(runEndedAt) {
		t.Fatalf("close window ended at = %s, want %s", window.EndedAt, runEndedAt)
	}
}

func TestWindowDispatcherDoesNotExpireWindowWithRunningRun(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	targetWindow := "20260427,20260428"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    closeKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-2 * time.Hour),
		Priority:     3,
		Status:       collectorpkg.GovernanceWindowStatusRunning,
		StartedAt:    now.Add(-2 * time.Hour),
		LeaseOwner:   "old-dispatcher",
		LeaseUntil:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed close window: %v", err)
	}
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        "run-close",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed close run: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("running window should not be re-entered: %+v", window)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher should not recover a window with an active running run")
	}

	window, err := store.GetWindowByKey(closeKey)
	if err != nil {
		t.Fatalf("get close window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusRunning {
		t.Fatalf("close window = %+v, want still running", window)
	}
}

func TestWindowDispatcherCreatesMissingCanonicalDependencyWindow(t *testing.T) {
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
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusWaitingDependency {
		t.Fatalf("audit window = %+v, want waiting_dependency", audit)
	}
	dependency, err := store.GetWindowByKey(missingCloseKey)
	if err != nil {
		t.Fatalf("get dependency window: %v", err)
	}
	if dependency == nil || dependency.Status != collectorpkg.GovernanceWindowStatusQueued {
		t.Fatalf("dependency window = %+v, want queued", dependency)
	}
}
