package governance

import (
	"context"
	"errors"
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

func TestWindowDispatcherRenewsLeaseDuringLongRunningExecution(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	windowKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	window := collectorpkg.GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: "20260427,20260428",
		DueAt:        now.Add(-time.Minute),
		Priority:     3,
		Status:       collectorpkg.GovernanceWindowStatusQueued,
		EnqueuedAt:   now.Add(-time.Minute),
	}
	if err := store.UpsertWindow(&window); err != nil {
		t.Fatalf("seed window: %v", err)
	}

	var renewedLease time.Time
	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store:         store,
		Owner:         "test-dispatcher",
		LeaseDuration: 30 * time.Millisecond,
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			lease, err := waitForRenewedLease(store, window.WindowKey, window.LeaseUntil, 500*time.Millisecond)
			if err != nil {
				return WindowExecutionResult{}, err
			}
			renewedLease = lease
			return WindowExecutionResult{
				Status:  collectorpkg.GovernanceWindowStatusPassed,
				Summary: "close sync completed",
				RunID:   "run-close",
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
	if renewedLease.IsZero() {
		t.Fatalf("lease was not renewed during execution")
	}
	finalWindow, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get final window: %v", err)
	}
	if finalWindow == nil || finalWindow.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("final window = %+v, want passed", finalWindow)
	}
	if finalWindow.LeaseOwner != "" || !finalWindow.LeaseUntil.IsZero() {
		t.Fatalf("final window lease was not cleared: %+v", finalWindow)
	}
}

func TestWindowDispatcherStopsLeaseRenewalAndClearsLeaseOnExecutionError(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	windowKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	window := collectorpkg.GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: "20260427,20260428",
		DueAt:        now.Add(-time.Minute),
		Priority:     3,
		Status:       collectorpkg.GovernanceWindowStatusQueued,
		EnqueuedAt:   now.Add(-time.Minute),
	}
	if err := store.UpsertWindow(&window); err != nil {
		t.Fatalf("seed window: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store:         store,
		Owner:         "test-dispatcher",
		LeaseDuration: 30 * time.Millisecond,
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			if _, err := waitForRenewedLease(store, window.WindowKey, window.LeaseUntil, 500*time.Millisecond); err != nil {
				return WindowExecutionResult{}, err
			}
			return WindowExecutionResult{}, errors.New("execution failed")
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if !ran {
		t.Fatalf("dispatcher did not run an eligible window")
	}
	if err == nil || err.Error() != "execution failed" {
		t.Fatalf("run next error = %v, want execution failed", err)
	}
	failedWindow, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get failed window: %v", err)
	}
	if failedWindow == nil || failedWindow.Status != collectorpkg.GovernanceWindowStatusQueued {
		t.Fatalf("failed window = %+v, want queued", failedWindow)
	}
	if failedWindow.LeaseOwner != "" || !failedWindow.LeaseUntil.IsZero() {
		t.Fatalf("failed window lease was not cleared: %+v", failedWindow)
	}

	time.Sleep(70 * time.Millisecond)
	afterStop, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window after renewal stop: %v", err)
	}
	if afterStop.LeaseOwner != "" || !afterStop.LeaseUntil.IsZero() {
		t.Fatalf("lease renewal continued after execution error: %+v", afterStop)
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
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusQueued {
		t.Fatalf("audit window = %+v, want still queued", audit)
	}
}

func waitForRenewedLease(store *collectorpkg.GovernanceStore, windowKey string, initialLease time.Time, timeout time.Duration) (time.Time, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			return time.Time{}, errors.New("lease was not renewed before timeout")
		case <-ticker.C:
			window, err := store.GetWindowByKey(windowKey)
			if err != nil {
				return time.Time{}, err
			}
			if window != nil && window.LeaseUntil.After(initialLease) {
				return window.LeaseUntil, nil
			}
		}
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

func TestWindowDispatcherRunsIndependentWindowAfterBlockedDependency(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 25, 1, 0, 0, 0, time.Local)
	targetWindow := "20260521,20260522"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	auditKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, targetWindow)
	billboardKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobMarketBillboardSync, targetWindow)
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    closeKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: targetWindow,
			DueAt:        now.Add(-3 * time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusTerminalFailed,
			LastError:    context.DeadlineExceeded.Error(),
		},
		{
			WindowKey:     auditKey,
			JobName:       string(collectorpkg.GovernanceJobDailyAudit),
			TargetWindow:  targetWindow,
			DueAt:         now.Add(-2 * time.Hour),
			Priority:      4,
			Status:        collectorpkg.GovernanceWindowStatusQueued,
			DependencyKey: closeKey,
			EnqueuedAt:    now.Add(-2 * time.Hour),
		},
		{
			WindowKey:    billboardKey,
			JobName:      string(collectorpkg.GovernanceJobMarketBillboardSync),
			TargetWindow: targetWindow,
			DueAt:        now.Add(-time.Hour),
			Priority:     6,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-time.Hour),
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}

	var executed []string
	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			executed = append(executed, window.WindowKey)
			return WindowExecutionResult{Status: collectorpkg.GovernanceWindowStatusPassed, Summary: "ok"}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not run an independent eligible window")
	}
	if len(executed) != 1 || executed[0] != billboardKey {
		t.Fatalf("executed = %+v, want [%s]", executed, billboardKey)
	}
	audit, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit == nil || audit.Status != collectorpkg.GovernanceWindowStatusWaitingDependency {
		t.Fatalf("audit window = %+v, want waiting_dependency", audit)
	}
	billboard, err := store.GetWindowByKey(billboardKey)
	if err != nil {
		t.Fatalf("get billboard window: %v", err)
	}
	if billboard == nil || billboard.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("billboard window = %+v, want passed", billboard)
	}
}

func TestWindowDispatcherSkipsRetryExhaustedWindowAndRunsIndependentWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 25, 1, 30, 0, 0, time.Local)
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	billboardKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobMarketBillboardSync, "20260521,20260522")
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:     closeKey,
			JobName:       string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow:  "20260427,20260428",
			DueAt:         now.Add(-7 * 24 * time.Hour),
			Priority:      3,
			Status:        collectorpkg.GovernanceWindowStatusQueued,
			Attempts:      3,
			LastError:     "context deadline exceeded",
			ResultSummary: "durable run ended with status=interrupted; dependency state allows replay",
			EnqueuedAt:    now.Add(-7 * 24 * time.Hour),
		},
		{
			WindowKey:    billboardKey,
			JobName:      string(collectorpkg.GovernanceJobMarketBillboardSync),
			TargetWindow: "20260521,20260522",
			DueAt:        now.Add(-time.Hour),
			Priority:     6,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-time.Hour),
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}

	var executed []string
	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			executed = append(executed, window.WindowKey)
			return WindowExecutionResult{Status: collectorpkg.GovernanceWindowStatusPassed, Summary: "ok"}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not run an independent eligible window")
	}
	if len(executed) != 1 || executed[0] != billboardKey {
		t.Fatalf("executed = %+v, want [%s]", executed, billboardKey)
	}
	closeWindow, err := store.GetWindowByKey(closeKey)
	if err != nil {
		t.Fatalf("get close window: %v", err)
	}
	if closeWindow == nil || closeWindow.Status != collectorpkg.GovernanceWindowStatusTerminalFailed {
		t.Fatalf("close window = %+v, want terminal_failed", closeWindow)
	}
	if closeWindow.LastError != "retry budget exhausted after 3 attempts: context deadline exceeded" {
		t.Fatalf("close last error = %q", closeWindow.LastError)
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

func TestWindowDispatcherDoesNotStartQueuedWindowBehindExpiredWindowWithRunningRun(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	runningKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260420")
	queuedKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    runningKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: "20260420",
			DueAt:        now.Add(-2 * time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusRunning,
			StartedAt:    now.Add(-2 * time.Hour),
			LeaseOwner:   "old-dispatcher",
			LeaseUntil:   now.Add(-time.Minute),
		},
		{
			WindowKey:    queuedKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: "20260427,20260428",
			DueAt:        now.Add(-time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-time.Hour),
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        "run-close-active",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: "20260420",
		StartedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed running run: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("dispatcher should not start %s while expired window still has a running run", window.WindowKey)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher ran a queued window while an expired window still has a running run")
	}
	queued, err := store.GetWindowByKey(queuedKey)
	if err != nil {
		t.Fatalf("get queued window: %v", err)
	}
	if queued == nil || queued.Status != collectorpkg.GovernanceWindowStatusQueued {
		t.Fatalf("queued window = %+v, want still queued", queued)
	}
}

func TestWindowDispatcherDoesNotStartQueuedWindowWhileAnotherWindowIsRunning(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.Local)
	runningKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260420")
	queuedKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	for _, window := range []collectorpkg.GovernanceWindowRecord{
		{
			WindowKey:    runningKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: "20260420",
			DueAt:        now.Add(-2 * time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusRunning,
			StartedAt:    now.Add(-time.Hour),
			LeaseOwner:   "active-dispatcher",
			LeaseUntil:   now.Add(time.Hour),
		},
		{
			WindowKey:    queuedKey,
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			TargetWindow: "20260427,20260428",
			DueAt:        now.Add(-time.Hour),
			Priority:     3,
			Status:       collectorpkg.GovernanceWindowStatusQueued,
			EnqueuedAt:   now.Add(-time.Hour),
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
		}
	}
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        "run-close-active",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: "20260420",
		StartedAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed running run: %v", err)
	}

	dispatcher := NewWindowDispatcher(WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner: "test-dispatcher",
		Execute: func(ctx context.Context, window collectorpkg.GovernanceWindowRecord) (WindowExecutionResult, error) {
			t.Fatalf("dispatcher should not start %s while another window is running", window.WindowKey)
			return WindowExecutionResult{}, nil
		},
	})

	ran, err := dispatcher.RunNext(context.Background())
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if ran {
		t.Fatalf("dispatcher ran a queued window while another window is running")
	}
	queued, err := store.GetWindowByKey(queuedKey)
	if err != nil {
		t.Fatalf("get queued window: %v", err)
	}
	if queued == nil || queued.Status != collectorpkg.GovernanceWindowStatusQueued {
		t.Fatalf("queued window = %+v, want still queued", queued)
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
