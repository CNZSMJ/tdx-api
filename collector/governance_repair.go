package collector

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
