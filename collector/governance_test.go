package collector

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResolveGovernancePathsUsesTDXDataDir(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("TDX_DATA_DIR", baseDir)

	paths := ResolveGovernancePaths("")

	if paths.RootDir != filepath.Join(baseDir, "governance") {
		t.Fatalf("root dir = %s, want %s", paths.RootDir, filepath.Join(baseDir, "governance"))
	}
	if paths.DBPath != filepath.Join(baseDir, "governance", "system_governance.db") {
		t.Fatalf("db path = %s, want %s", paths.DBPath, filepath.Join(baseDir, "governance", "system_governance.db"))
	}
	if paths.LockPath != filepath.Join(baseDir, "governance", "system_governance.lock") {
		t.Fatalf("lock path = %s, want %s", paths.LockPath, filepath.Join(baseDir, "governance", "system_governance.lock"))
	}
	if paths.ReportsDir != filepath.Join(baseDir, "governance", "reports") {
		t.Fatalf("reports dir = %s, want %s", paths.ReportsDir, filepath.Join(baseDir, "governance", "reports"))
	}
}

func TestResolveGovernancePathsFallsBackToRepoLocalDataDir(t *testing.T) {
	t.Setenv("TDX_DATA_DIR", "")

	paths := ResolveGovernancePaths("")

	wantRoot := filepath.Join(".", "data", "database", "governance")
	if paths.RootDir != wantRoot {
		t.Fatalf("root dir = %s, want %s", paths.RootDir, wantRoot)
	}
	if paths.DBPath != filepath.Join(wantRoot, "system_governance.db") {
		t.Fatalf("db path = %s, want %s", paths.DBPath, filepath.Join(wantRoot, "system_governance.db"))
	}
}

func TestOpenGovernanceStoreUsesIsolatedSchema(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	if paths.DBPath == DefaultDBPath(paths.BaseDataDir) {
		t.Fatalf("governance db path must not reuse collector db path: %s", paths.DBPath)
	}

	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	for _, bean := range []interface{}{
		new(GovernanceSchemaVersion),
		new(GovernanceRunRecord),
		new(GovernanceTaskRecord),
		new(DomainHealthSnapshotRecord),
		new(GovernanceLockMetadataRecord),
		new(GovernanceEvidenceRecord),
	} {
		ok, err := store.HasTable(bean)
		if err != nil {
			t.Fatalf("check table for %T: %v", bean, err)
		}
		if !ok {
			t.Fatalf("expected governance table for %T", bean)
		}
	}
}

func TestGovernanceStorePersistsControlPlaneRecords(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 19, 9, 0, 0, 0, time.Local)
	run := &GovernanceRunRecord{
		RunID:      "run-1",
		JobName:    string(GovernanceJobDailyOpenRefresh),
		Trigger:    "legacy-09:00",
		Status:     GovernanceRunStatusRunning,
		StartedAt:  now,
		Details:    "legacy_open_refresh",
		EvidenceID: "evidence-1",
	}
	if err := store.AddRun(run); err != nil {
		t.Fatalf("add run: %v", err)
	}
	run.Status = GovernanceRunStatusPassed
	run.EndedAt = now.Add(2 * time.Minute)
	if err := store.UpdateRun(run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	task := &GovernanceTaskRecord{
		TaskKey:      "task-1",
		JobName:      string(GovernanceJobDailyAudit),
		Domain:       "kline",
		Status:       GovernanceTaskStatusOpen,
		Priority:     2,
		Reason:       "coverage gap",
		TargetWindow: "20260418",
	}
	if err := store.UpsertTask(task); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	snapshot := &DomainHealthSnapshotRecord{
		Domain:          "codes",
		Status:          "healthy",
		Freshness:       "fresh",
		Coverage:        "covered",
		LatestCursor:    "20260419",
		LatestWatermark: "20260419",
		Summary:         "codes reference data present",
		SnapshotAt:      now,
	}
	if err := store.UpsertDomainHealthSnapshot(snapshot); err != nil {
		t.Fatalf("upsert domain snapshot: %v", err)
	}

	lock := &GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  "localhost",
		HolderJobName:   string(GovernanceJobDailyOpenRefresh),
		HolderRunID:     run.RunID,
		AcquiredAt:      now,
		LastHeartbeatAt: now.Add(time.Minute),
	}
	if err := store.RecordLockMetadata(lock); err != nil {
		t.Fatalf("record lock metadata: %v", err)
	}

	evidence := &GovernanceEvidenceRecord{
		EvidenceID: "evidence-1",
		RunID:      run.RunID,
		Kind:       "report",
		Path:       filepath.Join(paths.ReportsDir, "daily_open_refresh-20260419.json"),
		Summary:    "open refresh report",
		CreatedAt:  now,
	}
	if err := store.AddEvidence(evidence); err != nil {
		t.Fatalf("add evidence: %v", err)
	}

	runs, err := store.ListRecentRuns(10)
	if err != nil {
		t.Fatalf("list recent runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != GovernanceRunStatusPassed {
		t.Fatalf("unexpected recent runs: %+v", runs)
	}

	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Domain != "kline" {
		t.Fatalf("unexpected open tasks: %+v", tasks)
	}

	snapshots, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		t.Fatalf("list domain snapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Domain != "codes" {
		t.Fatalf("unexpected domain snapshots: %+v", snapshots)
	}

	latestLock, err := store.LatestLockMetadata()
	if err != nil {
		t.Fatalf("latest lock metadata: %v", err)
	}
	if latestLock == nil || latestLock.HolderRunID != run.RunID {
		t.Fatalf("unexpected lock metadata: %+v", latestLock)
	}

	loadedEvidence, err := store.ListEvidenceForRun(run.RunID)
	if err != nil {
		t.Fatalf("list evidence for run: %v", err)
	}
	if len(loadedEvidence) != 1 || loadedEvidence[0].EvidenceID != evidence.EvidenceID {
		t.Fatalf("unexpected evidence rows: %+v", loadedEvidence)
	}
}

func TestGovernanceStoreUpsertTaskPreservesDegradedStatus(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey:      "task-1",
		JobName:      string(GovernanceJobDailyAudit),
		Domain:       "kline",
		Status:       GovernanceTaskStatusDegraded,
		Priority:     3,
		Reason:       "accepted degradation",
		TargetWindow: "20260420",
	}); err != nil {
		t.Fatalf("seed degraded task: %v", err)
	}
	if err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey:      "task-1",
		JobName:      string(GovernanceJobDailyAudit),
		Domain:       "kline",
		Status:       GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "reopened by later audit",
		TargetWindow: "20260420",
	}); err != nil {
		t.Fatalf("re-upsert task: %v", err)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	if tasks[0].Status != GovernanceTaskStatusDegraded {
		t.Fatalf("task status = %s, want degraded", tasks[0].Status)
	}
	if tasks[0].Reason != "accepted degradation" {
		t.Fatalf("task reason = %q, want preserved degraded reason", tasks[0].Reason)
	}
}

func TestGovernanceStoreListsLowerNumericPrioritiesFirst(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	for _, task := range []GovernanceTaskRecord{
		{
			TaskKey:      "task-low-priority",
			JobName:      string(GovernanceJobDeepAuditBackfill),
			Domain:       "kline",
			Status:       GovernanceTaskStatusOpen,
			Priority:     5,
			TargetWindow: "20260420",
		},
		{
			TaskKey:      "task-high-priority",
			JobName:      string(GovernanceJobStartupRecovery),
			Domain:       string(GovernanceJobDailyOpenRefresh),
			Status:       GovernanceTaskStatusOpen,
			Priority:     1,
			TargetWindow: "20260420",
		},
	} {
		task := task
		if err := store.UpsertTask(&task); err != nil {
			t.Fatalf("seed task %s: %v", task.TaskKey, err)
		}
	}

	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	if tasks[0].TaskKey != "task-high-priority" || tasks[1].TaskKey != "task-low-priority" {
		t.Fatalf("unexpected task order: %+v", tasks)
	}
}

func TestGovernanceLockRejectsSecondProcess(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "system_governance.lock")
	cmd := exec.Command(os.Args[0], "-test.run=TestGovernanceLockHelperProcess", "--", lockPath)
	cmd.Env = append(os.Environ(), "GO_WANT_GOVERNANCE_LOCK_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read helper readiness: %v", err)
	}
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("unexpected helper output: %q", line)
	}

	lock, err := AcquireGovernanceLock(lockPath)
	if err == nil {
		lock.Release()
		t.Fatalf("expected second process acquisition to fail")
	}
	if !IsGovernanceLockHeld(err) {
		t.Fatalf("expected lock-held error, got %v", err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()

	lock, err = AcquireGovernanceLock(lockPath)
	if err != nil {
		t.Fatalf("reacquire after helper exit: %v", err)
	}
	defer lock.Release()
}

func TestGovernanceLockHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GOVERNANCE_LOCK_HELPER") != "1" {
		return
	}
	lockPath := os.Args[len(os.Args)-1]
	lock, err := AcquireGovernanceLock(lockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acquire lock: %v\n", err)
		os.Exit(2)
	}
	defer lock.Release()

	fmt.Fprintln(os.Stdout, "ready")
	select {}
}

func TestGovernanceJobCatalogAndLegacyMapping(t *testing.T) {
	jobs := DefaultGovernanceJobCatalog()
	names := make([]GovernanceJob, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Name)
	}

	want := []GovernanceJob{
		GovernanceJobStartupRecovery,
		GovernanceJobDailyOpenRefresh,
		GovernanceJobDailyCloseSync,
		GovernanceJobDailyAudit,
		GovernanceJobDeepAuditBackfill,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("job catalog = %+v, want %+v", names, want)
	}

	cases := map[string]GovernanceJob{
		"collector_startup_catchup":          GovernanceJobStartupRecovery,
		"collector_daily_full_sync":          GovernanceJobDailyCloseSync,
		"collector_daily_reconcile":          GovernanceJobDailyAudit,
		"codes_auto_update":                  GovernanceJobDailyOpenRefresh,
		"workday_auto_update":                GovernanceJobDailyOpenRefresh,
		"block_auto_refresh":                 GovernanceJobDailyOpenRefresh,
		"professional_finance_auto_prefetch": GovernanceJobDailyOpenRefresh,
	}
	for legacy, wantJob := range cases {
		got, ok := MapLegacyGovernanceJob(legacy)
		if !ok {
			t.Fatalf("expected legacy mapping for %s", legacy)
		}
		if got != wantJob {
			t.Fatalf("legacy mapping for %s = %s, want %s", legacy, got, wantJob)
		}
	}
}

func TestRuntimeUnifiedGovernanceStatusProjectsLegacyRunsAndDomains(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 19, 20, 0, 0, 0, time.Local)
	runtime, err := NewRuntime(store, &stubProvider{}, RuntimeConfig{
		Now: func() time.Time { return now },
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	for _, record := range []ScheduleRunRecord{
		{
			ScheduleName: "collector_startup_catchup",
			Status:       "passed",
			StartedAt:    now.Add(-3 * time.Hour),
			EndedAt:      now.Add(-2*time.Hour - 30*time.Minute),
			Details:      "legacy startup run",
		},
		{
			ScheduleName: "collector_daily_full_sync",
			Status:       "passed",
			StartedAt:    now.Add(-2 * time.Hour),
			EndedAt:      now.Add(-90 * time.Minute),
			Details:      "legacy full sync",
		},
		{
			ScheduleName: "collector_daily_reconcile",
			Status:       "partial",
			StartedAt:    now.Add(-time.Hour),
			EndedAt:      now.Add(-30 * time.Minute),
			Details:      "legacy reconcile",
		},
	} {
		record := record
		if err := store.AddScheduleRun(&record); err != nil {
			t.Fatalf("seed schedule run: %v", err)
		}
	}

	for _, cursor := range []CollectCursorRecord{
		{Domain: "codes", AssetType: MetadataAssetType, Instrument: MetadataAllKey, Cursor: "1713517200"},
		{Domain: "workday", AssetType: MetadataAssetType, Instrument: MetadataAllKey, Cursor: "20260418"},
	} {
		cursor := cursor
		if err := store.UpsertCollectCursor(&cursor); err != nil {
			t.Fatalf("seed cursor: %v", err)
		}
	}

	paths := ResolveGovernancePaths(tmp)
	govStore, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer govStore.Close()

	if err := govStore.UpsertTask(&GovernanceTaskRecord{
		TaskKey:      "task-1",
		JobName:      string(GovernanceJobDailyAudit),
		Domain:       "kline",
		Status:       GovernanceTaskStatusOpen,
		Priority:     2,
		Reason:       "coverage gap",
		TargetWindow: "20260418",
	}); err != nil {
		t.Fatalf("seed governance task: %v", err)
	}
	if err := govStore.UpsertDomainHealthSnapshot(&DomainHealthSnapshotRecord{
		Domain:          "professional_finance",
		Status:          "healthy",
		Freshness:       "fresh",
		Coverage:        "covered",
		LatestCursor:    "20260418",
		LatestWatermark: "20260418",
		Summary:         "seeded by open-refresh governance",
		SnapshotAt:      now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed professional_finance snapshot: %v", err)
	}
	if err := govStore.RecordLockMetadata(&GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  "localhost",
		HolderJobName:   string(GovernanceJobDailyAudit),
		HolderRunID:     "run-1",
		AcquiredAt:      now.Add(-15 * time.Minute),
		LastHeartbeatAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed lock metadata: %v", err)
	}

	status, err := runtime.UnifiedGovernanceStatus(govStore, paths)
	if err != nil {
		t.Fatalf("unified governance status: %v", err)
	}

	if status.Paths.DBPath != paths.DBPath {
		t.Fatalf("db path = %s, want %s", status.Paths.DBPath, paths.DBPath)
	}
	if len(status.Jobs) != 5 {
		t.Fatalf("job count = %d, want 5", len(status.Jobs))
	}

	jobMap := make(map[GovernanceJob]GovernanceJobStatus, len(status.Jobs))
	for _, job := range status.Jobs {
		jobMap[job.Name] = job
	}
	if jobMap[GovernanceJobStartupRecovery].LastRun == nil || jobMap[GovernanceJobStartupRecovery].LastRun.Status != GovernanceRunStatusPassed {
		t.Fatalf("unexpected startup job projection: %+v", jobMap[GovernanceJobStartupRecovery])
	}
	if jobMap[GovernanceJobDailyCloseSync].LastRun == nil || jobMap[GovernanceJobDailyCloseSync].LastRun.Status != GovernanceRunStatusPassed {
		t.Fatalf("unexpected daily close job projection: %+v", jobMap[GovernanceJobDailyCloseSync])
	}
	if jobMap[GovernanceJobDailyAudit].LastRun == nil || jobMap[GovernanceJobDailyAudit].LastRun.Status != GovernanceRunStatusPartial {
		t.Fatalf("unexpected daily audit job projection: %+v", jobMap[GovernanceJobDailyAudit])
	}

	domainMap := make(map[string]DomainHealthSnapshotRecord, len(status.Domains))
	for _, domain := range status.Domains {
		domainMap[domain.Domain] = domain
	}
	if domainMap["codes"].LatestCursor != "1713517200" {
		t.Fatalf("codes snapshot = %+v, want latest cursor 1713517200", domainMap["codes"])
	}
	if domainMap["workday"].LatestCursor != "20260418" {
		t.Fatalf("workday snapshot = %+v, want latest cursor 20260418", domainMap["workday"])
	}
	if domainMap["professional_finance"].Status != "healthy" || domainMap["professional_finance"].Summary != "seeded by open-refresh governance" {
		t.Fatalf("professional_finance snapshot was overwritten: %+v", domainMap["professional_finance"])
	}
	if len(status.Tasks) != 1 || status.Tasks[0].TaskKey != "task-1" {
		t.Fatalf("unexpected governance tasks: %+v", status.Tasks)
	}
	if status.Lock == nil || status.Lock.HolderRunID != "run-1" {
		t.Fatalf("unexpected governance lock metadata: %+v", status.Lock)
	}
}

func TestBlockServiceConstructorDoesNotStartAutoRefreshCron(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewBlockService(store, &stubProvider{}, BlockConfig{
		BaseDir: filepath.Join(tmp, "block"),
	})
	if err != nil {
		t.Fatalf("new block service: %v", err)
	}
	defer service.Close()

	if service.task != nil {
		t.Fatalf("expected constructor not to start block auto-refresh cron")
	}
}
