package governance

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestStartupRecoveryQueuesMissedWindowsAndInterruptedRuns(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewStartupRecoveryRunner(StartupRecoveryConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 8, 0, 0, 0, time.Local)
		},
		Inspect: func(ctx context.Context, now time.Time) (StartupRecoverySnapshot, error) {
			return StartupRecoverySnapshot{
				MissedJobs: []StartupRecoveryMissedJob{
					{Job: collectorpkg.GovernanceJobDailyOpenRefresh, TargetWindow: "20260421"},
					{Job: collectorpkg.GovernanceJobDailyCloseSync, TargetWindow: "20260418,20260421"},
					{Job: collectorpkg.GovernanceJobDailyAudit, TargetWindow: "20260418,20260421"},
				},
				InterruptedRuns:  []string{"run-1"},
				OpenBacklogCount: 2,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new startup runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "startup")
	if err != nil {
		t.Fatalf("run startup recovery: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("task count = %d, want 4", len(tasks))
	}
	targets := make(map[string]string, len(tasks))
	for _, task := range tasks {
		targets[task.Domain] = task.TargetWindow
	}
	if targets[string(collectorpkg.GovernanceJobDailyOpenRefresh)] != "20260421" {
		t.Fatalf("open refresh target window = %q, want 20260421", targets[string(collectorpkg.GovernanceJobDailyOpenRefresh)])
	}
	if targets[string(collectorpkg.GovernanceJobDailyCloseSync)] != "20260418,20260421" {
		t.Fatalf("close sync target window = %q, want 20260418,20260421", targets[string(collectorpkg.GovernanceJobDailyCloseSync)])
	}
	if targets[string(collectorpkg.GovernanceJobDailyAudit)] != "20260418,20260421" {
		t.Fatalf("daily audit target window = %q, want 20260418,20260421", targets[string(collectorpkg.GovernanceJobDailyAudit)])
	}

	windows, err := store.ListWindowsByStatus(collectorpkg.GovernanceWindowStatusQueued)
	if err != nil {
		t.Fatalf("list queued windows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("queued windows = %d, want 3", len(windows))
	}
	windowTargets := make(map[string]string, len(windows))
	windowDependencies := make(map[string]string, len(windows))
	for _, window := range windows {
		windowTargets[window.JobName] = window.TargetWindow
		windowDependencies[window.JobName] = window.DependencyKey
	}
	if windowTargets[string(collectorpkg.GovernanceJobDailyOpenRefresh)] != "20260421" {
		t.Fatalf("open refresh window = %q, want 20260421", windowTargets[string(collectorpkg.GovernanceJobDailyOpenRefresh)])
	}
	if windowTargets[string(collectorpkg.GovernanceJobDailyCloseSync)] != "20260418,20260421" {
		t.Fatalf("close sync window = %q, want 20260418,20260421", windowTargets[string(collectorpkg.GovernanceJobDailyCloseSync)])
	}
	if windowDependencies[string(collectorpkg.GovernanceJobDailyAudit)] != collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260418,20260421") {
		t.Fatalf("daily audit dependency = %q, want close-sync dependency", windowDependencies[string(collectorpkg.GovernanceJobDailyAudit)])
	}
}

func TestStartupRecoveryPassesWhenNoBacklogExists(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewStartupRecoveryRunner(StartupRecoveryConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 8, 0, 0, 0, time.Local)
		},
		Inspect: func(ctx context.Context, now time.Time) (StartupRecoverySnapshot, error) {
			return StartupRecoverySnapshot{}, nil
		},
	})
	if err != nil {
		t.Fatalf("new startup runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "startup")
	if err != nil {
		t.Fatalf("run startup recovery: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("run status = %s, want passed", run.Status)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("unexpected tasks for empty startup recovery: %+v", tasks)
	}
}
