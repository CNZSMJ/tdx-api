package collector

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGovernanceRepairBatchDryRunDoesNotApplyChanges(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		fakeGovernanceRepairOperation{},
	})
	if err != nil {
		t.Fatalf("run dry-run repair: %v", err)
	}
	if result.Mode != GovernanceRepairModeDryRun || result.BackupPath != "" {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 0 {
		t.Fatalf("unexpected dry-run operation summary: %+v", result.Operations)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("dry-run wrote tasks: %+v", tasks)
	}
}

func TestGovernanceRepairBatchApplyBacksUpBeforeApplyingChanges(t *testing.T) {
	baseDir := t.TempDir()
	paths := ResolveGovernancePaths(baseDir)
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(baseDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		fakeGovernanceRepairOperation{},
	})
	if err != nil {
		t.Fatalf("run apply repair: %v", err)
	}
	if result.Mode != GovernanceRepairModeApply || result.BackupPath == "" {
		t.Fatalf("apply did not report backup: %+v", result)
	}
	if _, err := OpenGovernanceStore(result.BackupPath); err != nil {
		t.Fatalf("backup is not a readable governance DB: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply operation summary: %+v", result.Operations)
	}

	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskKey != "repair:test" {
		t.Fatalf("apply did not persist repair task: %+v", tasks)
	}
}

func TestGovernanceRepairBatchDryRunUsesReadOnlyStore(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	_, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		writingPlanGovernanceRepairOperation{},
	})
	if err == nil {
		t.Fatalf("dry-run allowed a write during planning")
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("dry-run persisted a planning write: %+v", tasks)
	}
}

func TestStaleGovernanceLockMetadataRepairClearsOnlyReleasedLockMetadata(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	if err := store.RecordLockMetadata(&GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       12345,
		HolderHostname:  "localhost",
		HolderJobName:   string(GovernanceJobStartupRecovery),
		HolderRunID:     "startup-recovery-ended",
		AcquiredAt:      now.Add(-time.Hour),
		LastHeartbeatAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed stale lock metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		StaleGovernanceLockMetadataRepair{LockPath: paths.LockPath},
	})
	if err != nil {
		t.Fatalf("repair stale lock metadata: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected repair result: %+v", result)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	lock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("latest lock metadata: %v", err)
	}
	if lock != nil {
		t.Fatalf("stale lock metadata was not cleared: %+v", lock)
	}
}

func TestStaleGovernanceLockMetadataRepairRefusesActiveLock(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	if err := store.RecordLockMetadata(&GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       12345,
		HolderHostname:  "localhost",
		HolderJobName:   string(GovernanceJobStartupRecovery),
		HolderRunID:     "startup-recovery-running",
		AcquiredAt:      now.Add(-time.Minute),
		LastHeartbeatAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed lock metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}
	heldLock, err := AcquireGovernanceLock(paths.LockPath)
	if err != nil {
		t.Fatalf("acquire real governance lock: %v", err)
	}
	defer heldLock.Release()

	_, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		StaleGovernanceLockMetadataRepair{LockPath: paths.LockPath},
	})
	if err == nil || !strings.Contains(err.Error(), "active governance lock") {
		t.Fatalf("expected active lock refusal, got %v", err)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	lock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("latest lock metadata: %v", err)
	}
	if lock == nil || lock.HolderRunID != "startup-recovery-running" {
		t.Fatalf("active lock metadata was changed: %+v", lock)
	}
}

func TestStartupRecoveryDeferredBacklogRepairReopensOnlyLegacyDeferredTasks(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	for _, task := range []GovernanceTaskRecord{
		{
			TaskKey:      "startup_recovery:missed:daily_audit:20260427,20260428",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       string(GovernanceJobDailyAudit),
			Status:       GovernanceTaskStatusDegraded,
			Priority:     1,
			Reason:       "full replay is deferred until startup recovery replay is available",
			TargetWindow: "20260427,20260428",
		},
		{
			TaskKey:      "startup_recovery:interrupted:daily-audit-run",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       "interrupted_run",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     1,
			Reason:       "startup recovery replay was deferred after restart",
			TargetWindow: "20260428,20260429",
		},
		{
			TaskKey:      "startup_recovery:missed:daily_close_sync:20260428,20260429",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       string(GovernanceJobDailyCloseSync),
			Status:       GovernanceTaskStatusDegraded,
			Priority:     1,
			Reason:       "provider timeout remains degraded",
			TargetWindow: "20260428,20260429",
		},
		{
			TaskKey:      "daily_audit:quote_snapshot:20260428",
			JobName:      string(GovernanceJobDailyAudit),
			Domain:       "quote_snapshot",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     4,
			Reason:       "full replay is deferred until product policy exists",
			TargetWindow: "20260428",
		},
	} {
		task := task
		if err := store.UpsertTask(&task); err != nil {
			t.Fatalf("seed task %s: %v", task.TaskKey, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		StartupRecoveryDeferredBacklogRepair{},
	})
	if err != nil {
		t.Fatalf("dry-run deferred backlog repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 2 || result.Operations[0].Applied != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}

	result, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		StartupRecoveryDeferredBacklogRepair{},
	})
	if err != nil {
		t.Fatalf("apply deferred backlog repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 2 || result.Operations[0].Applied != 2 {
		t.Fatalf("unexpected apply result: %+v", result)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	openTasks, err := store.ListTasksByStatus(GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	openByKey := make(map[string]GovernanceTaskRecord, len(openTasks))
	for _, task := range openTasks {
		openByKey[task.TaskKey] = task
	}
	for _, key := range []string{
		"startup_recovery:missed:daily_audit:20260427,20260428",
	} {
		task, ok := openByKey[key]
		if !ok {
			t.Fatalf("expected task %s to be reopened; open=%+v", key, openTasks)
		}
		if task.Reason != "legacy startup recovery deferred backlog requeued" {
			t.Fatalf("task %s reason = %q", key, task.Reason)
		}
	}
	interrupted, ok := openByKey["startup_recovery:interrupted:daily-audit-run"]
	if !ok {
		t.Fatalf("expected interrupted task to be reopened; open=%+v", openTasks)
	}
	if interrupted.Reason != "daily-audit-run" {
		t.Fatalf("interrupted task reason = %q, want original run id", interrupted.Reason)
	}
	degradedTasks, err := store.ListTasksByStatus(GovernanceTaskStatusDegraded)
	if err != nil {
		t.Fatalf("list degraded tasks: %v", err)
	}
	degradedByKey := make(map[string]GovernanceTaskRecord, len(degradedTasks))
	for _, task := range degradedTasks {
		degradedByKey[task.TaskKey] = task
	}
	for _, key := range []string{
		"startup_recovery:missed:daily_close_sync:20260428,20260429",
		"daily_audit:quote_snapshot:20260428",
	} {
		if _, ok := degradedByKey[key]; !ok {
			t.Fatalf("expected task %s to stay degraded; degraded=%+v", key, degradedTasks)
		}
	}
}

func fixedRepairNow() time.Time {
	return time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)
}

type fakeGovernanceRepairOperation struct{}

func (fakeGovernanceRepairOperation) Name() string {
	return "fake_repair"
}

func (fakeGovernanceRepairOperation) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	return []GovernanceRepairChange{{
		Operation: "fake_repair",
		Target:    "repair:test",
		Action:    "upsert_open_task",
		Reason:    "test repair operation",
	}}, nil
}

func (fakeGovernanceRepairOperation) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey: "repair:test",
		JobName: string(GovernanceJobStartupRecovery),
		Domain:  "test",
		Status:  GovernanceTaskStatusOpen,
		Reason:  "test repair operation",
	})
	if err != nil {
		return nil, err
	}
	return []GovernanceRepairChange{{
		Operation: "fake_repair",
		Target:    "repair:test",
		Action:    "upsert_open_task",
		Reason:    "test repair operation",
	}}, nil
}

type writingPlanGovernanceRepairOperation struct{}

func (writingPlanGovernanceRepairOperation) Name() string {
	return "bad_plan"
}

func (writingPlanGovernanceRepairOperation) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey: "repair:bad-plan",
		JobName: string(GovernanceJobStartupRecovery),
		Domain:  "test",
		Status:  GovernanceTaskStatusOpen,
		Reason:  "bad dry-run write",
	})
	return nil, err
}

func (writingPlanGovernanceRepairOperation) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	return nil, nil
}
