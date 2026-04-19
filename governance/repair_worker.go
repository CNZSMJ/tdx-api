package governance

import (
	"context"
	"fmt"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type RepairWorkerConfig struct {
	Store   *collectorpkg.GovernanceStore
	Paths   collectorpkg.GovernancePaths
	Now     func() time.Time
	Execute func(context.Context, collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error)
}

type RepairWorkerRunner struct {
	cfg RepairWorkerConfig
}

func NewRepairWorkerRunner(cfg RepairWorkerConfig) (*RepairWorkerRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("repair worker requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("repair worker requires governance paths")
	}
	if cfg.Execute == nil {
		return nil, fmt.Errorf("repair worker requires task executor")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RepairWorkerRunner{cfg: cfg}, nil
}

func (r *RepairWorkerRunner) Run(ctx context.Context, limit int) ([]collectorpkg.GovernanceTaskRecord, error) {
	updated := make([]collectorpkg.GovernanceTaskRecord, 0, limit)
	for limit <= 0 || len(updated) < limit {
		task, ok, err := r.claimNextOpenTask()
		if err != nil {
			return updated, err
		}
		if !ok {
			return updated, nil
		}

		status, reason, execErr := r.cfg.Execute(ctx, task)
		if execErr != nil {
			task.Status = collectorpkg.GovernanceTaskStatusOpen
			if reason != "" {
				task.Reason = reason
			} else {
				task.Reason = execErr.Error()
			}
			if err := r.persistTask(task); err != nil {
				return updated, err
			}
			return updated, execErr
		}

		task.Status = status
		if reason != "" {
			task.Reason = reason
		}
		if err := r.persistTask(task); err != nil {
			return updated, err
		}
		updated = append(updated, task)
	}
	return updated, nil
}

func (r *RepairWorkerRunner) claimNextOpenTask() (collectorpkg.GovernanceTaskRecord, bool, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return collectorpkg.GovernanceTaskRecord{}, false, err
	}
	defer lock.Release()

	tasks, err := r.cfg.Store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		return collectorpkg.GovernanceTaskRecord{}, false, err
	}
	if len(tasks) == 0 {
		return collectorpkg.GovernanceTaskRecord{}, false, nil
	}

	task := tasks[0]
	task.Status = collectorpkg.GovernanceTaskStatusInProgress
	if err := r.cfg.Store.UpsertTask(&task); err != nil {
		return collectorpkg.GovernanceTaskRecord{}, false, err
	}
	return task, true, nil
}

func (r *RepairWorkerRunner) persistTask(task collectorpkg.GovernanceTaskRecord) error {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return err
	}
	defer lock.Release()
	return r.cfg.Store.UpsertTask(&task)
}
