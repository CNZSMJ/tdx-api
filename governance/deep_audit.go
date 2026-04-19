package governance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type DeepAuditBackfillRequest struct {
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	BacklogOnly bool   `json:"backlog_only"`
	Limit       int    `json:"limit"`
}

type DeepAuditBackfillResult struct {
	SummaryPath string
	Reports     []AuditResult
}

type DeepAuditBackfillConfig struct {
	Store    *collectorpkg.GovernanceStore
	Paths    collectorpkg.GovernancePaths
	Now      func() time.Time
	Hostname string
	Execute  func(context.Context, DeepAuditBackfillRequest, string) (*DeepAuditBackfillResult, error)
}

type DeepAuditBackfillRunner struct {
	cfg DeepAuditBackfillConfig
}

func NewDeepAuditBackfillRunner(cfg DeepAuditBackfillConfig) (*DeepAuditBackfillRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("deep audit backfill requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("deep audit backfill requires governance paths")
	}
	if cfg.Execute == nil {
		return nil, fmt.Errorf("deep audit backfill requires executor")
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
	return &DeepAuditBackfillRunner{cfg: cfg}, nil
}

func (r *DeepAuditBackfillRunner) Run(ctx context.Context, trigger string, req DeepAuditBackfillRequest) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("deep-audit-backfill-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDeepAuditBackfill),
		Trigger:      trigger,
		Status:       collectorpkg.GovernanceRunStatusRunning,
		TargetWindow: deepAuditTargetWindow(req),
		StartedAt:    startedAt,
	}
	if err := r.cfg.Store.AddRun(run); err != nil {
		return nil, err
	}
	if err := r.cfg.Store.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
		LockName:        "system_governance",
		HolderPID:       int64(os.Getpid()),
		HolderHostname:  r.cfg.Hostname,
		HolderJobName:   string(collectorpkg.GovernanceJobDeepAuditBackfill),
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

	result, err := r.cfg.Execute(ctx, req, trigger)
	if err != nil {
		resultErr = err
		return nil, err
	}
	if result == nil {
		return run, nil
	}
	if strings.TrimSpace(result.SummaryPath) != "" {
		if err := r.cfg.Store.AddEvidence(&collectorpkg.GovernanceEvidenceRecord{
			EvidenceID: fmt.Sprintf("%s:summary", run.RunID),
			RunID:      run.RunID,
			Kind:       "deep_audit_summary",
			Path:       result.SummaryPath,
			Summary:    "deep audit backfill summary",
			CreatedAt:  r.cfg.Now(),
		}); err != nil {
			resultErr = err
			return nil, err
		}
	}

	for _, report := range result.Reports {
		if strings.TrimSpace(report.ReportPath) != "" {
			if err := r.cfg.Store.AddEvidence(&collectorpkg.GovernanceEvidenceRecord{
				EvidenceID: fmt.Sprintf("%s:%s", run.RunID, report.Date),
				RunID:      run.RunID,
				Kind:       "deep_audit_report",
				Path:       report.ReportPath,
				Summary:    fmt.Sprintf("deep audit report for %s", report.Date),
				CreatedAt:  r.cfg.Now(),
			}); err != nil {
				resultErr = err
				return nil, err
			}
		}

		for _, domain := range report.Domains {
			taskStatus := classifyAuditTaskStatus(domain)
			if taskStatus == collectorpkg.GovernanceTaskStatusClosed {
				continue
			}
			if taskStatus != collectorpkg.GovernanceTaskStatusRepaired {
				partial = true
			}
			reason := strings.TrimSpace(domain.Details)
			if len(domain.Errors) > 0 {
				reason = strings.Join(domain.Errors, "; ")
			}
			if err := r.cfg.Store.UpsertTask(&collectorpkg.GovernanceTaskRecord{
				TaskKey:      fmt.Sprintf("%s:%s:%s", collectorpkg.GovernanceJobDeepAuditBackfill, domain.Domain, report.Date),
				JobName:      string(collectorpkg.GovernanceJobDeepAuditBackfill),
				Domain:       domain.Domain,
				Status:       taskStatus,
				Priority:     classifyAuditTaskPriority(taskStatus),
				Reason:       reason,
				TargetWindow: report.Date,
			}); err != nil {
				resultErr = err
				return nil, err
			}
		}

		if report.Status != "" && report.Status != "passed" {
			partial = true
		}
	}

	return run, nil
}

func deepAuditTargetWindow(req DeepAuditBackfillRequest) string {
	start := strings.TrimSpace(req.StartDate)
	end := strings.TrimSpace(req.EndDate)
	switch {
	case start != "" && end != "":
		return start + ":" + end
	case start != "":
		return start + ":" + start
	case end != "":
		return end + ":" + end
	case req.BacklogOnly:
		return "backlog"
	default:
		return "manual"
	}
}
