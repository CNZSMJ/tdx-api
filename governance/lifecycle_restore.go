package governance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/collector/lifecycle"
)

type DataLifecycleRestoreRequest struct {
	SegmentID  string
	Mode       string
	TargetPath string
}

type DataLifecycleRestoreConfig struct {
	Store    *collectorpkg.GovernanceStore
	Paths    collectorpkg.GovernancePaths
	Now      func() time.Time
	Hostname string
	Execute  func(context.Context, DataLifecycleRestoreRequest) (lifecycle.RestoreResult, error)
}

type DataLifecycleRestoreRunner struct {
	cfg DataLifecycleRestoreConfig
}

func NewDataLifecycleRestoreRunner(cfg DataLifecycleRestoreConfig) (*DataLifecycleRestoreRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("data lifecycle restore requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("data lifecycle restore requires governance paths")
	}
	if cfg.Execute == nil {
		return nil, fmt.Errorf("data lifecycle restore requires executor")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if strings.TrimSpace(cfg.Hostname) == "" {
		hostname, err := os.Hostname()
		if err == nil {
			cfg.Hostname = hostname
		}
	}
	return &DataLifecycleRestoreRunner{cfg: cfg}, nil
}

func (r *DataLifecycleRestoreRunner) Run(ctx context.Context, trigger string, req DataLifecycleRestoreRequest) (*collectorpkg.GovernanceRunRecord, error) {
	if strings.TrimSpace(req.SegmentID) == "" {
		return nil, fmt.Errorf("restore segment id is required")
	}
	if strings.TrimSpace(req.Mode) == "" {
		return nil, fmt.Errorf("restore mode is required")
	}
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("data-lifecycle-restore-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDataLifecycleRestore),
		Trigger:      trigger,
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: req.SegmentID + ":" + req.Mode,
		StartedAt:    startedAt,
	}
	if err := r.cfg.Store.AddRun(run); err != nil {
		return nil, err
	}
	if err := r.cfg.Store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  r.cfg.Hostname,
		HolderJobName:   string(collectorpkg.GovernanceJobDataLifecycleRestore),
		HolderRunID:     run.RunID,
		AcquiredAt:      startedAt,
		LastHeartbeatAt: startedAt,
	}); err != nil {
		return nil, err
	}

	var resultErr error
	defer func() {
		run.EndedAt = r.cfg.Now()
		if resultErr != nil {
			if interruptedGovernanceError(resultErr) {
				run.Status = collectorpkg.GovernanceRunStatusInterrupted
			} else {
				run.Status = collectorpkg.GovernanceRunStatusFailed
			}
			run.Reason = resultErr.Error()
		}
		_ = r.cfg.Store.UpdateRun(run)
	}()

	result, err := r.cfg.Execute(ctx, req)
	if err != nil {
		resultErr = err
		return nil, err
	}
	run.Status = collectorpkg.GovernanceRunStatusPassed
	run.Details = fmt.Sprintf("segment_id=%s mode=%s table=%s rows_restored=%d target=%s", req.SegmentID, req.Mode, result.TableName, result.RowCount, result.TargetDBPath)
	if err := r.cfg.Store.AddEvidence(&collectorpkg.GovernanceEvidenceRecord{
		EvidenceID: fmt.Sprintf("%s:%s", run.RunID, req.SegmentID),
		RunID:      run.RunID,
		Kind:       "lifecycle_restore",
		Path:       result.TargetDBPath,
		Summary:    fmt.Sprintf("data lifecycle restore rows=%d mode=%s", result.RowCount, req.Mode),
		CreatedAt:  r.cfg.Now(),
	}); err != nil {
		resultErr = err
		return nil, err
	}
	return run, nil
}
