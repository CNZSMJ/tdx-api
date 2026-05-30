package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
	"github.com/injoyai/tdx/market/billboard"
)

func TestRunGovernanceRepairWorkerDoesNotReplayMissedOpenRefreshTask(t *testing.T) {
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusUnsupported {
		t.Fatalf("task status = %s, want unsupported", updated[0].Status)
	}
	if calls != 0 {
		t.Fatalf("daily open refresh calls = %d, want 0", calls)
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
	if foundOpenRefresh {
		t.Fatalf("startup compensation should not execute a daily_open_refresh run, got %+v", runs)
	}
}

func TestRunGovernanceRepairWorkerReplaysMissedCloseSyncTask(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyCloseSync := dailyCloseSync
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyCloseSync = originalDailyCloseSync
		governanceWindowDispatcher = originalDispatcher
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
	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store:   store,
		Now:     time.Now,
		Owner:   "test-repair-worker",
		Execute: executeGovernanceWindow,
	})

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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", updated[0].Status)
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260417" || receivedDates[1] != "20260418" {
		t.Fatalf("received close-sync dates = %+v, want [20260417 20260418]", receivedDates)
	}
	window, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260417,20260418"))
	if err != nil {
		t.Fatalf("get replay window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("replay window = %+v, want passed", window)
	}
}

func TestRunGovernanceRepairWorkerReplaysMissedMarketBillboardTask(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalMarketBillboardSync := marketBillboardSync
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		marketBillboardSync = originalMarketBillboardSync
		governanceWindowDispatcher = originalDispatcher
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
	marketBillboardSync, err = systemgov.NewMarketBillboardSyncRunner(systemgov.MarketBillboardSyncConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 5, 22, 21, 35, 0, 0, time.Local)
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time, count int) ([]string, error) {
			t.Fatalf("missed-window replay should use stored target window, not resolve current dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) (billboard.SyncResult, error) {
			receivedDates = append(receivedDates, dates...)
			return billboard.SyncResult{StartDate: dates[0], EndDate: dates[len(dates)-1]}, nil
		},
	})
	if err != nil {
		t.Fatalf("new market billboard sync runner: %v", err)
	}
	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store:   store,
		Now:     time.Now,
		Owner:   "test-repair-worker",
		Execute: executeGovernanceWindow,
	})

	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:missed:market_billboard_sync:20260521,20260522",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       string(collectorpkg.GovernanceJobMarketBillboardSync),
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "missed governance window queued for recovery",
		TargetWindow: "20260521,20260522",
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", updated[0].Status)
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260521" || receivedDates[1] != "20260522" {
		t.Fatalf("received market billboard dates = %+v, want [20260521 20260522]", receivedDates)
	}
	window, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobMarketBillboardSync, "20260521,20260522"))
	if err != nil {
		t.Fatalf("get replay window: %v", err)
	}
	if window == nil || window.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("replay window = %+v, want passed", window)
	}
}

func TestRunGovernanceRepairWorkerReplaysInterruptedCloseSyncRun(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyCloseSync := dailyCloseSync
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyCloseSync = originalDailyCloseSync
		governanceWindowDispatcher = originalDispatcher
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
	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store:   store,
		Now:     time.Now,
		Owner:   "test-repair-worker",
		Execute: executeGovernanceWindow,
	})

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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", updated[0].Status)
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260417" || receivedDates[1] != "20260418" {
		t.Fatalf("received close-sync dates = %+v, want [20260417 20260418]", receivedDates)
	}
}

func TestRunGovernanceRepairWorkerReplaysMissedDailyAudit(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyAudit := dailyAudit
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyAudit = originalDailyAudit
		governanceWindowDispatcher = originalDispatcher
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	var auditedDates []string
	dailyAudit, err = systemgov.NewDailyAuditRunner(systemgov.DailyAuditConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 19, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("missed audit replay should use stored target window, not resolve current dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*systemgov.AuditResult, error) {
			auditedDates = append(auditedDates, date)
			return &systemgov.AuditResult{Date: date, Status: "passed"}, nil
		},
	})
	if err != nil {
		t.Fatalf("new daily audit runner: %v", err)
	}
	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store:   store,
		Now:     time.Now,
		Owner:   "test-repair-worker",
		Execute: executeGovernanceWindow,
	})
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260420,20260421"),
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: "20260420,20260421",
		DueAt:        time.Now().Add(-time.Hour),
		Priority:     collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceWindowStatusPassed,
		EndedAt:      time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed close-sync dependency window: %v", err)
	}

	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:missed:daily_audit:20260420,20260421",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       string(collectorpkg.GovernanceJobDailyAudit),
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "missed governance window queued for recovery",
		TargetWindow: "20260420,20260421",
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", updated[0].Status)
	}
	if len(auditedDates) != 2 || auditedDates[0] != "20260420" || auditedDates[1] != "20260421" {
		t.Fatalf("audited dates = %+v, want [20260420 20260421]", auditedDates)
	}
}

func TestRunGovernanceRepairWorkerReplaysInterruptedDailyAudit(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyAudit := dailyAudit
	originalDispatcher := governanceWindowDispatcher
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyAudit = originalDailyAudit
		governanceWindowDispatcher = originalDispatcher
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	var auditedDates []string
	dailyAudit, err = systemgov.NewDailyAuditRunner(systemgov.DailyAuditConfig{
		Store: store,
		Paths: governancePaths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 20, 5, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("interrupted audit replay should use original target window, not resolve current dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*systemgov.AuditResult, error) {
			auditedDates = append(auditedDates, date)
			return &systemgov.AuditResult{Date: date, Status: "passed"}, nil
		},
	})
	if err != nil {
		t.Fatalf("new daily audit runner: %v", err)
	}
	governanceWindowDispatcher = systemgov.NewWindowDispatcher(systemgov.WindowDispatcherConfig{
		Store:   store,
		Now:     time.Now,
		Owner:   "test-repair-worker",
		Execute: executeGovernanceWindow,
	})
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260420,20260421"),
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: "20260420,20260421",
		DueAt:        time.Now().Add(-time.Hour),
		Priority:     collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceWindowStatusPassed,
		EndedAt:      time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed close-sync dependency window: %v", err)
	}

	interruptedRun := collectorpkg.GovernanceRunRecord{
		RunID:        "interrupted-daily-audit-run",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Status:       collectorpkg.GovernanceRunStatusInterrupted,
		Trigger:      "daily-19:00",
		TargetWindow: "20260420,20260421",
		StartedAt:    time.Date(2026, 4, 21, 19, 0, 0, 0, time.Local),
		EndedAt:      time.Date(2026, 4, 21, 20, 0, 0, 0, time.Local),
	}
	if err := store.AddRun(&interruptedRun); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:interrupted:interrupted-daily-audit-run",
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", updated[0].Status)
	}
	if len(auditedDates) != 2 || auditedDates[0] != "20260420" || auditedDates[1] != "20260421" {
		t.Fatalf("audited dates = %+v, want [20260420 20260421]", auditedDates)
	}
}

func TestExecuteDailyAuditRepairTaskDegradesRetryableReasonWithoutReconcile(t *testing.T) {
	originalRuntime := collectorRuntime
	defer func() {
		collectorRuntime = originalRuntime
	}()
	collectorRuntime = nil

	status, reason, err := executeDailyAuditRepairTask(context.Background(), collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_audit:live_capture:20260416",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "live_capture",
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Reason:       "sh515643: 超时; sh515644: EOF",
		TargetWindow: "20260416",
	})
	if err != nil {
		t.Fatalf("execute daily audit repair task: %v", err)
	}
	if status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("status = %s, want degraded", status)
	}
	if reason != "sh515643: 超时; sh515644: EOF" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestExecuteDailyAuditRepairTaskRespectsGovernanceLock(t *testing.T) {
	originalRuntime := collectorRuntime
	originalPaths := governancePaths
	defer func() {
		collectorRuntime = originalRuntime
		governancePaths = originalPaths
	}()

	governancePaths = collectorpkg.ResolveGovernancePaths(t.TempDir())
	lock, err := collectorpkg.AcquireGovernanceLock(governancePaths.LockPath)
	if err != nil {
		t.Fatalf("acquire governance lock: %v", err)
	}
	defer lock.Release()
	collectorRuntime = nil

	status, _, err := executeDailyAuditRepairTask(context.Background(), collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_audit:f10:20260420",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "f10",
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Reason:       "sh600000: schema mismatch",
		TargetWindow: "20260420",
	})
	if err == nil || !collectorpkg.IsGovernanceLockHeld(err) {
		t.Fatalf("err = %v, want governance lock held", err)
	}
	if status != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("status = %s, want open", status)
	}
}

func TestQueueMissedGovernanceWindowOnLockConflict(t *testing.T) {
	originalStore := governanceStore
	defer func() {
		governanceStore = originalStore
	}()

	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	lockErr := collectorpkg.ErrGovernanceLockHeld
	if err := upsertMissedGovernanceWindowTask(collectorpkg.GovernanceJobDailyAudit, "20260420,20260421", lockErr.Error()); err != nil {
		t.Fatalf("upsert missed governance window task: %v", err)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %+v, want one task", tasks)
	}
	task := tasks[0]
	if task.TaskKey != "startup_recovery:missed:daily_audit:20260420,20260421" {
		t.Fatalf("task key = %q", task.TaskKey)
	}
	if task.JobName != string(collectorpkg.GovernanceJobStartupRecovery) || task.Domain != string(collectorpkg.GovernanceJobDailyAudit) {
		t.Fatalf("task routing = job:%s domain:%s", task.JobName, task.Domain)
	}
	if task.TargetWindow != "20260420,20260421" {
		t.Fatalf("target window = %q", task.TargetWindow)
	}
}

func TestRecoverStaleInProgressGovernanceTasks(t *testing.T) {
	originalStore := governanceStore
	defer func() {
		governanceStore = originalStore
	}()

	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:interrupted:close-sync-run",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       "interrupted_run",
		Status:       collectorpkg.GovernanceTaskStatusInProgress,
		Priority:     1,
		Reason:       "close-sync-run",
		TargetWindow: "20260424",
	}); err != nil {
		t.Fatalf("seed startup recovery task: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_audit:order_history:20260424",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "order_history",
		Status:       collectorpkg.GovernanceTaskStatusInProgress,
		Priority:     2,
		Reason:       "provider timeout",
		TargetWindow: "20260424",
	}); err != nil {
		t.Fatalf("seed stale daily audit task: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_audit:live_capture:20260416",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "live_capture",
		Status:       collectorpkg.GovernanceTaskStatusInProgress,
		Priority:     2,
		Reason:       "stale in-progress task recovered after service restart",
		TargetWindow: "20260416",
	}); err != nil {
		t.Fatalf("seed stale daily audit task with lost reason: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_close_sync:live_capture:20260424:sh600000",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Domain:       "live_capture",
		Status:       collectorpkg.GovernanceTaskStatusInProgress,
		Priority:     2,
		Reason:       "context canceled",
		TargetWindow: "20260424",
	}); err != nil {
		t.Fatalf("seed bounded close sync task: %v", err)
	}

	counts, err := recoverStaleInProgressGovernanceTasks("deferred stale task")
	if err != nil {
		t.Fatalf("recover stale in-progress governance tasks: %v", err)
	}
	if counts.Degraded != 2 || counts.Reopened != 2 {
		t.Fatalf("recovery counts = %+v, want degraded=2 reopened=2", counts)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	byKey := make(map[string]collectorpkg.GovernanceTaskRecord, len(tasks))
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if byKey["startup_recovery:interrupted:close-sync-run"].Status != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("startup task status = %s, want open", byKey["startup_recovery:interrupted:close-sync-run"].Status)
	}
	if byKey["daily_audit:order_history:20260424"].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("stale daily audit status = %s, want degraded", byKey["daily_audit:order_history:20260424"].Status)
	}
	if byKey["daily_close_sync:live_capture:20260424:sh600000"].Status != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("bounded close sync status = %s, want open", byKey["daily_close_sync:live_capture:20260424:sh600000"].Status)
	}
	if byKey["daily_close_sync:live_capture:20260424:sh600000"].Reason != "context canceled" {
		t.Fatalf("bounded close sync reason = %q, want original context canceled", byKey["daily_close_sync:live_capture:20260424:sh600000"].Reason)
	}
	if byKey["daily_audit:live_capture:20260416"].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("lost-reason daily audit status = %s, want degraded", byKey["daily_audit:live_capture:20260416"].Status)
	}
}

func TestClassifyRepairAuditDomainStatusMarksRetryablePartialAsDegraded(t *testing.T) {
	status := classifyRepairAuditDomainStatus("partial", true, []string{"sh515643: timeout", "sh515644: EOF"})
	if status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("status = %s, want degraded", status)
	}
}

func TestDailyAuditRetryableErrorsIncludeContextCanceled(t *testing.T) {
	if !hasOnlyRetryableAuditErrors([]string{"context canceled"}) {
		t.Fatal("context canceled should be treated as retryable audit error")
	}
}

func TestCloseSyncRepairErrorsDegradeProviderTimeouts(t *testing.T) {
	for _, message := range []string{"超时", "数据长度不足", "context canceled"} {
		if !isRetryableProviderRepairError(message) {
			t.Fatalf("%q should be treated as retryable provider repair error", message)
		}
	}
}

func TestClassifyRepairAuditDomainStatusKeepsNonRetryablePartialOpen(t *testing.T) {
	status := classifyRepairAuditDomainStatus("partial", true, []string{"sh600000: schema mismatch"})
	if status != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("status = %s, want open", status)
	}
}
