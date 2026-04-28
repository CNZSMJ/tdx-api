package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

func TestGovernanceWindowDispatcherRunsQueuedAuditAfterDependencyPasses(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalAudit := dailyAudit
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		dailyAudit = originalAudit
		governanceWindowDispatcher = originalDispatcher
	}()

	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	governancePaths = paths

	now := time.Date(2026, 4, 29, 1, 0, 0, 0, time.Local)
	executedDates := make([]string, 0, 2)
	dailyAudit, err = systemgov.NewDailyAuditRunner(systemgov.DailyAuditConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return now
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("dispatcher should execute audit with dates from the durable window")
			return nil, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*systemgov.AuditResult, error) {
			executedDates = append(executedDates, date)
			return &systemgov.AuditResult{Date: date, Status: "passed"}, nil
		},
	})
	if err != nil {
		t.Fatalf("new daily audit runner: %v", err)
	}

	targetWindow := "20260427,20260428"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    closeKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-7 * time.Hour),
		Priority:     collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceWindowStatusPartial,
		EndedAt:      now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("seed close sync window: %v", err)
	}
	auditKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, targetWindow)
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:     auditKey,
		JobName:       string(collectorpkg.GovernanceJobDailyAudit),
		TargetWindow:  targetWindow,
		DueAt:         now.Add(-6 * time.Hour),
		Priority:      collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyAudit),
		Status:        collectorpkg.GovernanceWindowStatusWaitingDependency,
		DependencyKey: closeKey,
		EnqueuedAt:    now.Add(-6 * time.Hour),
	}); err != nil {
		t.Fatalf("seed audit window: %v", err)
	}

	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
		Owner:   "test-web-dispatcher",
		Execute: executeGovernanceWindow,
	})

	ran, err := runGovernanceWindowDispatcherWithContext(context.Background(), "test-dispatcher")
	if err != nil {
		t.Fatalf("run dispatcher: %v", err)
	}
	if !ran {
		t.Fatalf("dispatcher did not run queued audit window")
	}
	if len(executedDates) != 2 || executedDates[0] != "20260427" || executedDates[1] != "20260428" {
		t.Fatalf("executed dates = %+v, want [20260427 20260428]", executedDates)
	}
	window, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusPassed || window.RunID == "" {
		t.Fatalf("audit window = %+v, want passed with run id", window)
	}
}
