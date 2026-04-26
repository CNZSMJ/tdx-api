package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
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
	if calls != 0 {
		t.Fatalf("daily open refresh calls = %d, want 0", calls)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != collectorpkg.GovernanceTaskStatusDegraded {
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
	if foundOpenRefresh {
		t.Fatalf("startup compensation should not execute a daily_open_refresh run, got %+v", runs)
	}
}

func TestRunGovernanceRepairWorkerDoesNotReplayMissedCloseSyncTask(t *testing.T) {
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("task status = %s, want degraded", updated[0].Status)
	}
	if len(receivedDates) != 0 {
		t.Fatalf("received close-sync dates = %+v, want none", receivedDates)
	}
}

func TestRunGovernanceRepairWorkerDoesNotReplayInterruptedCloseSyncRun(t *testing.T) {
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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("task status = %s, want degraded", updated[0].Status)
	}
	if len(receivedDates) != 0 {
		t.Fatalf("received close-sync dates = %+v, want none", receivedDates)
	}
}

func TestRunGovernanceRepairWorkerDoesNotReplayMissedDailyAudit(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyAudit := dailyAudit
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyAudit = originalDailyAudit
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	dailyAudit = nil

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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("task status = %s, want degraded", updated[0].Status)
	}
}

func TestRunGovernanceRepairWorkerDoesNotReplayInterruptedDailyAudit(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	originalRepairWorker := repairWorker
	originalDailyAudit := dailyAudit
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
		repairWorker = originalRepairWorker
		dailyAudit = originalDailyAudit
	}()

	tmp := t.TempDir()
	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	store, err := collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	dailyAudit = nil

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
	if updated[0].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("task status = %s, want degraded", updated[0].Status)
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

func TestDegradeStaleStartupRecoveryTasks(t *testing.T) {
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
		t.Fatalf("seed non-startup task: %v", err)
	}

	count, err := degradeStaleStartupRecoveryTasks("deferred stale task")
	if err != nil {
		t.Fatalf("degrade stale startup recovery tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("degraded task count = %d, want 1", count)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	byKey := make(map[string]collectorpkg.GovernanceTaskRecord, len(tasks))
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if byKey["startup_recovery:interrupted:close-sync-run"].Status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("startup task status = %s, want degraded", byKey["startup_recovery:interrupted:close-sync-run"].Status)
	}
	if byKey["daily_audit:order_history:20260424"].Status != collectorpkg.GovernanceTaskStatusInProgress {
		t.Fatalf("non-startup task status = %s, want in_progress", byKey["daily_audit:order_history:20260424"].Status)
	}
}

func TestClassifyRepairAuditDomainStatusMarksRetryablePartialAsDegraded(t *testing.T) {
	status := classifyRepairAuditDomainStatus("partial", true, []string{"sh515643: timeout", "sh515644: EOF"})
	if status != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("status = %s, want degraded", status)
	}
}

func TestClassifyRepairAuditDomainStatusKeepsNonRetryablePartialOpen(t *testing.T) {
	status := classifyRepairAuditDomainStatus("partial", true, []string{"sh600000: schema mismatch"})
	if status != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("status = %s, want open", status)
	}
}
