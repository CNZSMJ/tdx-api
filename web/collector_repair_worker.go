package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

var repairWorker *systemgov.RepairWorkerRunner

func initGovernanceRepairWorker() {
	if governanceStore == nil {
		return
	}
	runner, err := systemgov.NewRepairWorkerRunner(systemgov.RepairWorkerConfig{
		Store: governanceStore,
		Paths: governancePaths,
		Now:   time.Now,
		Execute: func(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
			return executeGovernanceRepairTask(ctx, task)
		},
	})
	if err != nil {
		log.Printf("初始化 governance repair worker 失败: %v", err)
		return
	}
	repairWorker = runner
}

func runGovernanceRepairWorker(trigger string, limit int) ([]collectorpkg.GovernanceTaskRecord, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("service shutdown in progress, skip governance repair worker: trigger=%s", trigger)
	}
	if repairWorker == nil {
		return nil, fmt.Errorf("governance repair worker 未初始化")
	}
	updated, err := repairWorker.Run(context.Background(), limit)
	if err != nil {
		return updated, err
	}
	if len(updated) > 0 {
		log.Printf("governance repair worker 完成: trigger=%s updated=%d", trigger, len(updated))
	}
	return updated, nil
}

func executeGovernanceRepairTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	switch collectorpkg.GovernanceJob(task.JobName) {
	case collectorpkg.GovernanceJobStartupRecovery:
		return executeStartupRecoveryTask(ctx, task)
	case collectorpkg.GovernanceJobDailyOpenRefresh:
		return executeOpenRefreshTask(ctx, task)
	case collectorpkg.GovernanceJobDailyCloseSync:
		return executeCloseSyncTask(ctx, task)
	case collectorpkg.GovernanceJobDailyAudit:
		return executeDailyAuditRepairTask(ctx, task)
	default:
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("unsupported governance repair job: %s", task.JobName), nil
	}
}

func executeStartupRecoveryTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	switch collectorpkg.GovernanceJob(task.Domain) {
	case collectorpkg.GovernanceJobDailyOpenRefresh:
		run, err := runDailyOpenRefresh("startup-recovery")
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed missed %s run with status=%s", run.JobName, run.Status), nil
	case collectorpkg.GovernanceJobDailyCloseSync:
		run, err := runDailyCloseSyncWithDates("startup-recovery", governanceTargetDates(task.TargetWindow))
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed missed %s run with status=%s", run.JobName, run.Status), nil
	case collectorpkg.GovernanceJobDailyAudit:
		run, err := runDailyAuditWithDates("startup-recovery", governanceTargetDates(task.TargetWindow))
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed missed %s run with status=%s", run.JobName, run.Status), nil
	case "interrupted_run":
		return executeInterruptedStartupRecoveryTask(ctx, task)
	default:
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("unsupported startup recovery task domain: %s", task.Domain), nil
	}
}

func executeInterruptedStartupRecoveryTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	if governanceStore == nil {
		return collectorpkg.GovernanceTaskStatusBlocked, "governance store unavailable", nil
	}
	runID := strings.TrimSpace(task.Reason)
	if runID == "" {
		return collectorpkg.GovernanceTaskStatusBlocked, "missing interrupted run id", nil
	}
	run, err := governanceStore.GetRunByRunID(runID)
	if err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	if run == nil {
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("interrupted governance run not found: %s", runID), nil
	}

	switch collectorpkg.GovernanceJob(run.JobName) {
	case collectorpkg.GovernanceJobDailyOpenRefresh:
		replayedRun, err := runDailyOpenRefresh("startup-recovery-interrupted")
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed interrupted %s run with status=%s", replayedRun.JobName, replayedRun.Status), nil
	case collectorpkg.GovernanceJobDailyCloseSync:
		replayedRun, err := runDailyCloseSyncWithDates("startup-recovery-interrupted", governanceTargetDates(run.TargetWindow))
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed interrupted %s run with status=%s", replayedRun.JobName, replayedRun.Status), nil
	case collectorpkg.GovernanceJobDailyAudit:
		replayedRun, err := runDailyAuditWithDates("startup-recovery-interrupted", governanceTargetDates(run.TargetWindow))
		if err != nil {
			return collectorpkg.GovernanceTaskStatusOpen, "", err
		}
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed interrupted %s run with status=%s", replayedRun.JobName, replayedRun.Status), nil
	default:
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("unsupported interrupted governance job: %s", run.JobName), nil
	}
}

func executeOpenRefreshTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	var err error
	switch task.Domain {
	case "codes":
		if manager == nil || manager.Codes == nil {
			return collectorpkg.GovernanceTaskStatusBlocked, "codes manager unavailable", nil
		}
		err = manager.Codes.Update()
		if err == nil && tdx.DefaultCodes != nil && tdx.DefaultCodes != manager.Codes {
			err = tdx.DefaultCodes.Update(true)
		}
	case "workday":
		if manager == nil || manager.Workday == nil {
			return collectorpkg.GovernanceTaskStatusBlocked, "workday manager unavailable", nil
		}
		err = manager.Workday.Update()
	case "block":
		if collectorRuntime == nil {
			return collectorpkg.GovernanceTaskStatusBlocked, "collector runtime unavailable", nil
		}
		err = collectorRuntime.BlockService().SyncBlocks(ctx)
	case "professional_finance":
		if proFinanceService == nil {
			return collectorpkg.GovernanceTaskStatusBlocked, "professional finance service unavailable", nil
		}
		synced, reason, syncErr := proFinanceService.SyncIfNeeded(ctx)
		if syncErr != nil {
			err = syncErr
			break
		}
		if !synced {
			return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("professional_finance already current (%s)", reason), nil
		}
		return collectorpkg.GovernanceTaskStatusRepaired, "professional_finance refreshed", nil
	default:
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("unsupported daily_open_refresh domain: %s", task.Domain), nil
	}
	if err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("%s refreshed", task.Domain), nil
}

func executeCloseSyncTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	if collectorRuntime == nil {
		return collectorpkg.GovernanceTaskStatusBlocked, "collector runtime unavailable", nil
	}
	failure := closeSyncFailureFromTask(task)
	if err := collectorRuntime.RepairCloseSyncFailure(ctx, failure); err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("%s repaired for %s", failure.Domain, failure.Date), nil
}

func executeDailyAuditRepairTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	if collectorRuntime == nil {
		return collectorpkg.GovernanceTaskStatusBlocked, "collector runtime unavailable", nil
	}
	targetWindow := strings.TrimSpace(task.TargetWindow)
	if targetWindow == "" {
		return collectorpkg.GovernanceTaskStatusBlocked, "missing audit target window", nil
	}

	report, err := collectorRuntime.ReconcileDateWithTrigger(ctx, targetWindow, "repair-worker")
	if err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	if report == nil {
		return collectorpkg.GovernanceTaskStatusOpen, "repair audit returned no report", nil
	}

	for _, domain := range report.Domains {
		if domain.Domain != task.Domain {
			continue
		}
		status := classifyRepairAuditDomainStatus(domain.Status, domain.RepairAttempted, len(domain.Errors) > 0)
		reason := strings.TrimSpace(domain.Details)
		if len(domain.Errors) > 0 {
			reason = strings.Join(domain.Errors, "; ")
		}
		if status == collectorpkg.GovernanceTaskStatusClosed {
			status = collectorpkg.GovernanceTaskStatusRepaired
		}
		return status, reason, nil
	}

	if task.Domain == "collector_gap" && report.OpenGapCount == 0 {
		return collectorpkg.GovernanceTaskStatusRepaired, "collector gaps reconciled", nil
	}
	return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed audit for %s", task.Domain), nil
}

func closeSyncFailureFromTask(task collectorpkg.GovernanceTaskRecord) collectorpkg.CloseSyncFailure {
	failure := collectorpkg.CloseSyncFailure{
		Domain: task.Domain,
		Date:   task.TargetWindow,
		Reason: task.Reason,
	}
	if strings.TrimSpace(task.PayloadJSON) == "" {
		return failure
	}
	if err := json.Unmarshal([]byte(task.PayloadJSON), &failure); err != nil {
		return failure
	}
	if failure.Domain == "" {
		failure.Domain = task.Domain
	}
	if failure.Date == "" {
		failure.Date = task.TargetWindow
	}
	if failure.Reason == "" {
		failure.Reason = task.Reason
	}
	return failure
}

func governanceTargetDates(targetWindow string) []string {
	parts := strings.Split(strings.TrimSpace(targetWindow), ",")
	dates := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dates = append(dates, part)
	}
	return dates
}

func classifyRepairAuditDomainStatus(status string, repairAttempted bool, hasErrors bool) collectorpkg.GovernanceTaskStatus {
	switch status {
	case "unsupported_historical_rebuild":
		return collectorpkg.GovernanceTaskStatusUnsupported
	case "acknowledged":
		return collectorpkg.GovernanceTaskStatusDegraded
	case "reconciled":
		if repairAttempted {
			return collectorpkg.GovernanceTaskStatusRepaired
		}
		return collectorpkg.GovernanceTaskStatusClosed
	case "blocked":
		return collectorpkg.GovernanceTaskStatusBlocked
	}
	if hasErrors || status == "partial" || status == "best_effort" {
		return collectorpkg.GovernanceTaskStatusOpen
	}
	return collectorpkg.GovernanceTaskStatusRepaired
}
