package collector

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type GovernanceRepairMode string

const (
	GovernanceRepairModeDryRun GovernanceRepairMode = "dry-run"
	GovernanceRepairModeApply  GovernanceRepairMode = "apply"
)

type GovernanceRepairBatchOptions struct {
	DBPath    string
	BackupDir string
	Mode      GovernanceRepairMode
	Now       func() time.Time
}

type GovernanceRepairOperation interface {
	Name() string
	Plan(*GovernanceStore) ([]GovernanceRepairChange, error)
	Apply(*GovernanceStore) ([]GovernanceRepairChange, error)
}

type GovernanceRepairChange struct {
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
}

type GovernanceRepairBatchResult struct {
	Mode       GovernanceRepairMode              `json:"mode"`
	DBPath     string                            `json:"db_path"`
	BackupPath string                            `json:"backup_path,omitempty"`
	Operations []GovernanceRepairOperationResult `json:"operations"`
}

type GovernanceRepairOperationResult struct {
	Name    string                   `json:"name"`
	Planned int                      `json:"planned"`
	Applied int                      `json:"applied"`
	Changes []GovernanceRepairChange `json:"changes,omitempty"`
}

type StaleGovernanceLockMetadataRepair struct {
	LockPath string
}

func (r StaleGovernanceLockMetadataRepair) Name() string {
	return "stale_lock_metadata"
}

func (r StaleGovernanceLockMetadataRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	lock, err := store.LatestLockMetadata()
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, nil
	}
	if err := verifyGovernanceLockReleased(r.LockPath); err != nil {
		return nil, err
	}
	return []GovernanceRepairChange{{
		Operation: r.Name(),
		Target:    lock.LockName,
		Action:    "delete_stale_lock_metadata",
		Reason:    fmt.Sprintf("real governance lock is acquirable; stale holder_run_id=%s", lock.HolderRunID),
	}}, nil
}

func (r StaleGovernanceLockMetadataRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	planned, err := r.Plan(store)
	if err != nil || len(planned) == 0 {
		return planned, err
	}
	affected, err := store.DeleteLockMetadata(planned[0].Target)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, nil
	}
	return planned, nil
}

type StartupRecoveryDeferredBacklogRepair struct{}

func (r StartupRecoveryDeferredBacklogRepair) Name() string {
	return "startup_recovery_deferred_backlog"
}

func (r StartupRecoveryDeferredBacklogRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	tasks, err := legacyStartupRecoveryDeferredTasks(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(tasks))
	for _, task := range tasks {
		changes = append(changes, GovernanceRepairChange{
			Operation: r.Name(),
			Target:    task.TaskKey,
			Action:    "requeue_open_task",
			Reason:    fmt.Sprintf("legacy deferred startup recovery task target_window=%s", task.TargetWindow),
		})
	}
	return changes, nil
}

func (r StartupRecoveryDeferredBacklogRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	tasks, err := legacyStartupRecoveryDeferredTasks(store)
	if err != nil {
		return nil, err
	}
	applied := make([]GovernanceRepairChange, 0, len(tasks))
	for _, task := range tasks {
		change := GovernanceRepairChange{
			Operation: r.Name(),
			Target:    task.TaskKey,
			Action:    "requeue_open_task",
			Reason:    fmt.Sprintf("legacy deferred startup recovery task target_window=%s", task.TargetWindow),
		}
		task.Status = GovernanceTaskStatusOpen
		task.Reason = requeuedStartupRecoveryTaskReason(task)
		if err := store.UpdateTask(&task); err != nil {
			return nil, err
		}
		applied = append(applied, change)
	}
	return applied, nil
}

func legacyStartupRecoveryDeferredTasks(store *GovernanceStore) ([]GovernanceTaskRecord, error) {
	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusDegraded)
	if err != nil {
		return nil, err
	}
	matched := make([]GovernanceTaskRecord, 0, len(tasks))
	for _, task := range tasks {
		if !isLegacyStartupRecoveryDeferredTask(task) {
			continue
		}
		matched = append(matched, task)
	}
	return matched, nil
}

func isLegacyStartupRecoveryDeferredTask(task GovernanceTaskRecord) bool {
	if task.JobName != string(GovernanceJobStartupRecovery) || task.Status != GovernanceTaskStatusDegraded {
		return false
	}
	reason := strings.ToLower(task.Reason)
	return strings.Contains(reason, "replay") && strings.Contains(reason, "deferred")
}

func requeuedStartupRecoveryTaskReason(task GovernanceTaskRecord) string {
	if task.Domain == "interrupted_run" {
		if runID, ok := strings.CutPrefix(task.TaskKey, string(GovernanceJobStartupRecovery)+":interrupted:"); ok {
			return strings.TrimSpace(runID)
		}
	}
	return "legacy startup recovery deferred backlog requeued"
}

type TerminalGovernanceWindowRepair struct {
	Now func() time.Time
}

type terminalGovernanceWindowRepairPlan struct {
	change GovernanceRepairChange
	window GovernanceWindowRecord
	run    *GovernanceRunRecord
}

func (r TerminalGovernanceWindowRepair) Name() string {
	return "terminal_governance_windows"
}

func (r TerminalGovernanceWindowRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, plan.change)
	}
	return changes, nil
}

func (r TerminalGovernanceWindowRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	now := r.now()
	applied := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		if plan.change.Action == "keep_terminal_failed" {
			continue
		}
		window := plan.window
		switch plan.change.Action {
		case "create_missing_dependency_window":
			dependency, ok := missingDependencyWindowForTerminalWindow(window, now)
			if !ok {
				continue
			}
			created, err := store.CreateWindowIfMissing(dependency)
			if err != nil {
				return nil, err
			}
			if created {
				applied = append(applied, plan.change)
			}
			continue
		case "mirror_completed_run":
			if plan.run == nil {
				continue
			}
			window.Status = governanceWindowStatusFromRunStatus(plan.run.Status)
			window.RunID = plan.run.RunID
			window.ResultSummary = fmt.Sprintf("run_id=%s status=%s target=%s", plan.run.RunID, plan.run.Status, plan.run.TargetWindow)
			window.LastError = ""
			if !plan.run.EndedAt.IsZero() {
				window.EndedAt = plan.run.EndedAt
			} else {
				window.EndedAt = now
			}
		case "close_covered_window":
			window.Status = GovernanceWindowStatusPassed
			window.LastError = ""
			window.ResultSummary = plan.change.Reason
			if window.EndedAt.IsZero() {
				window.EndedAt = now
			}
		case "requeue_window":
			window.Status = GovernanceWindowStatusQueued
			window.Attempts = 0
			window.NextRunAt = time.Time{}
			window.EnqueuedAt = now
			window.EndedAt = time.Time{}
			window.LastError = ""
			window.ResultSummary = plan.change.Reason
		default:
			continue
		}
		window.LeaseOwner = ""
		window.LeaseUntil = time.Time{}
		ok, err := store.UpdateWindowIfStatus(&window, GovernanceWindowStatusTerminalFailed)
		if err != nil {
			return nil, err
		}
		if ok {
			applied = append(applied, plan.change)
		}
	}
	return applied, nil
}

func (r TerminalGovernanceWindowRepair) plan(store *GovernanceStore) ([]terminalGovernanceWindowRepairPlan, error) {
	windows, err := store.ListWindowsByStatus(GovernanceWindowStatusTerminalFailed)
	if err != nil {
		return nil, err
	}
	snapshots, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		return nil, err
	}
	coverage := closeSyncSnapshotCoverage(snapshots)
	plans := make([]terminalGovernanceWindowRepairPlan, 0, len(windows))
	for _, window := range windows {
		plan, err := r.planWindow(store, window, coverage)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (r TerminalGovernanceWindowRepair) planWindow(store *GovernanceStore, window GovernanceWindowRecord, coverage closeSyncCoverage) (terminalGovernanceWindowRepairPlan, error) {
	latestRun, err := store.LatestRunForWindow(window.JobName, window.TargetWindow)
	if err != nil {
		return terminalGovernanceWindowRepairPlan{}, err
	}
	change := GovernanceRepairChange{
		Operation: r.Name(),
		Target:    window.WindowKey,
	}
	if latestRun != nil {
		switch governanceWindowStatusFromRunStatus(latestRun.Status) {
		case GovernanceWindowStatusPassed, GovernanceWindowStatusPartial, GovernanceWindowStatusSkipped:
			change.Action = "mirror_completed_run"
			change.Reason = fmt.Sprintf("durable run_id=%s ended with status=%s", latestRun.RunID, latestRun.Status)
			return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
		}
		if latestRun.Status == GovernanceRunStatusRunning {
			change.Action = "keep_terminal_failed"
			change.Reason = fmt.Sprintf("latest durable run_id=%s is still running", latestRun.RunID)
			return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
		}
	}
	if terminalWindowCoveredBySnapshots(window, coverage) {
		target, _ := maxWindowTargetDate(window.TargetWindow)
		change.Action = "close_covered_window"
		change.Reason = fmt.Sprintf("data health snapshots cover target %s", target)
		return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
	}

	ready, reason, err := terminalWindowDependencyReady(store, window)
	if err != nil {
		return terminalGovernanceWindowRepairPlan{}, err
	}
	if !ready {
		if dependency, ok := missingDependencyWindowForTerminalWindow(window, r.now()); ok && strings.Contains(reason, "missing dependency "+dependency.WindowKey) {
			change.Target = dependency.WindowKey
			change.Action = "create_missing_dependency_window"
			change.Reason = fmt.Sprintf("terminal window %s waits on missing dependency", window.WindowKey)
			return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
		}
		change.Action = "keep_terminal_failed"
		change.Reason = reason
		return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
	}
	change.Action = "requeue_window"
	if latestRun != nil {
		change.Reason = fmt.Sprintf("durable run_id=%s ended with status=%s; dependency state allows replay", latestRun.RunID, latestRun.Status)
	} else {
		change.Reason = "no durable completed run; dependency state allows replay"
	}
	return terminalGovernanceWindowRepairPlan{change: change, window: window, run: latestRun}, nil
}

func (r TerminalGovernanceWindowRepair) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func terminalWindowCoveredBySnapshots(window GovernanceWindowRecord, coverage closeSyncCoverage) bool {
	switch GovernanceJob(window.JobName) {
	case GovernanceJobDailyCloseSync, GovernanceJobDailyAudit:
	default:
		return false
	}
	target, ok := maxWindowTargetDate(window.TargetWindow)
	return ok && coverage.covers(target)
}

func missingDependencyWindowForTerminalWindow(window GovernanceWindowRecord, now time.Time) (*GovernanceWindowRecord, bool) {
	var dependencyJob GovernanceJob
	switch GovernanceJob(window.JobName) {
	case GovernanceJobDailyAudit:
		dependencyJob = GovernanceJobDailyCloseSync
	default:
		return nil, false
	}
	canonicalKey := GovernanceWindowKey(dependencyJob, window.TargetWindow)
	dependencyKey := strings.TrimSpace(window.DependencyKey)
	if dependencyKey == "" {
		dependencyKey = GovernanceWindowDependencyKey(GovernanceJob(window.JobName), window.TargetWindow)
	}
	if dependencyKey != canonicalKey {
		return nil, false
	}
	dueAt := window.DueAt
	if dueAt.IsZero() {
		dueAt = now
	}
	return &GovernanceWindowRecord{
		WindowKey:     dependencyKey,
		JobName:       string(dependencyJob),
		TargetWindow:  window.TargetWindow,
		DueAt:         dueAt,
		Priority:      GovernanceJobPriority(dependencyJob),
		Status:        GovernanceWindowStatusQueued,
		ScheduledAt:   now,
		EnqueuedAt:    now,
		ResultSummary: fmt.Sprintf("created because terminal window %s was missing its dependency", window.WindowKey),
	}, true
}

func terminalWindowDependencyReady(store *GovernanceStore, window GovernanceWindowRecord) (bool, string, error) {
	dependencyKey := strings.TrimSpace(window.DependencyKey)
	if dependencyKey == "" {
		dependencyKey = GovernanceWindowDependencyKey(GovernanceJob(window.JobName), window.TargetWindow)
	}
	if dependencyKey == "" {
		return true, "", nil
	}
	dependency, err := store.GetWindowByKey(dependencyKey)
	if err != nil {
		return false, "", err
	}
	if dependency == nil {
		return false, "missing dependency " + dependencyKey, nil
	}
	switch dependency.Status {
	case GovernanceWindowStatusPassed, GovernanceWindowStatusPartial, GovernanceWindowStatusSkipped:
		return true, "", nil
	default:
		return false, fmt.Sprintf("dependency %s is %s", dependencyKey, dependency.Status), nil
	}
}

type CoveredCloseSyncWindowRepair struct {
	Now func() time.Time
}

type CoveredAuditWindowRepair struct {
	Now func() time.Time
}

type CoveredBacklogRepair struct{}

type coveredCloseSyncWindowPlan struct {
	change GovernanceRepairChange
	window GovernanceWindowRecord
}

func (r CoveredCloseSyncWindowRepair) Name() string {
	return "covered_close_sync_windows"
}

func (r CoveredCloseSyncWindowRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, plan.change)
	}
	return changes, nil
}

func (r CoveredCloseSyncWindowRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	now := r.now()
	applied := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		window := plan.window
		expected := window.Status
		window.Status = GovernanceWindowStatusPassed
		window.LastError = ""
		window.ResultSummary = plan.change.Reason
		window.LeaseOwner = ""
		window.LeaseUntil = time.Time{}
		window.NextRunAt = time.Time{}
		if window.EndedAt.IsZero() {
			window.EndedAt = now
		}
		ok, err := store.UpdateWindowIfStatus(&window, expected)
		if err != nil {
			return nil, err
		}
		if ok {
			applied = append(applied, plan.change)
		}
	}
	return applied, nil
}

func (r CoveredCloseSyncWindowRepair) plan(store *GovernanceStore) ([]coveredCloseSyncWindowPlan, error) {
	snapshots, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		return nil, err
	}
	coverage := closeSyncSnapshotCoverage(snapshots)
	windows, err := store.ListWindowsByStatus(GovernanceWindowStatusQueued, GovernanceWindowStatusTerminalFailed, GovernanceWindowStatusRunning, GovernanceWindowStatusWaitingDependency)
	if err != nil {
		return nil, err
	}
	plans := make([]coveredCloseSyncWindowPlan, 0, len(windows))
	for _, window := range windows {
		if GovernanceJob(window.JobName) != GovernanceJobDailyCloseSync {
			continue
		}
		if window.Status == GovernanceWindowStatusRunning {
			run, err := store.LatestRunForWindow(window.JobName, window.TargetWindow)
			if err != nil {
				return nil, err
			}
			if run == nil || run.Status == GovernanceRunStatusRunning {
				continue
			}
		}
		target, ok := maxWindowTargetDate(window.TargetWindow)
		if !ok || !coverage.covers(target) {
			continue
		}
		change := GovernanceRepairChange{
			Operation: r.Name(),
			Target:    window.WindowKey,
			Action:    "close_covered_window",
			Reason:    fmt.Sprintf("data health snapshots cover target %s", target),
		}
		plans = append(plans, coveredCloseSyncWindowPlan{change: change, window: window})
	}
	return plans, nil
}

func (r CoveredCloseSyncWindowRepair) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r CoveredAuditWindowRepair) Name() string {
	return "covered_audit_windows"
}

func (r CoveredAuditWindowRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, plan.change)
	}
	return changes, nil
}

func (r CoveredAuditWindowRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	now := r.now()
	applied := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		window := plan.window
		expected := window.Status
		window.Status = GovernanceWindowStatusPassed
		window.LastError = ""
		window.ResultSummary = plan.change.Reason
		window.LeaseOwner = ""
		window.LeaseUntil = time.Time{}
		window.NextRunAt = time.Time{}
		if window.EndedAt.IsZero() {
			window.EndedAt = now
		}
		ok, err := store.UpdateWindowIfStatus(&window, expected)
		if err != nil {
			return nil, err
		}
		if ok {
			applied = append(applied, plan.change)
		}
	}
	return applied, nil
}

func (r CoveredAuditWindowRepair) plan(store *GovernanceStore) ([]coveredCloseSyncWindowPlan, error) {
	snapshots, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		return nil, err
	}
	coverage := closeSyncSnapshotCoverage(snapshots)
	windows, err := store.ListWindowsByStatus(GovernanceWindowStatusQueued, GovernanceWindowStatusTerminalFailed, GovernanceWindowStatusRunning, GovernanceWindowStatusWaitingDependency)
	if err != nil {
		return nil, err
	}
	plans := make([]coveredCloseSyncWindowPlan, 0, len(windows))
	for _, window := range windows {
		if GovernanceJob(window.JobName) != GovernanceJobDailyAudit {
			continue
		}
		if window.Status == GovernanceWindowStatusRunning {
			run, err := store.LatestRunForWindow(window.JobName, window.TargetWindow)
			if err != nil {
				return nil, err
			}
			if run == nil || run.Status == GovernanceRunStatusRunning {
				continue
			}
		}
		target, ok := maxWindowTargetDate(window.TargetWindow)
		if !ok || !coverage.covers(target) {
			continue
		}
		change := GovernanceRepairChange{
			Operation: r.Name(),
			Target:    window.WindowKey,
			Action:    "close_covered_window",
			Reason:    fmt.Sprintf("data health snapshots cover target %s", target),
		}
		plans = append(plans, coveredCloseSyncWindowPlan{change: change, window: window})
	}
	return plans, nil
}

func (r CoveredAuditWindowRepair) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

type coveredBacklogPlan struct {
	change GovernanceRepairChange
	task   GovernanceTaskRecord
}

func (r CoveredBacklogRepair) Name() string {
	return "covered_backlog"
}

func (r CoveredBacklogRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, plan.change)
	}
	return changes, nil
}

func (r CoveredBacklogRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	applied := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		task := plan.task
		expected := task.Status
		task.Status = GovernanceTaskStatusClosed
		task.Reason = plan.change.Reason
		ok, err := store.UpdateTaskIfStatus(&task, expected)
		if err != nil {
			return nil, err
		}
		if ok {
			applied = append(applied, plan.change)
		}
	}
	return applied, nil
}

func (r CoveredBacklogRepair) plan(store *GovernanceStore) ([]coveredBacklogPlan, error) {
	snapshots, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		return nil, err
	}
	coverage := closeSyncSnapshotCoverage(snapshots)
	domainCoverage := domainSnapshotCoverage(snapshots)
	tasks, err := store.ListTasksByStatus(
		GovernanceTaskStatusOpen,
		GovernanceTaskStatusInProgress,
		GovernanceTaskStatusBlocked,
		GovernanceTaskStatusDegraded,
		GovernanceTaskStatusUnsupported,
	)
	if err != nil {
		return nil, err
	}
	plans := make([]coveredBacklogPlan, 0, len(tasks))
	for _, task := range tasks {
		target, ok, err := coveredBacklogTaskTarget(store, task)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if isProviderBacklogTask(task) {
			if !domainCoverage.covers(task.Domain, target) {
				continue
			}
		} else if isMarketBillboardStartupRecoveryTask(task) {
			if !domainCoverage.covers("market_billboard", target) {
				continue
			}
		} else if !coverage.covers(target) {
			continue
		}
		change := GovernanceRepairChange{
			Operation: r.Name(),
			Target:    task.TaskKey,
			Action:    "close_covered_task",
			Reason:    fmt.Sprintf("data health snapshots cover target %s", target),
		}
		plans = append(plans, coveredBacklogPlan{change: change, task: task})
	}
	return plans, nil
}

func coveredBacklogTaskTarget(store *GovernanceStore, task GovernanceTaskRecord) (string, bool, error) {
	switch GovernanceJob(task.JobName) {
	case GovernanceJobDailyCloseSync, GovernanceJobDailyAudit:
		if !isProviderBacklogTask(task) {
			return "", false, nil
		}
		target, ok := maxWindowTargetDate(task.TargetWindow)
		return target, ok, nil
	case GovernanceJobStartupRecovery:
		switch task.Domain {
		case string(GovernanceJobDailyCloseSync), string(GovernanceJobDailyAudit), string(GovernanceJobMarketBillboardSync):
			target, ok := maxWindowTargetDate(task.TargetWindow)
			return target, ok, nil
		case "interrupted_run":
			return coveredInterruptedRunTaskTarget(store, task)
		default:
			return "", false, nil
		}
	default:
		return "", false, nil
	}
}

func coveredInterruptedRunTaskTarget(store *GovernanceStore, task GovernanceTaskRecord) (string, bool, error) {
	runID := strings.TrimSpace(task.Reason)
	if runID == "" {
		runID = strings.TrimPrefix(task.TaskKey, string(GovernanceJobStartupRecovery)+":interrupted:")
	}
	if runID != "" {
		run, err := store.GetRunByRunID(runID)
		if err != nil {
			return "", false, err
		}
		if run != nil && coveredBacklogRunJobSupported(GovernanceJob(run.JobName)) {
			target, ok := maxWindowTargetDate(run.TargetWindow)
			return target, ok, nil
		}
	}
	return coveredWindowTargetFromText(task.Reason)
}

func coveredBacklogRunJobSupported(job GovernanceJob) bool {
	switch job {
	case GovernanceJobDailyCloseSync, GovernanceJobDailyAudit, GovernanceJobMarketBillboardSync:
		return true
	default:
		return false
	}
}

type closeSyncCoverage struct {
	healthy    bool
	watermarks map[string]string
}

type domainCoverage map[string]DomainHealthSnapshotRecord

func closeSyncSnapshotCoverage(snapshots []DomainHealthSnapshotRecord) closeSyncCoverage {
	required := []string{"trade_history", "live_capture", "order_history", "finance", "f10", "kline"}
	byDomain := make(map[string]DomainHealthSnapshotRecord, len(snapshots))
	for _, snapshot := range snapshots {
		byDomain[snapshot.Domain] = snapshot
	}
	coverage := closeSyncCoverage{healthy: true, watermarks: make(map[string]string, len(required))}
	for _, domain := range required {
		snapshot, ok := byDomain[domain]
		if !ok || snapshot.Status != "healthy" || snapshot.Freshness != "fresh" || snapshot.Coverage != "covered" {
			coverage.healthy = false
			continue
		}
		coverage.watermarks[domain] = strings.TrimSpace(snapshot.LatestWatermark)
	}
	return coverage
}

func domainSnapshotCoverage(snapshots []DomainHealthSnapshotRecord) domainCoverage {
	coverage := make(domainCoverage, len(snapshots))
	for _, snapshot := range snapshots {
		coverage[snapshot.Domain] = snapshot
	}
	return coverage
}

func (c domainCoverage) covers(domain, target string) bool {
	snapshot, ok := c[domain]
	if !ok || snapshot.Status != "healthy" || snapshot.Freshness != "fresh" || snapshot.Coverage != "covered" {
		return false
	}
	switch domain {
	case "trade_history", "live_capture", "order_history", "market_billboard":
		return dateWatermarkCovers(strings.TrimSpace(snapshot.LatestWatermark), target)
	default:
		return true
	}
}

func (c closeSyncCoverage) covers(target string) bool {
	if !c.healthy {
		return false
	}
	for _, domain := range []string{"trade_history", "live_capture", "order_history"} {
		watermark := c.watermarks[domain]
		if !dateWatermarkCovers(watermark, target) {
			return false
		}
	}
	return true
}

var yyyymmddPattern = regexp.MustCompile(`^\d{8}$`)
var governanceWindowKeyPattern = regexp.MustCompile(`\b(daily_close_sync|daily_audit):(\d{8}(?:,\d{8})*)\b`)

func maxWindowTargetDate(targetWindow string) (string, bool) {
	maxDate := ""
	for _, part := range strings.Split(targetWindow, ",") {
		date := strings.TrimSpace(part)
		if !yyyymmddPattern.MatchString(date) {
			return "", false
		}
		if date > maxDate {
			maxDate = date
		}
	}
	return maxDate, maxDate != ""
}

func dateWatermarkCovers(watermark, target string) bool {
	return yyyymmddPattern.MatchString(watermark) && watermark >= target
}

func coveredWindowTargetFromText(text string) (string, bool, error) {
	matches := governanceWindowKeyPattern.FindStringSubmatch(text)
	if len(matches) < 3 {
		return "", false, nil
	}
	target, ok := maxWindowTargetDate(matches[2])
	return target, ok, nil
}

type DegradedProviderBacklogRepair struct {
	MaxAttempts int
}

type degradedProviderBacklogPlan struct {
	change GovernanceRepairChange
	task   GovernanceTaskRecord
}

func (r DegradedProviderBacklogRepair) Name() string {
	return "degraded_provider_backlog"
}

func (r DegradedProviderBacklogRepair) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	changes := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, plan.change)
	}
	return changes, nil
}

func (r DegradedProviderBacklogRepair) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	plans, err := r.plan(store)
	if err != nil {
		return nil, err
	}
	applied := make([]GovernanceRepairChange, 0, len(plans))
	for _, plan := range plans {
		task := plan.task
		switch plan.change.Action {
		case "requeue_provider_task":
			task.Status = GovernanceTaskStatusOpen
			if task.Priority <= 0 || task.Priority > 2 {
				task.Priority = 2
			}
		case "classify_retry_exhausted":
			task.Reason = providerRepairExhaustedReason(task, r.maxAttempts())
		case "classify_non_retryable":
			task.Reason = providerRepairNonRetryableReason(task)
		default:
			continue
		}
		ok, err := store.UpdateTaskIfStatus(&task, GovernanceTaskStatusDegraded)
		if err != nil {
			return nil, err
		}
		if ok {
			applied = append(applied, plan.change)
		}
	}
	return applied, nil
}

func (r DegradedProviderBacklogRepair) plan(store *GovernanceStore) ([]degradedProviderBacklogPlan, error) {
	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusDegraded)
	if err != nil {
		return nil, err
	}
	plans := make([]degradedProviderBacklogPlan, 0, len(tasks))
	for _, task := range tasks {
		if !isProviderBacklogTask(task) {
			continue
		}
		change := GovernanceRepairChange{
			Operation: r.Name(),
			Target:    task.TaskKey,
			Reason:    providerBacklogRepairReason(task, r.maxAttempts()),
		}
		switch {
		case isRetryableProviderBacklogReason(task.Reason) && !providerRepairBudgetExhausted(task, r.maxAttempts()):
			change.Action = "requeue_provider_task"
		case isRetryableProviderBacklogReason(task.Reason):
			change.Action = "classify_retry_exhausted"
		default:
			change.Action = "classify_non_retryable"
		}
		plans = append(plans, degradedProviderBacklogPlan{change: change, task: task})
	}
	return plans, nil
}

func (r DegradedProviderBacklogRepair) maxAttempts() int {
	if r.MaxAttempts > 0 {
		return r.MaxAttempts
	}
	return 2
}

func isProviderBacklogTask(task GovernanceTaskRecord) bool {
	switch GovernanceJob(task.JobName) {
	case GovernanceJobDailyCloseSync, GovernanceJobDailyAudit:
	default:
		return false
	}
	switch task.Domain {
	case "kline", "trade_history", "live_capture", "order_history", "finance", "f10":
		return true
	default:
		return false
	}
}

func isMarketBillboardStartupRecoveryTask(task GovernanceTaskRecord) bool {
	return GovernanceJob(task.JobName) == GovernanceJobStartupRecovery &&
		task.Domain == string(GovernanceJobMarketBillboardSync)
}

func providerRepairBudgetExhausted(task GovernanceTaskRecord, maxAttempts int) bool {
	if strings.HasPrefix(strings.TrimSpace(task.Reason), "provider repair exhausted:") {
		return true
	}
	return task.Attempts >= maxAttempts
}

func isRetryableProviderBacklogReason(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}
	return strings.Contains(text, "timeout") ||
		strings.Contains(text, "超时") ||
		strings.Contains(text, "eof") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "context canceled") ||
		strings.Contains(text, "context cancelled") ||
		strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "connection refused") ||
		strings.Contains(text, "数据长度不足")
}

func providerBacklogRepairReason(task GovernanceTaskRecord, maxAttempts int) string {
	return fmt.Sprintf("job=%s domain=%s date=%s attempts=%d/%d reason=%s", task.JobName, task.Domain, task.TargetWindow, task.Attempts, maxAttempts, strings.TrimSpace(task.Reason))
}

func providerRepairExhaustedReason(task GovernanceTaskRecord, maxAttempts int) string {
	reason := strings.TrimSpace(task.Reason)
	if strings.HasPrefix(reason, "provider repair exhausted:") {
		return reason
	}
	return fmt.Sprintf("provider repair exhausted: retry budget %d/%d; original: %s", task.Attempts, maxAttempts, reason)
}

func providerRepairNonRetryableReason(task GovernanceTaskRecord) string {
	reason := strings.TrimSpace(task.Reason)
	if strings.HasPrefix(reason, "provider repair not retryable:") {
		return reason
	}
	return "provider repair not retryable: original: " + reason
}

func RunGovernanceRepairBatch(opts GovernanceRepairBatchOptions, operations []GovernanceRepairOperation) (*GovernanceRepairBatchResult, error) {
	if opts.DBPath == "" {
		opts.DBPath = ResolveGovernancePaths("").DBPath
	}
	if opts.Mode == "" {
		opts.Mode = GovernanceRepairModeDryRun
	}
	if opts.Mode != GovernanceRepairModeDryRun && opts.Mode != GovernanceRepairModeApply {
		return nil, fmt.Errorf("governance repair mode must be %s or %s", GovernanceRepairModeDryRun, GovernanceRepairModeApply)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := prepareGovernanceRepairDB(opts.DBPath); err != nil {
		return nil, err
	}

	store, err := OpenGovernanceStoreReadOnly(opts.DBPath)
	if err != nil {
		return nil, err
	}
	result := &GovernanceRepairBatchResult{
		Mode:       opts.Mode,
		DBPath:     opts.DBPath,
		Operations: make([]GovernanceRepairOperationResult, 0, len(operations)),
	}
	for _, operation := range operations {
		changes, err := operation.Plan(store)
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("plan %s: %w", operation.Name(), err)
		}
		result.Operations = append(result.Operations, GovernanceRepairOperationResult{
			Name:    operation.Name(),
			Planned: len(changes),
			Changes: changes,
		})
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	if opts.Mode == GovernanceRepairModeDryRun {
		return result, nil
	}

	backupPath, err := backupGovernanceRepairDB(opts.DBPath, opts.BackupDir, opts.Now())
	if err != nil {
		return nil, err
	}
	result.BackupPath = backupPath

	store, err = OpenGovernanceStore(opts.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	for i, operation := range operations {
		changes, err := operation.Apply(store)
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", operation.Name(), err)
		}
		result.Operations[i].Applied = len(changes)
		result.Operations[i].Changes = changes
	}
	return result, nil
}

func prepareGovernanceRepairDB(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	store, err := OpenGovernanceStore(dbPath)
	if err != nil {
		return err
	}
	return store.Close()
}

func verifyGovernanceLockReleased(lockPath string) error {
	lock, err := AcquireGovernanceLock(lockPath)
	if err != nil {
		if IsGovernanceLockHeld(err) {
			return fmt.Errorf("active governance lock: %w", err)
		}
		return err
	}
	return lock.Release()
}

func backupGovernanceRepairDB(dbPath, backupDir string, now time.Time) (string, error) {
	if backupDir == "" {
		backupDir = filepath.Dir(dbPath)
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s.%s.bak", filepath.Base(dbPath), stamp))
	for i := 1; ; i++ {
		if _, err := os.Stat(backupPath); err != nil {
			if os.IsNotExist(err) {
				break
			}
			return "", err
		}
		backupPath = filepath.Join(backupDir, fmt.Sprintf("%s.%s.%d.bak", filepath.Base(dbPath), stamp, i))
	}
	src, err := os.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(backupPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(backupPath)
		return "", closeErr
	}
	return backupPath, nil
}
