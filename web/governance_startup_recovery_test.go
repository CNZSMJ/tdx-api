package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestCollectStartupRecoverySnapshotRecognizesCompletedCloseAndAuditWindows(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.Local)
	for _, run := range []collectorpkg.GovernanceRunRecord{
		{
			RunID:        "close-sync-friday",
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			Status:       collectorpkg.GovernanceRunStatusPassed,
			TargetWindow: "20260416,20260417",
			StartedAt:    now.Add(-14 * time.Hour),
			EndedAt:      now.Add(-13 * time.Hour),
		},
		{
			RunID:        "daily-audit-friday",
			JobName:      string(collectorpkg.GovernanceJobDailyAudit),
			Status:       collectorpkg.GovernanceRunStatusPassed,
			TargetWindow: "20260416,20260417",
			StartedAt:    now.Add(-13 * time.Hour),
			EndedAt:      now.Add(-12 * time.Hour),
		},
	} {
		run := run
		if err := store.AddRun(&run); err != nil {
			t.Fatalf("seed governance run %s: %v", run.RunID, err)
		}
	}

	snapshot, err := collectStartupRecoverySnapshot(
		context.Background(),
		now,
		store,
		func(day time.Time) (bool, error) {
			switch day.Format("20060102") {
			case "20260416", "20260417", "20260420":
				return true, nil
			default:
				return false, nil
			}
		},
		func(ctx context.Context, anchor time.Time, limit int) ([]string, error) {
			switch anchor.Format("20060102") {
			case "20260417":
				return []string{"20260416", "20260417"}, nil
			case "20260420":
				return []string{"20260417", "20260420"}, nil
			default:
				t.Fatalf("unexpected anchor for target-date resolution: %s", anchor.Format(time.RFC3339))
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect startup recovery snapshot: %v", err)
	}
	if len(snapshot.MissedJobs) != 1 {
		t.Fatalf("missed jobs = %+v, want only daily_open_refresh", snapshot.MissedJobs)
	}
	if snapshot.MissedJobs[0].Job != collectorpkg.GovernanceJobDailyOpenRefresh || snapshot.MissedJobs[0].TargetWindow != "20260420" {
		t.Fatalf("unexpected missed jobs: %+v", snapshot.MissedJobs)
	}
}

func TestCollectStartupRecoverySnapshotQueuesPreviousTradingWindowsBeforeEveningCutoff(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.Local)
	snapshot, err := collectStartupRecoverySnapshot(
		context.Background(),
		now,
		store,
		func(day time.Time) (bool, error) {
			switch day.Format("20060102") {
			case "20260416", "20260417", "20260420":
				return true, nil
			default:
				return false, nil
			}
		},
		func(ctx context.Context, anchor time.Time, limit int) ([]string, error) {
			switch anchor.Format("20060102") {
			case "20260417":
				return []string{"20260416", "20260417"}, nil
			case "20260420":
				return []string{"20260417", "20260420"}, nil
			default:
				t.Fatalf("unexpected anchor for target-date resolution: %s", anchor.Format(time.RFC3339))
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect startup recovery snapshot: %v", err)
	}

	missedByJob := make(map[collectorpkg.GovernanceJob]string, len(snapshot.MissedJobs))
	for _, missed := range snapshot.MissedJobs {
		missedByJob[missed.Job] = missed.TargetWindow
	}
	if missedByJob[collectorpkg.GovernanceJobDailyOpenRefresh] != "20260420" {
		t.Fatalf("open refresh target window = %q, want 20260420", missedByJob[collectorpkg.GovernanceJobDailyOpenRefresh])
	}
	if missedByJob[collectorpkg.GovernanceJobDailyCloseSync] != "20260416,20260417" {
		t.Fatalf("close sync target window = %q, want 20260416,20260417", missedByJob[collectorpkg.GovernanceJobDailyCloseSync])
	}
	if missedByJob[collectorpkg.GovernanceJobDailyAudit] != "20260416,20260417" {
		t.Fatalf("daily audit target window = %q, want 20260416,20260417", missedByJob[collectorpkg.GovernanceJobDailyAudit])
	}
}

func TestCollectStartupRecoverySnapshotSkipsTerminalInterruptedRunTasks(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 21, 8, 0, 0, 0, time.Local)
	run := collectorpkg.GovernanceRunRecord{
		RunID:        "daily-close-sync-interrupted",
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusInterrupted,
		TargetWindow: "20260420",
		StartedAt:    now.Add(-time.Hour),
		EndedAt:      now.Add(-30 * time.Minute),
	}
	if err := store.AddRun(&run); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	task := collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:interrupted:daily-close-sync-interrupted",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       "interrupted_run",
		Status:       collectorpkg.GovernanceTaskStatusClosed,
		Priority:     1,
		Reason:       "data health snapshots cover target 20260420",
		TargetWindow: "20260502",
	}
	if err := store.UpsertTask(&task); err != nil {
		t.Fatalf("seed closed recovery task: %v", err)
	}
	unsupportedQuoteSnapshot := collectorpkg.GovernanceTaskRecord{
		TaskKey:      "daily_audit:quote_snapshot:20260420",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "quote_snapshot",
		Status:       collectorpkg.GovernanceTaskStatusUnsupported,
		Priority:     3,
		Reason:       "historical intraday quote snapshots cannot be rebuilt",
		TargetWindow: "20260420",
	}
	if err := store.UpsertTask(&unsupportedQuoteSnapshot); err != nil {
		t.Fatalf("seed accepted unsupported task: %v", err)
	}

	snapshot, err := collectStartupRecoverySnapshot(
		context.Background(),
		now,
		store,
		func(day time.Time) (bool, error) { return false, nil },
		func(ctx context.Context, anchor time.Time, limit int) ([]string, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("collect startup recovery snapshot: %v", err)
	}
	if len(snapshot.InterruptedRuns) != 0 {
		t.Fatalf("interrupted runs = %+v, want none", snapshot.InterruptedRuns)
	}
	if snapshot.OpenBacklogCount != 0 {
		t.Fatalf("open backlog count = %d, want 0", snapshot.OpenBacklogCount)
	}
}

func TestRecoverInterruptedGovernanceRunsConvergesCoveredStartupState(t *testing.T) {
	originalStore := governanceStore
	originalPaths := governancePaths
	defer func() {
		governanceStore = originalStore
		governancePaths = originalPaths
	}()

	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store
	governancePaths = paths

	now := time.Date(2026, 4, 22, 9, 0, 0, 0, time.Local)
	targetWindow := "20260420,20260421"
	closeRunID := "close-sync-active"
	closeKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, targetWindow)
	auditKey := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, targetWindow)
	if err := store.AddRun(&collectorpkg.GovernanceRunRecord{
		RunID:        closeRunID,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: targetWindow,
		StartedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed running close sync run: %v", err)
	}
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:    closeKey,
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: targetWindow,
		DueAt:        now.Add(-3 * time.Hour),
		Priority:     collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyCloseSync),
		Status:       collectorpkg.GovernanceWindowStatusRunning,
		LeaseOwner:   "previous-process",
		LeaseUntil:   now.Add(-time.Hour),
		RunID:        closeRunID,
		StartedAt:    now.Add(-2 * time.Hour),
		ScheduledAt:  now.Add(-3 * time.Hour),
		EnqueuedAt:   now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatalf("seed running close sync window: %v", err)
	}
	if err := store.UpsertWindow(&collectorpkg.GovernanceWindowRecord{
		WindowKey:     auditKey,
		JobName:       string(collectorpkg.GovernanceJobDailyAudit),
		TargetWindow:  targetWindow,
		DueAt:         now.Add(-2 * time.Hour),
		Priority:      collectorpkg.GovernanceJobPriority(collectorpkg.GovernanceJobDailyAudit),
		Status:        collectorpkg.GovernanceWindowStatusQueued,
		DependencyKey: closeKey,
		ScheduledAt:   now.Add(-2 * time.Hour),
		EnqueuedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed queued audit window: %v", err)
	}
	if err := store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "startup_recovery:interrupted:" + closeRunID,
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       "interrupted_run",
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       closeRunID,
		TargetWindow: targetWindow,
	}); err != nil {
		t.Fatalf("seed interrupted run task: %v", err)
	}
	if err := store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       99999,
		HolderJobName:   string(collectorpkg.GovernanceJobDataLifecycleMaintenance),
		HolderRunID:     "stale-lifecycle-run",
		AcquiredAt:      now.Add(-12 * time.Hour),
		LastHeartbeatAt: now.Add(-12 * time.Hour),
	}); err != nil {
		t.Fatalf("seed stale lock metadata: %v", err)
	}
	seedCoveredGovernanceSnapshots(t, store, now)

	if err := recoverInterruptedGovernanceRuns(); err != nil {
		t.Fatalf("recover interrupted governance runs: %v", err)
	}

	run, err := store.GetRunByRunID(closeRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run == nil || run.Status != collectorpkg.GovernanceRunStatusInterrupted {
		t.Fatalf("run = %+v, want interrupted", run)
	}
	closeWindow, err := store.GetWindowByKey(closeKey)
	if err != nil {
		t.Fatalf("get close window: %v", err)
	}
	if closeWindow == nil || closeWindow.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("close window = %+v, want passed", closeWindow)
	}
	auditWindow, err := store.GetWindowByKey(auditKey)
	if err != nil {
		t.Fatalf("get audit window: %v", err)
	}
	if auditWindow == nil || auditWindow.Status != collectorpkg.GovernanceWindowStatusPassed {
		t.Fatalf("audit window = %+v, want passed", auditWindow)
	}
	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, task := range tasks {
		if task.TaskKey == "startup_recovery:interrupted:"+closeRunID && task.Status != collectorpkg.GovernanceTaskStatusClosed {
			t.Fatalf("startup recovery task status = %s, want closed", task.Status)
		}
	}
	lock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("read lock metadata: %v", err)
	}
	if lock != nil {
		t.Fatalf("lock metadata = %+v, want nil", lock)
	}
}

func TestMissedGovernanceWindowCreatesDurableWindowIntent(t *testing.T) {
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

	if err := upsertMissedGovernanceWindowTask(
		collectorpkg.GovernanceJobDailyAudit,
		"20260427,20260428",
		"scheduled daily-19:00 blocked by active governance lock",
	); err != nil {
		t.Fatalf("upsert missed governance window: %v", err)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %d, want 1", len(tasks))
	}

	window, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"))
	if err != nil {
		t.Fatalf("get governance window: %v", err)
	}
	if window == nil {
		t.Fatalf("expected missed audit window intent")
	}
	if window.Status != collectorpkg.GovernanceWindowStatusQueued || window.TargetWindow != "20260427,20260428" {
		t.Fatalf("unexpected window intent: %+v", window)
	}
	wantDependency := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	if window.DependencyKey != wantDependency {
		t.Fatalf("window dependency = %q, want %q", window.DependencyKey, wantDependency)
	}
}

func seedCoveredGovernanceSnapshots(t *testing.T, store *collectorpkg.GovernanceStore, now time.Time) {
	t.Helper()
	for _, snapshot := range []collectorpkg.DomainHealthSnapshotRecord{
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
