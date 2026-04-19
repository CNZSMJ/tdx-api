package governance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type AuditDomainResult struct {
	Domain          string
	Status          string
	RepairAttempted bool
	Details         string
	Errors          []string
}

type AuditResult struct {
	Date       string
	Status     string
	ReportPath string
	Domains    []AuditDomainResult
}

type DailyAuditConfig struct {
	Store              *collectorpkg.GovernanceStore
	Paths              collectorpkg.GovernancePaths
	Now                func() time.Time
	Hostname           string
	CalendarGate       func(day time.Time) (bool, error)
	ResolveTargetDates func(context.Context, time.Time) ([]string, error)
	Execute            func(context.Context, string, string) (*AuditResult, error)
}

type DailyAuditRunner struct {
	cfg DailyAuditConfig
}

func NewDailyAuditRunner(cfg DailyAuditConfig) (*DailyAuditRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("daily audit requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("daily audit requires governance paths")
	}
	if cfg.CalendarGate == nil || cfg.ResolveTargetDates == nil || cfg.Execute == nil {
		return nil, fmt.Errorf("daily audit requires gate, target-date resolver, and executor")
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
	return &DailyAuditRunner{cfg: cfg}, nil
}

func (r *DailyAuditRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, nil)
}

func (r *DailyAuditRunner) RunWithDates(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	return r.run(ctx, trigger, append([]string(nil), targetDates...))
}

func (r *DailyAuditRunner) run(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("daily-audit-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
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
		HolderJobName:   string(collectorpkg.GovernanceJobDailyAudit),
		HolderRunID:     run.RunID,
		AcquiredAt:      startedAt,
		LastHeartbeatAt: startedAt,
	}); err != nil {
		return nil, err
	}

	var resultErr error
	var partial bool
	defer func() {
		run.EndedAt = r.cfg.Now()
		switch {
		case resultErr != nil:
			run.Status = collectorpkg.GovernanceRunStatusFailed
			run.Reason = resultErr.Error()
		case partial:
			run.Status = collectorpkg.GovernanceRunStatusPartial
		case run.Status == collectorpkg.GovernanceRunStatusRunning:
			run.Status = collectorpkg.GovernanceRunStatusPassed
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

	for _, date := range targetDates {
		result, err := r.cfg.Execute(ctx, date, trigger)
		if err != nil {
			resultErr = err
			return nil, err
		}
		if result == nil {
			continue
		}
		if result.ReportPath != "" {
			if err := r.cfg.Store.AddEvidence(&collectorpkg.GovernanceEvidenceRecord{
				EvidenceID: fmt.Sprintf("%s:%s", run.RunID, result.Date),
				RunID:      run.RunID,
				Kind:       "audit_report",
				Path:       result.ReportPath,
				Summary:    fmt.Sprintf("daily_audit report for %s", result.Date),
				CreatedAt:  r.cfg.Now(),
			}); err != nil {
				resultErr = err
				return nil, err
			}
		}
		for _, domain := range result.Domains {
			taskStatus := classifyAuditTaskStatus(domain)
			if taskStatus == collectorpkg.GovernanceTaskStatusClosed {
				continue
			}
			if taskStatus != collectorpkg.GovernanceTaskStatusRepaired {
				partial = true
			}
			reason := domain.Details
			if len(domain.Errors) > 0 {
				reason = strings.Join(domain.Errors, "; ")
			}
			if err := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
				TaskKey:      fmt.Sprintf("%s:%s:%s", collectorpkg.GovernanceJobDailyAudit, domain.Domain, result.Date),
				JobName:      string(collectorpkg.GovernanceJobDailyAudit),
				Domain:       domain.Domain,
				Status:       taskStatus,
				Priority:     classifyAuditTaskPriority(taskStatus),
				Reason:       reason,
				TargetWindow: result.Date,
			}); err != nil {
				resultErr = err
				return nil, err
			}
		}
		if result.Status != "" && result.Status != "passed" {
			partial = true
		}
	}

	return run, nil
}

func classifyAuditTaskStatus(domain AuditDomainResult) collectorpkg.GovernanceTaskStatus {
	switch domain.Status {
	case "unsupported_historical_rebuild":
		return collectorpkg.GovernanceTaskStatusUnsupported
	case "acknowledged":
		return collectorpkg.GovernanceTaskStatusDegraded
	case "reconciled":
		if domain.RepairAttempted {
			return collectorpkg.GovernanceTaskStatusRepaired
		}
		return collectorpkg.GovernanceTaskStatusClosed
	default:
		if len(domain.Errors) > 0 || domain.Status == "partial" || domain.Status == "best_effort" {
			return collectorpkg.GovernanceTaskStatusOpen
		}
	}
	return collectorpkg.GovernanceTaskStatusOpen
}

func classifyAuditTaskPriority(status collectorpkg.GovernanceTaskStatus) int {
	switch status {
	case collectorpkg.GovernanceTaskStatusOpen:
		return 2
	case collectorpkg.GovernanceTaskStatusDegraded:
		return 3
	case collectorpkg.GovernanceTaskStatusUnsupported:
		return 4
	case collectorpkg.GovernanceTaskStatusRepaired:
		return 1
	default:
		return 3
	}
}
