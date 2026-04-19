package governance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type DailyOpenRefreshConfig struct {
	Store             *collectorpkg.GovernanceStore
	Paths             collectorpkg.GovernancePaths
	Now               func() time.Time
	Sleep             func(time.Duration)
	RetryInterval     time.Duration
	Hostname          string
	CalendarGate      func(day time.Time) (bool, error)
	CodesRefresh      func(context.Context) error
	WorkdayRefresh    func(context.Context) error
	BlockRefresh      func(context.Context) error
	ProFinanceRefresh func(context.Context) error
}

type DailyOpenRefreshRunner struct {
	cfg DailyOpenRefreshConfig
}

func NewDailyOpenRefreshRunner(cfg DailyOpenRefreshConfig) (*DailyOpenRefreshRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("daily open refresh requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("daily open refresh requires governance paths")
	}
	if cfg.CalendarGate == nil {
		return nil, fmt.Errorf("daily open refresh requires trading-calendar gate")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 5 * time.Minute
	}
	if strings.TrimSpace(cfg.Hostname) == "" {
		hostname, err := os.Hostname()
		if err == nil {
			cfg.Hostname = hostname
		}
	}
	return &DailyOpenRefreshRunner{cfg: cfg}, nil
}

func (r *DailyOpenRefreshRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	targetWindow := startedAt.Format("20060102")
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("daily-open-refresh-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDailyOpenRefresh),
		Trigger:      trigger,
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: targetWindow,
		StartedAt:    startedAt,
	}
	if err := r.cfg.Store.AddRun(run); err != nil {
		return nil, err
	}

	if err := r.cfg.Store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  r.cfg.Hostname,
		HolderJobName:   string(collectorpkg.GovernanceJobDailyOpenRefresh),
		HolderRunID:     run.RunID,
		AcquiredAt:      startedAt,
		LastHeartbeatAt: startedAt,
	}); err != nil {
		return nil, err
	}

	var resultErr error
	var domainFailures []string
	defer func() {
		run.EndedAt = r.cfg.Now()
		if resultErr != nil {
			run.Status = collectorpkg.GovernanceRunStatusFailed
			run.Reason = resultErr.Error()
		} else if len(domainFailures) > 0 {
			run.Status = collectorpkg.GovernanceRunStatusPartial
			run.Details = strings.Join(domainFailures, "; ")
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
		run.Details = "trading-calendar gate skipped daily_open_refresh"
		run.EndedAt = startedAt
		if err := r.cfg.Store.UpdateRun(run); err != nil {
			return nil, err
		}
		return run, nil
	}

	type domainStage struct {
		name        string
		priority    int
		refresh     func(context.Context) error
		fastRetry   bool
	}
	stages := []domainStage{
		{name: "codes", priority: 1, refresh: r.cfg.CodesRefresh, fastRetry: true},
		{name: "workday", priority: 1, refresh: r.cfg.WorkdayRefresh, fastRetry: true},
		{name: "block", priority: 2, refresh: r.cfg.BlockRefresh},
		{name: "professional_finance", priority: 3, refresh: r.cfg.ProFinanceRefresh},
	}

	for _, stage := range stages {
		if stage.refresh == nil {
			continue
		}
		err := r.runDomainStage(ctx, stage, targetWindow)
		if err == nil {
			continue
		}
		domainFailures = append(domainFailures, fmt.Sprintf("%s=%v", stage.name, err))
		if taskErr := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
			TaskKey:      fmt.Sprintf("%s:%s:%s", collectorpkg.GovernanceJobDailyOpenRefresh, stage.name, targetWindow),
			JobName:      string(collectorpkg.GovernanceJobDailyOpenRefresh),
			Domain:       stage.name,
			Status:       collectorpkg.GovernanceTaskStatusOpen,
			Priority:     stage.priority,
			Reason:       err.Error(),
			TargetWindow: targetWindow,
		}); taskErr != nil {
			resultErr = taskErr
			return nil, taskErr
		}
	}

	if len(domainFailures) == 0 {
		run.Status = collectorpkg.GovernanceRunStatusPassed
		run.Details = "daily_open_refresh completed"
	}
	return run, nil
}

func (r *DailyOpenRefreshRunner) runDomainStage(ctx context.Context, stage struct {
	name      string
	priority  int
	refresh   func(context.Context) error
	fastRetry bool
}, targetWindow string) error {
	err := stage.refresh(ctx)
	if err == nil {
		return r.upsertDomainSnapshot(stage.name, "healthy", "fresh", "covered", targetWindow, "refresh completed")
	}
	if !stage.fastRetry {
		_ = r.upsertDomainSnapshot(stage.name, "degraded", "stale", "unknown", targetWindow, err.Error())
		return err
	}

	cutoff := preOpenCutoff(r.cfg.Now())
	for r.cfg.Now().Before(cutoff) {
		r.cfg.Sleep(r.cfg.RetryInterval)
		err = stage.refresh(ctx)
		if err == nil {
			return r.upsertDomainSnapshot(stage.name, "healthy", "fresh", "covered", targetWindow, "refresh recovered within pre-open retry window")
		}
	}
	_ = r.upsertDomainSnapshot(stage.name, "degraded", "stale", "unknown", targetWindow, err.Error())
	return err
}

func (r *DailyOpenRefreshRunner) upsertDomainSnapshot(domain, status, freshness, coverage, targetWindow, summary string) error {
	return r.cfg.Store.UpsertDomainHealthSnapshot(&collectorpkg.DomainHealthSnapshotRecord{
		Domain:          domain,
		Status:          status,
		Freshness:       freshness,
		Coverage:        coverage,
		LatestCursor:    targetWindow,
		LatestWatermark: targetWindow,
		Summary:         summary,
		SnapshotAt:      r.cfg.Now(),
	})
}

func preOpenCutoff(now time.Time) time.Time {
	year, month, day := now.In(time.Local).Date()
	return time.Date(year, month, day, 9, 30, 0, 0, time.Local)
}
