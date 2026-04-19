package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

func TestRunGovernanceRepairWorkerExecutesStartupMissedWindowTask(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyOpenRefresh := dailyOpenRefresh
	originalDailyCloseSync := dailyCloseSync
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyOpenRefresh = originalDailyOpenRefresh
		dailyCloseSync = originalDailyCloseSync
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	calls := 0
	dailyOpenRefresh, err = systemgov.NewDailyOpenRefreshRunner(systemgov.DailyOpenRefreshConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 9, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		CodesRefresh: func(ctx context.Context) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new daily open refresh runner: %v", err)
	}

	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:missed:daily_open_refresh:20260421",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       string(collectorpkg.GovernanceJobDailyOpenRefresh),
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "missed governance window queued for recovery",
		TargetWindow: "20260421",
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	initGovernanceRepairWorker()

	updated, err := runGovernanceRepairWorker("startup", 1)
	if err != nil {
		t.Fatalf("run governance repair worker: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("updated tasks = %d, want 1", len(updated))
	}
	if calls != 1 {
		t.Fatalf("daily open refresh calls = %d, want 1", calls)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("unexpected tasks after repair worker run: %+v", tasks)
	}

	runs, err := store.ListRecentRuns(10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	foundOpenRefresh := false
	for _, run := range runs {
		if run.JobName == string(collectorpkg.GovernanceJobDailyOpenRefresh) {
			foundOpenRefresh = true
			break
		}
	}
	if !foundOpenRefresh {
		t.Fatalf("expected startup compensation to execute a daily_open_refresh run, got %+v", runs)
	}
}

func TestRunGovernanceRepairWorkerExecutesStartupMissedCloseSyncTaskWithStoredWindow(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyCloseSync := dailyCloseSync
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyCloseSync = originalDailyCloseSync
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	var receivedDates []string
	dailyCloseSync, err = systemgov.NewDailyCloseSyncRunner(systemgov.DailyCloseSyncConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 9, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("missed-window replay should use stored target window, not resolve current dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			receivedDates = append(receivedDates, dates...)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new daily close sync runner: %v", err)
	}

	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:missed:daily_close_sync:20260417,20260418",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "missed governance window queued for recovery",
		TargetWindow: "20260417,20260418",
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	initGovernanceRepairWorker()

	updated, err := runGovernanceRepairWorker("startup", 1)
	if err != nil {
		t.Fatalf("run governance repair worker: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("updated tasks = %d, want 1", len(updated))
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260417" || receivedDates[1] != "20260418" {
		t.Fatalf("received close-sync dates = %+v, want [20260417 20260418]", receivedDates)
	}
}

func TestRunGovernanceRepairWorkerReplaysInterruptedCloseSyncRun(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyCloseSync := dailyCloseSync
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyCloseSync = originalDailyCloseSync
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	var receivedDates []string
	dailyCloseSync, err = systemgov.NewDailyCloseSyncRunner(systemgov.DailyCloseSyncConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 9, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("interrupted-run replay should use original target window, not resolve current dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			receivedDates = append(receivedDates, dates...)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new daily close sync runner: %v", err)
	}

	interruptedRun := collectorpkg.GovernanceRunRecord{
		RunID:        "interrupted-close-sync-run",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusInterrupted,
		Trigger:      "daily-18:00",
		TargetWindow: "20260417,20260418",
		StartedAt:    time.Date(2026, 4, 18, 18, 0, 0, 0, time.Local),
		EndedAt:      time.Date(2026, 4, 18, 18, 5, 0, 0, time.Local),
	}
	if err := store.AddRun(&interruptedRun); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:interrupted:interrupted-close-sync-run",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       "interrupted_run",
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       interruptedRun.RunID,
		TargetWindow: "20260421",
	}); err != nil {
		t.Fatalf("seed interrupted-run task: %v", err)
	}

	initGovernanceRepairWorker()

	updated, err := runGovernanceRepairWorker("startup", 1)
	if err != nil {
		t.Fatalf("run governance repair worker: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("updated tasks = %d, want 1", len(updated))
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260417" || receivedDates[1] != "20260418" {
		t.Fatalf("received close-sync dates = %+v, want [20260417 20260418]", receivedDates)
	}
}
