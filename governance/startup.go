package governance

import (
	"context"
	"fmt"
	"os"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type StartupRecoverySnapshot struct {
	MissedJobs       []StartupRecoveryMissedJob
	InterruptedRuns  []string
	OpenBacklogCount int
}

type StartupRecoveryMissedJob struct {
	Job          collectorpkg.GovernanceJob
	TargetWindow string
}

type StartupRecoveryConfig struct {
	Store   *collectorpkg.GovernanceStore
	Paths   collectorpkg.GovernancePaths
	Now     func() time.Time
	Inspect func(context.Context, time.Time) (StartupRecoverySnapshot, error)
}

type StartupRecoveryRunner struct {
	cfg StartupRecoveryConfig
}

func NewStartupRecoveryRunner(cfg StartupRecoveryConfig) (*StartupRecoveryRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("startup recovery requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("startup recovery requires governance paths")
	}
	if cfg.Inspect == nil {
		return nil, fmt.Errorf("startup recovery requires backlog inspector")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &StartupRecoveryRunner{cfg: cfg}, nil
}

func (r *StartupRecoveryRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("startup-recovery-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Trigger:      trigger,
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: startedAt.Format("20060102"),
		StartedAt:    startedAt,
	}
	if err := r.cfg.Store.AddRun(run); err != nil {
		return nil, err
	}
	if err := r.cfg.Store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderJobName:   string(collectorpkg.GovernanceJobStartupRecovery),
		HolderRunID:     run.RunID,
		AcquiredAt:      startedAt,
		LastHeartbeatAt: startedAt,
	}); err != nil {
		return nil, err
	}

	snapshot, err := r.cfg.Inspect(ctx, startedAt)
	if err != nil {
		run.Status = collectorpkg.GovernanceRunStatusFailed
		run.Reason = err.Error()
		run.EndedAt = r.cfg.Now()
		_ = r.cfg.Store.UpdateRun(run)
		return nil, err
	}

	for _, missed := range snapshot.MissedJobs {
		if err := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
			TaskKey:      fmt.Sprintf("%s:missed:%s:%s", collectorpkg.GovernanceJobStartupRecovery, missed.Job, missed.TargetWindow),
			JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
			Domain:       string(missed.Job),
			Status:       collectorpkg.GovernanceTaskStatusOpen,
			Priority:     1,
			Reason:       "missed governance window queued for recovery",
			TargetWindow: missed.TargetWindow,
		}); err != nil {
			return nil, err
		}
	}
	for _, interrupted := range snapshot.InterruptedRuns {
		if err := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
			TaskKey:      fmt.Sprintf("%s:interrupted:%s", collectorpkg.GovernanceJobStartupRecovery, interrupted),
			JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
			Domain:       "interrupted_run",
			Status:       collectorpkg.GovernanceTaskStatusOpen,
			Priority:     1,
			Reason:       interrupted,
			TargetWindow: run.TargetWindow,
		}); err != nil {
			return nil, err
		}
	}

	run.EndedAt = r.cfg.Now()
	if len(snapshot.MissedJobs) > 0 || len(snapshot.InterruptedRuns) > 0 || snapshot.OpenBacklogCount > 0 {
		run.Status = collectorpkg.GovernanceRunStatusPartial
		run.Details = fmt.Sprintf("missed_jobs=%d interrupted_runs=%d open_backlog=%d", len(snapshot.MissedJobs), len(snapshot.InterruptedRuns), snapshot.OpenBacklogCount)
	} else {
		run.Status = collectorpkg.GovernanceRunStatusPassed
		run.Details = "no governance backlog detected"
	}
	if err := r.cfg.Store.UpdateRun(run); err != nil {
		return nil, err
	}
	return run, nil
}
