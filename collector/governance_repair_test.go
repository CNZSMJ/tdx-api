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

func TestGovernanceRepairBackupUsesUniquePathWithinSameSecond(t *testing.T) {
	baseDir := t.TempDir()
	paths := ResolveGovernancePaths(baseDir)
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	backupDir := filepath.Join(baseDir, "backups")
	first, err := backupGovernanceRepairDB(paths.DBPath, backupDir, fixedRepairNow())
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := backupGovernanceRepairDB(paths.DBPath, backupDir, fixedRepairNow())
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if first == second {
		t.Fatalf("backup paths collided: %s", first)
	}
	if _, err := OpenGovernanceStore(first); err != nil {
		t.Fatalf("first backup is not readable: %v", err)
	}
	if _, err := OpenGovernanceStore(second); err != nil {
		t.Fatalf("second backup is not readable: %v", err)
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

func TestGovernanceRepairBatchDryRunSyncsExistingSchemaBeforeReadOnlyPlan(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	if _, err := store.engine.Exec(`ALTER TABLE governance_task DROP COLUMN Attempts`); err != nil {
		t.Fatalf("drop attempts column to simulate old schema: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		DegradedProviderBacklogRepair{},
	})
	if err != nil {
		t.Fatalf("dry-run repair on old schema: %v", err)
	}
	if result.Mode != GovernanceRepairModeDryRun || result.BackupPath != "" {
		t.Fatalf("unexpected dry-run result: %+v", result)
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

func TestTerminalGovernanceWindowRepairRequeuesOnlySafeTerminalWindows(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	closeTarget := "20260428,20260429"
	closeKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, closeTarget)
	auditMissingTarget := "20260427,20260428"
	auditMissingKey := GovernanceWindowKey(GovernanceJobDailyAudit, auditMissingTarget)
	auditReadyTarget := "20260426,20260427"
	auditReadyKey := GovernanceWindowKey(GovernanceJobDailyAudit, auditReadyTarget)
	closeReadyKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, auditReadyTarget)
	for _, window := range []GovernanceWindowRecord{
		{
			WindowKey:     closeKey,
			JobName:       string(GovernanceJobDailyCloseSync),
			TargetWindow:  closeTarget,
			DueAt:         now.Add(-3 * time.Hour),
			Priority:      3,
			Status:        GovernanceWindowStatusTerminalFailed,
			LastError:     "governance window lease expired",
			ResultSummary: "governance window lease expired",
		},
		{
			WindowKey:     auditMissingKey,
			JobName:       string(GovernanceJobDailyAudit),
			TargetWindow:  auditMissingTarget,
			DueAt:         now.Add(-2 * time.Hour),
			Priority:      4,
			Status:        GovernanceWindowStatusTerminalFailed,
			DependencyKey: GovernanceWindowKey(GovernanceJobDailyCloseSync, auditMissingTarget),
			LastError:     "missing dependency " + GovernanceWindowKey(GovernanceJobDailyCloseSync, auditMissingTarget),
			ResultSummary: "dependency missing; terminally deferred " + GovernanceWindowKey(GovernanceJobDailyCloseSync, auditMissingTarget),
		},
		{
			WindowKey:    closeReadyKey,
			JobName:      string(GovernanceJobDailyCloseSync),
			TargetWindow: auditReadyTarget,
			DueAt:        now.Add(-3 * time.Hour),
			Priority:     3,
			Status:       GovernanceWindowStatusPassed,
			RunID:        "run-close-ready",
		},
		{
			WindowKey:     auditReadyKey,
			JobName:       string(GovernanceJobDailyAudit),
			TargetWindow:  auditReadyTarget,
			DueAt:         now.Add(-2 * time.Hour),
			Priority:      4,
			Status:        GovernanceWindowStatusTerminalFailed,
			DependencyKey: closeReadyKey,
			LastError:     "context canceled",
			ResultSummary: "run_id=run-audit-interrupted status=interrupted target=" + auditReadyTarget,
		},
	} {
		window := window
		if err := store.UpsertWindow(&window); err != nil {
			t.Fatalf("seed window %s: %v", window.WindowKey, err)
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
		TerminalGovernanceWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("dry-run terminal window repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 3 || result.Operations[0].Applied != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result.Operations)
	}
	actions := make(map[string]string, len(result.Operations[0].Changes))
	for _, change := range result.Operations[0].Changes {
		actions[change.Target] = change.Action
	}
	if actions[closeKey] != "requeue_window" || actions[auditReadyKey] != "requeue_window" || actions[GovernanceWindowKey(GovernanceJobDailyCloseSync, auditMissingTarget)] != "create_missing_dependency_window" {
		t.Fatalf("unexpected planned actions: %+v", actions)
	}

	result, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		TerminalGovernanceWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply terminal window repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 3 || result.Operations[0].Applied != 3 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	for _, key := range []string{closeKey, auditReadyKey} {
		window, err := store.GetWindowByKey(key)
		if err != nil {
			t.Fatalf("get window %s: %v", key, err)
		}
		if window == nil || window.Status != GovernanceWindowStatusQueued {
			t.Fatalf("window %s = %+v, want queued", key, window)
		}
		if window.LastError != "" || window.LeaseOwner != "" || !window.LeaseUntil.IsZero() || !window.NextRunAt.IsZero() {
			t.Fatalf("window %s kept stale failure metadata: %+v", key, window)
		}
	}
	auditMissing, err := store.GetWindowByKey(auditMissingKey)
	if err != nil {
		t.Fatalf("get missing-dependency audit window: %v", err)
	}
	if auditMissing == nil || auditMissing.Status != GovernanceWindowStatusTerminalFailed {
		t.Fatalf("missing-dependency audit window = %+v, want terminal_failed", auditMissing)
	}
}

func TestTerminalGovernanceWindowRepairCreatesMissingDependencyWindow(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260427,20260428"
	auditKey := GovernanceWindowKey(GovernanceJobDailyAudit, targetWindow)
	dependencyKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:     auditKey,
		JobName:       string(GovernanceJobDailyAudit),
		TargetWindow:  targetWindow,
		DueAt:         now.Add(-2 * time.Hour),
		Priority:      4,
		Status:        GovernanceWindowStatusTerminalFailed,
		DependencyKey: dependencyKey,
		LastError:     "missing dependency " + dependencyKey,
		ResultSummary: "dependency missing; terminally deferred " + dependencyKey,
	}); err != nil {
		t.Fatalf("seed audit window: %v", err)
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
		TerminalGovernanceWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply terminal window repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}
	change := result.Operations[0].Changes[0]
	if change.Target != dependencyKey || change.Action != "create_missing_dependency_window" {
		t.Fatalf("unexpected repair change: %+v", change)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	dependency, err := store.GetWindowByKey(dependencyKey)
	if err != nil {
		t.Fatalf("get dependency window: %v", err)
	}
	if dependency == nil || dependency.Status != GovernanceWindowStatusQueued {
		t.Fatalf("dependency window = %+v, want queued", dependency)
	}
	if dependency.JobName != string(GovernanceJobDailyCloseSync) || dependency.TargetWindow != targetWindow || dependency.Priority != GovernanceJobPriority(GovernanceJobDailyCloseSync) {
		t.Fatalf("dependency window has wrong identity: %+v", dependency)
	}
	audit, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if audit == nil || audit.Status != GovernanceWindowStatusTerminalFailed {
		t.Fatalf("audit window = %+v, want terminal_failed until dependency completes", audit)
	}
}

func TestTerminalGovernanceWindowRepairClosesCoveredTerminalCloseSyncWindow(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260514,20260515"
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-time.Hour),
		Priority:     GovernanceJobPriority(GovernanceJobDailyCloseSync),
		Status:       GovernanceWindowStatusTerminalFailed,
		LastError:    "context deadline exceeded",
	}); err != nil {
		t.Fatalf("seed close sync window: %v", err)
	}
	for _, snapshot := range []DomainHealthSnapshotRecord{
		{Domain: "trade_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "live_capture", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "order_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "finance", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260512", SnapshotAt: now},
		{Domain: "f10", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "hash", SnapshotAt: now},
		{Domain: "kline", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "1778749200", SnapshotAt: now},
	} {
		snapshot := snapshot
		if err := store.UpsertDomainHealthSnapshot(&snapshot); err != nil {
			t.Fatalf("seed snapshot %s: %v", snapshot.Domain, err)
		}
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
		TerminalGovernanceWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply terminal window repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}
	if result.Operations[0].Changes[0].Action != "close_covered_window" {
		t.Fatalf("unexpected repair change: %+v", result.Operations[0].Changes[0])
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
	if window.LastError != "" || !strings.Contains(window.ResultSummary, "data health snapshots cover target") {
		t.Fatalf("window did not record coverage repair: %+v", window)
	}
}

func TestCoveredCloseSyncWindowRepairClosesCoveredQueuedWindows(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, "20260423,20260424")
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: "20260423,20260424",
		DueAt:        now.Add(-time.Hour),
		Priority:     GovernanceJobPriority(GovernanceJobDailyCloseSync),
		Status:       GovernanceWindowStatusQueued,
		LastError:    "queued replay window daily_close_sync:20260423,20260424 status=running",
	}); err != nil {
		t.Fatalf("seed close sync window: %v", err)
	}
	for _, snapshot := range []DomainHealthSnapshotRecord{
		{Domain: "trade_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "live_capture", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "order_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "finance", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260429", SnapshotAt: now},
		{Domain: "f10", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "hash", SnapshotAt: now},
		{Domain: "kline", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "1777532400", SnapshotAt: now},
	} {
		snapshot := snapshot
		if err := store.UpsertDomainHealthSnapshot(&snapshot); err != nil {
			t.Fatalf("seed snapshot %s: %v", snapshot.Domain, err)
		}
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
		CoveredCloseSyncWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered close sync repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
	if window.LastError != "" || !strings.Contains(window.ResultSummary, "data health snapshots cover target") {
		t.Fatalf("window did not record coverage repair: %+v", window)
	}
}

func TestCoveredCloseSyncWindowRepairDoesNotDateGateFinanceSnapshot(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260514,20260515"
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-time.Hour),
		Priority:     GovernanceJobPriority(GovernanceJobDailyCloseSync),
		Status:       GovernanceWindowStatusTerminalFailed,
		LastError:    "context deadline exceeded",
	}); err != nil {
		t.Fatalf("seed close sync window: %v", err)
	}
	for _, snapshot := range []DomainHealthSnapshotRecord{
		{Domain: "trade_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "live_capture", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "order_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260515", SnapshotAt: now},
		{Domain: "finance", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260512", SnapshotAt: now},
		{Domain: "f10", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "hash", SnapshotAt: now},
		{Domain: "kline", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "1778749200", SnapshotAt: now},
	} {
		snapshot := snapshot
		if err := store.UpsertDomainHealthSnapshot(&snapshot); err != nil {
			t.Fatalf("seed snapshot %s: %v", snapshot.Domain, err)
		}
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
		CoveredCloseSyncWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered close sync repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
}

func TestCoveredCloseSyncWindowRepairClosesCoveredRunningWindowAfterRunEnded(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260420,20260421"
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-time.Hour),
		Priority:     GovernanceJobPriority(GovernanceJobDailyCloseSync),
		Status:       GovernanceWindowStatusRunning,
		RunID:        "daily-close-sync-interrupted",
		LastError:    "context canceled",
	}); err != nil {
		t.Fatalf("seed close sync window: %v", err)
	}
	if err := store.AddRun(&GovernanceRunRecord{
		RunID:        "daily-close-sync-interrupted",
		JobName:      string(GovernanceJobDailyCloseSync),
		Status:       GovernanceRunStatusInterrupted,
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	seedCoveredGovernanceSnapshots(t, store, now)
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		CoveredCloseSyncWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered close sync repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
}

func TestCoveredAuditWindowRepairClosesCoveredQueuedWindows(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	windowKey := GovernanceWindowKey(GovernanceJobDailyAudit, "20260423,20260424")
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:     windowKey,
		JobName:       string(GovernanceJobDailyAudit),
		TargetWindow:  "20260423,20260424",
		DueAt:         now.Add(-time.Hour),
		Priority:      GovernanceJobPriority(GovernanceJobDailyAudit),
		Status:        GovernanceWindowStatusQueued,
		DependencyKey: GovernanceWindowKey(GovernanceJobDailyCloseSync, "20260423,20260424"),
		LastError:     "queued replay window daily_audit:20260423,20260424 status=running",
	}); err != nil {
		t.Fatalf("seed audit window: %v", err)
	}
	seedCoveredGovernanceSnapshots(t, store, now)
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		CoveredAuditWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered audit repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
	if window.LastError != "" || !strings.Contains(window.ResultSummary, "data health snapshots cover target") {
		t.Fatalf("window did not record coverage repair: %+v", window)
	}
}

func TestCoveredAuditWindowRepairClosesCoveredWaitingDependencyWindow(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260420,20260421"
	windowKey := GovernanceWindowKey(GovernanceJobDailyAudit, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:     windowKey,
		JobName:       string(GovernanceJobDailyAudit),
		TargetWindow:  targetWindow,
		DueAt:         now.Add(-time.Hour),
		Priority:      GovernanceJobPriority(GovernanceJobDailyAudit),
		Status:        GovernanceWindowStatusWaitingDependency,
		DependencyKey: GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow),
	}); err != nil {
		t.Fatalf("seed audit window: %v", err)
	}
	seedCoveredGovernanceSnapshots(t, store, now)
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		CoveredAuditWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered audit repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
}

func TestCoveredAuditWindowRepairClosesCoveredRunningWindowAfterRunEnded(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260423,20260424"
	windowKey := GovernanceWindowKey(GovernanceJobDailyAudit, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyAudit),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-time.Hour),
		Priority:     GovernanceJobPriority(GovernanceJobDailyAudit),
		Status:       GovernanceWindowStatusRunning,
		RunID:        "daily-audit-interrupted",
		LastError:    "legacy startup recovery deferred backlog requeued",
	}); err != nil {
		t.Fatalf("seed audit window: %v", err)
	}
	if err := store.AddRun(&GovernanceRunRecord{
		RunID:        "daily-audit-interrupted",
		JobName:      string(GovernanceJobDailyAudit),
		Status:       GovernanceRunStatusInterrupted,
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	seedCoveredGovernanceSnapshots(t, store, now)
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		CoveredAuditWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply covered audit repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
}

func TestCoveredBacklogRepairClosesCoveredGovernanceTasks(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	seedCoveredGovernanceSnapshots(t, store, now)
	if err := store.AddRun(&GovernanceRunRecord{
		RunID:        "daily-close-sync-interrupted",
		JobName:      string(GovernanceJobDailyCloseSync),
		Status:       GovernanceRunStatusInterrupted,
		TargetWindow: "20260427,20260428",
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	for _, task := range []GovernanceTaskRecord{
		{
			TaskKey:      "daily_audit:live_capture:20260420",
			JobName:      string(GovernanceJobDailyAudit),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusOpen,
			Priority:     3,
			Reason:       "context canceled",
			TargetWindow: "20260420",
		},
		{
			TaskKey:      "daily_close_sync:live_capture:20260428:sh512143",
			JobName:      string(GovernanceJobDailyCloseSync),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     2,
			Reason:       "provider repair exhausted",
			TargetWindow: "20260428",
		},
		{
			TaskKey:      "daily_close_sync:live_capture:20260430:sh512143",
			JobName:      string(GovernanceJobDailyCloseSync),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusOpen,
			Priority:     2,
			Reason:       "超时",
			TargetWindow: "20260430",
		},
		{
			TaskKey:      "startup_recovery:missed:daily_audit:20260423,20260424",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       string(GovernanceJobDailyAudit),
			Status:       GovernanceTaskStatusInProgress,
			Priority:     1,
			Reason:       "legacy startup recovery deferred backlog requeued",
			TargetWindow: "20260423,20260424",
		},
		{
			TaskKey:      "startup_recovery:interrupted:daily-close-sync-interrupted",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       "interrupted_run",
			Status:       GovernanceTaskStatusOpen,
			Priority:     1,
			Reason:       "daily-close-sync-interrupted",
			TargetWindow: "20260502",
		},
		{
			TaskKey:      "daily_close_sync:live_capture:20260502:sh512143",
			JobName:      string(GovernanceJobDailyCloseSync),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusOpen,
			Priority:     2,
			Reason:       "超时",
			TargetWindow: "20260502",
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
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		CoveredBacklogRepair{},
	})
	if err != nil {
		t.Fatalf("apply covered backlog repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 5 || result.Operations[0].Applied != 5 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
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
	byKey := make(map[string]GovernanceTaskRecord, len(tasks))
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	for _, key := range []string{
		"daily_audit:live_capture:20260420",
		"daily_close_sync:live_capture:20260428:sh512143",
		"daily_close_sync:live_capture:20260430:sh512143",
		"startup_recovery:missed:daily_audit:20260423,20260424",
		"startup_recovery:interrupted:daily-close-sync-interrupted",
	} {
		if byKey[key].Status != GovernanceTaskStatusClosed {
			t.Fatalf("task %s status = %s, want closed", key, byKey[key].Status)
		}
		if !strings.Contains(byKey[key].Reason, "data health snapshots cover target") {
			t.Fatalf("task %s did not record coverage repair: %+v", key, byKey[key])
		}
	}
	if byKey["daily_close_sync:live_capture:20260502:sh512143"].Status != GovernanceTaskStatusOpen {
		t.Fatalf("uncovered task was changed: %+v", byKey["daily_close_sync:live_capture:20260502:sh512143"])
	}
}

func TestTerminalGovernanceWindowRepairMirrorsCompletedDurableRun(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260428,20260429"
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-3 * time.Hour),
		Priority:     3,
		Status:       GovernanceWindowStatusTerminalFailed,
		LastError:    "governance window lease expired",
		LeaseOwner:   "old-dispatcher",
		LeaseUntil:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	runEndedAt := now.Add(-time.Minute)
	if err := store.AddRun(&GovernanceRunRecord{
		RunID:        "run-close-passed",
		JobName:      string(GovernanceJobDailyCloseSync),
		Status:       GovernanceRunStatusPassed,
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      runEndedAt,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
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
		TerminalGovernanceWindowRepair{Now: fixedRepairNow},
	})
	if err != nil {
		t.Fatalf("apply terminal window repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed {
		t.Fatalf("window = %+v, want passed", window)
	}
	if window.RunID != "run-close-passed" || !window.EndedAt.Equal(runEndedAt) || window.LastError != "" {
		t.Fatalf("window did not mirror completed run: %+v", window)
	}
}

func TestTerminalGovernanceWindowRepairDoesNotOverwriteConcurrentCompletion(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	now := fixedRepairNow()
	targetWindow := "20260428,20260429"
	windowKey := GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	if err := store.UpsertWindow(&GovernanceWindowRecord{
		WindowKey:    windowKey,
		JobName:      string(GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-3 * time.Hour),
		Priority:     3,
		Status:       GovernanceWindowStatusTerminalFailed,
		LastError:    "governance window lease expired",
	}); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	planned, err := (TerminalGovernanceWindowRepair{Now: fixedRepairNow}).Plan(store)
	if err != nil {
		t.Fatalf("plan terminal window repair: %v", err)
	}
	if len(planned) != 1 || planned[0].Action != "requeue_window" {
		t.Fatalf("unexpected plan: %+v", planned)
	}
	window, err := store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("get window: %v", err)
	}
	window.Status = GovernanceWindowStatusPassed
	window.LastError = ""
	window.RunID = "run-concurrent-pass"
	if err := store.UpdateWindow(window); err != nil {
		t.Fatalf("mark window concurrently passed: %v", err)
	}

	applied, err := (TerminalGovernanceWindowRepair{Now: fixedRepairNow}).Apply(store)
	if err != nil {
		t.Fatalf("apply terminal window repair: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("repair overwrote concurrent completion: %+v", applied)
	}
	window, err = store.GetWindowByKey(windowKey)
	if err != nil {
		t.Fatalf("reload window: %v", err)
	}
	if window == nil || window.Status != GovernanceWindowStatusPassed || window.RunID != "run-concurrent-pass" {
		t.Fatalf("concurrent completion was overwritten: %+v", window)
	}
}

func TestDegradedProviderBacklogRepairRequeuesRetryableTasksWithinBudget(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	for _, task := range []GovernanceTaskRecord{
		{
			TaskKey:      "daily_close_sync:live_capture:20260429:sh512143",
			JobName:      string(GovernanceJobDailyCloseSync),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     2,
			Attempts:     0,
			Reason:       "超时",
			TargetWindow: "20260429",
			PayloadJSON:  `{"domain":"live_capture","date":"20260429","instrument":"sh512143","reason":"超时"}`,
		},
		{
			TaskKey:      "daily_audit:order_history:20260420",
			JobName:      string(GovernanceJobDailyAudit),
			Domain:       "order_history",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     3,
			Attempts:     1,
			Reason:       "sz300344: 超时",
			TargetWindow: "20260420",
		},
		{
			TaskKey:      "daily_close_sync:live_capture:20260428:sh512143",
			JobName:      string(GovernanceJobDailyCloseSync),
			Domain:       "live_capture",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     2,
			Attempts:     2,
			Reason:       "超时",
			TargetWindow: "20260428",
		},
		{
			TaskKey:      "daily_audit:f10:20260420",
			JobName:      string(GovernanceJobDailyAudit),
			Domain:       "f10",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     3,
			Reason:       "schema mismatch",
			TargetWindow: "20260420",
		},
		{
			TaskKey:      "daily_audit:quote_snapshot:20260420",
			JobName:      string(GovernanceJobDailyAudit),
			Domain:       "quote_snapshot",
			Status:       GovernanceTaskStatusDegraded,
			Priority:     3,
			Reason:       "historical snapshot unavailable",
			TargetWindow: "20260420",
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
		DegradedProviderBacklogRepair{MaxAttempts: 2},
	})
	if err != nil {
		t.Fatalf("dry-run degraded provider backlog repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 4 || result.Operations[0].Applied != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result.Operations)
	}
	actions := make(map[string]string, len(result.Operations[0].Changes))
	for _, change := range result.Operations[0].Changes {
		actions[change.Target] = change.Action
	}
	if actions["daily_close_sync:live_capture:20260429:sh512143"] != "requeue_provider_task" ||
		actions["daily_audit:order_history:20260420"] != "requeue_provider_task" ||
		actions["daily_close_sync:live_capture:20260428:sh512143"] != "classify_retry_exhausted" ||
		actions["daily_audit:f10:20260420"] != "classify_non_retryable" {
		t.Fatalf("unexpected actions: %+v", actions)
	}
	if _, ok := actions["daily_audit:quote_snapshot:20260420"]; ok {
		t.Fatalf("non-provider quote snapshot task should not be planned: %+v", actions)
	}

	result, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(paths.BaseDataDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		DegradedProviderBacklogRepair{MaxAttempts: 2},
	})
	if err != nil {
		t.Fatalf("apply degraded provider backlog repair: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 4 || result.Operations[0].Applied != 4 {
		t.Fatalf("unexpected apply result: %+v", result.Operations)
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
	byKey := make(map[string]GovernanceTaskRecord, len(tasks))
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	for _, key := range []string{
		"daily_close_sync:live_capture:20260429:sh512143",
		"daily_audit:order_history:20260420",
	} {
		if byKey[key].Status != GovernanceTaskStatusOpen {
			t.Fatalf("task %s status = %s, want open", key, byKey[key].Status)
		}
	}
	if byKey["daily_close_sync:live_capture:20260428:sh512143"].Status != GovernanceTaskStatusDegraded ||
		!strings.HasPrefix(byKey["daily_close_sync:live_capture:20260428:sh512143"].Reason, "provider repair exhausted:") {
		t.Fatalf("exhausted task not classified: %+v", byKey["daily_close_sync:live_capture:20260428:sh512143"])
	}
	if byKey["daily_audit:f10:20260420"].Status != GovernanceTaskStatusDegraded ||
		!strings.HasPrefix(byKey["daily_audit:f10:20260420"].Reason, "provider repair not retryable:") {
		t.Fatalf("non-retryable task not classified: %+v", byKey["daily_audit:f10:20260420"])
	}
	if byKey["daily_audit:quote_snapshot:20260420"].Reason != "historical snapshot unavailable" {
		t.Fatalf("non-provider task was changed: %+v", byKey["daily_audit:quote_snapshot:20260420"])
	}
}

func fixedRepairNow() time.Time {
	return time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)
}

func seedCoveredGovernanceSnapshots(t *testing.T, store *GovernanceStore, now time.Time) {
	t.Helper()
	for _, snapshot := range []DomainHealthSnapshotRecord{
		{Domain: "trade_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "live_capture", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "order_history", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260430", SnapshotAt: now},
		{Domain: "finance", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "20260429", SnapshotAt: now},
		{Domain: "f10", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "hash", SnapshotAt: now},
		{Domain: "kline", Status: "healthy", Freshness: "fresh", Coverage: "covered", LatestWatermark: "1777532400", SnapshotAt: now},
	} {
		snapshot := snapshot
		if err := store.UpsertDomainHealthSnapshot(&snapshot); err != nil {
			t.Fatalf("seed snapshot %s: %v", snapshot.Domain, err)
		}
	}
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
