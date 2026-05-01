package collector

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
