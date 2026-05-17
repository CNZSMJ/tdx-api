package governance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/market/billboard"
)

type MarketBillboardSyncConfig struct {
	Store              *collectorpkg.GovernanceStore
	Paths              collectorpkg.GovernancePaths
	Now                func() time.Time
	Hostname           string
	UseExternalLock    bool
	ResolveTargetDates func(context.Context, time.Time, int) ([]string, error)
	Execute            func(context.Context, []string) (billboard.SyncResult, error)
}

type MarketBillboardSyncRunner struct {
	cfg MarketBillboardSyncConfig
}

func NewMarketBillboardSyncRunner(cfg MarketBillboardSyncConfig) (*MarketBillboardSyncRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("market billboard sync requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("market billboard sync requires governance paths")
	}
	if cfg.ResolveTargetDates == nil || cfg.Execute == nil {
		return nil, fmt.Errorf("market billboard sync requires target-date resolver and executor")
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
	return &MarketBillboardSyncRunner{cfg: cfg}, nil
}

func (r *MarketBillboardSyncRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, nil)
}

func (r *MarketBillboardSyncRunner) RunWithDates(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, append([]string(nil), targetDates...))
}

func (r *MarketBillboardSyncRunner) run(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	var lock *collectorpkg.GovernanceLock
	if !r.cfg.UseExternalLock {
		acquired, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
		if err != nil {
			return nil, err
		}
		lock = acquired
		defer lock.Release()
	}

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("market-billboard-sync-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobMarketBillboardSync),
		Trigger:      trigger,
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: startedAt.Format("20060102"),
		StartedAt:    startedAt,
	}
	if err := r.cfg.Store.AddRun(run); err != nil {
		return nil, err
	}
	if !r.cfg.UseExternalLock {
		if err := r.cfg.Store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
			LockName:        "system_governance",
			HolderPID:       int64(os.Getpid()),
			HolderHostname:  r.cfg.Hostname,
			HolderJobName:   string(collectorpkg.GovernanceJobMarketBillboardSync),
			HolderRunID:     run.RunID,
			AcquiredAt:      startedAt,
			LastHeartbeatAt: startedAt,
		}); err != nil {
			return nil, err
		}
		defer r.cfg.Store.DeleteLockMetadata("system_governance")
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
			_ = r.cfg.Store.UpsertDomainHealthSnapshot(&collectorpkg.DomainHealthSnapshotRecord{
				Domain:          billboard.DomainMarketBillboard,
				Status:          "degraded",
				Freshness:       "stale",
				Coverage:        "failed",
				LatestWatermark: "",
				Summary:         resultErr.Error(),
				SnapshotAt:      run.EndedAt,
			})
		}
		_ = r.cfg.Store.UpdateRun(run)
	}()

	if len(targetDates) == 0 {
		dates, err := r.cfg.ResolveTargetDates(ctx, startedAt, 5)
		if err != nil {
			resultErr = err
			return run, err
		}
		targetDates = dates
	}
	if len(targetDates) == 0 {
		run.Status = collectorpkg.GovernanceRunStatusSkipped
		run.Reason = "no_recent_trading_dates"
		return run, nil
	}
	run.TargetWindow = strings.Join(targetDates, ",")
	run.Details = fmt.Sprintf("target_window=%s phase=execute domain=%s", run.TargetWindow, billboard.DomainMarketBillboard)
	if err := r.cfg.Store.UpdateRun(run); err != nil {
		resultErr = err
		return run, err
	}

	result, err := r.cfg.Execute(ctx, targetDates)
	if err != nil {
		resultErr = err
		return run, err
	}
	run.Status = collectorpkg.GovernanceRunStatusPassed
	run.Details = marketBillboardDetails(result)
	if err := r.cfg.Store.UpsertDomainHealthSnapshot(&collectorpkg.DomainHealthSnapshotRecord{
		Domain:          billboard.DomainMarketBillboard,
		Status:          "healthy",
		Freshness:       "fresh",
		Coverage:        "covered",
		LatestCursor:    result.EndDate,
		LatestWatermark: result.EndDate,
		Summary:         run.Details,
		SnapshotAt:      r.cfg.Now(),
	}); err != nil {
		resultErr = err
		return run, err
	}
	return run, nil
}

func marketBillboardDetails(result billboard.SyncResult) string {
	return strings.Join([]string{
		fmt.Sprintf("target_window=%s,%s", result.StartDate, result.EndDate),
		fmt.Sprintf("entries=%d", result.EntryCount),
		fmt.Sprintf("seat_trades=%d", result.SeatTradeCount),
		fmt.Sprintf("institutions=%d", result.InstitutionCount),
		fmt.Sprintf("instrument_stats=%d", result.InstrumentStatCount),
		fmt.Sprintf("raw_rows=%d", result.RawRowCount),
		fmt.Sprintf("filtered=%d", result.FilteredCount),
	}, " ")
}
