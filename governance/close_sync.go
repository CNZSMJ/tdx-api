package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type DailyCloseSyncConfig struct {
	Store              *collectorpkg.GovernanceStore
	Paths              collectorpkg.GovernancePaths
	Now                func() time.Time
	Hostname           string
	CalendarGate       func(day time.Time) (bool, error)
	ResolveTargetDates func(context.Context, time.Time) ([]string, error)
	Execute            func(context.Context, []string) ([]collectorpkg.CloseSyncFailure, error)
}

type DailyCloseSyncRunner struct {
	cfg DailyCloseSyncConfig
}

func NewDailyCloseSyncRunner(cfg DailyCloseSyncConfig) (*DailyCloseSyncRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("daily close sync requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("daily close sync requires governance paths")
	}
	if cfg.CalendarGate == nil || cfg.ResolveTargetDates == nil || cfg.Execute == nil {
		return nil, fmt.Errorf("daily close sync requires gate, target-date resolver, and executor")
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
	return &DailyCloseSyncRunner{cfg: cfg}, nil
}

func (r *DailyCloseSyncRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, nil)
}

func (r *DailyCloseSyncRunner) RunWithDates(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, append([]string(nil), targetDates...))
}

func (r *DailyCloseSyncRunner) run(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("daily-close-sync-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
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
		HolderHostname:  r.cfg.Hostname,
		HolderJobName:   string(collectorpkg.GovernanceJobDailyCloseSync),
		HolderRunID:     run.RunID,
		AcquiredAt:      startedAt,
		LastHeartbeatAt: startedAt,
	}); err != nil {
		return nil, err
	}

	var resultErr error
	var failures []collectorpkg.CloseSyncFailure
	defer func() {
		run.EndedAt = r.cfg.Now()
		if resultErr != nil {
			run.Status = collectorpkg.GovernanceRunStatusFailed
			run.Reason = resultErr.Error()
		} else if len(failures) > 0 {
			run.Status = collectorpkg.GovernanceRunStatusPartial
			run.Details = fmt.Sprintf("repair_tasks=%d", len(failures))
		} else if run.Status == collectorpkg.GovernanceRunStatusRunning {
			run.Status = collectorpkg.GovernanceRunStatusPassed
			run.Details = "daily_close_sync completed"
		}
		_ = r.cfg.Store.UpdateRun(run)
	}()

	tradingDay, err := r.cfg.CalendarGate(startedAt)
	if err != nil {
		resultErr = err
		return nil, err
	}
	if !tradingDay {
		run.Status = collectorpkg.GovernanceRunStatusSkipped
		run.Reason = "non_trading_day_window"
		if err := r.cfg.Store.UpdateRun(run); err != nil {
			return nil, err
		}
		return run, nil
	}

	if len(targetDates) == 0 {
		targetDates, err = r.cfg.ResolveTargetDates(ctx, startedAt)
		if err != nil {
			resultErr = err
			return nil, err
		}
	}
	run.TargetWindow = strings.Join(targetDates, ",")

	failures, err = r.cfg.Execute(ctx, targetDates)
	if err != nil {
		resultErr = err
		return nil, err
	}
	for _, failure := range failures {
		payloadJSON, err := json.Marshal(failure)
		if err != nil {
			resultErr = err
			return nil, err
		}
		if err := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
			TaskKey:      fmt.Sprintf("%s:%s:%s:%s", collectorpkg.GovernanceJobDailyCloseSync, failure.Domain, failure.Date, failure.Instrument),
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			Domain:       failure.Domain,
			Status:       collectorpkg.GovernanceTaskStatusOpen,
			Priority:     2,
			Reason:       failure.Reason,
			TargetWindow: failure.Date,
			PayloadJSON:  string(payloadJSON),
		}); err != nil {
			resultErr = err
			return nil, err
		}
	}

	return run, nil
}
