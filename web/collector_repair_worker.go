package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	return runGovernanceRepairWorkerWithContext(context.Background(), trigger, limit)
}

func runGovernanceRepairWorkerWithContext(ctx context.Context, trigger string, limit int) ([]collectorpkg.GovernanceTaskRecord, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("service shutdown in progress, skip governance repair worker: trigger=%s", trigger)
	}
	if repairWorker == nil {
		return nil, fmt.Errorf("governance repair worker 未初始化")
	}
	updated, err := repairWorker.Run(ctx, limit)
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
		return collectorpkg.GovernanceTaskStatusUnsupported, "missed daily_open_refresh cannot be replayed without an explicit target-date runner", nil
	case collectorpkg.GovernanceJobDailyCloseSync:
		return executeStartupRecoveryReplayWindow(ctx, collectorpkg.GovernanceJobDailyCloseSync, task.TargetWindow, task.Reason)
	case collectorpkg.GovernanceJobDailyAudit:
		return executeStartupRecoveryReplayWindow(ctx, collectorpkg.GovernanceJobDailyAudit, task.TargetWindow, task.Reason)
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
		return collectorpkg.GovernanceTaskStatusUnsupported, "interrupted daily_open_refresh cannot be replayed without an explicit target-date runner", nil
	case collectorpkg.GovernanceJobDailyCloseSync:
		return executeStartupRecoveryReplayWindow(ctx, collectorpkg.GovernanceJobDailyCloseSync, run.TargetWindow, task.Reason)
	case collectorpkg.GovernanceJobDailyAudit:
		return executeStartupRecoveryReplayWindow(ctx, collectorpkg.GovernanceJobDailyAudit, run.TargetWindow, task.Reason)
	default:
		return collectorpkg.GovernanceTaskStatusUnsupported, fmt.Sprintf("unsupported interrupted governance job: %s", run.JobName), nil
	}
}

func executeStartupRecoveryReplayWindow(ctx context.Context, job collectorpkg.GovernanceJob, targetWindow, reason string) (collectorpkg.GovernanceTaskStatus, string, error) {
	targetWindow = strings.TrimSpace(targetWindow)
	if targetWindow == "" {
		return collectorpkg.GovernanceTaskStatusBlocked, "missing replay target window", nil
	}
	if governanceStore == nil {
		return collectorpkg.GovernanceTaskStatusBlocked, "governance store unavailable", nil
	}
	if err := upsertGovernanceWindowIntent(job, targetWindow, "", reason, time.Now()); err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	if governanceWindowDispatcher == nil {
		return collectorpkg.GovernanceTaskStatusOpen, fmt.Sprintf("queued replay window %s", collectorpkg.GovernanceWindowKey(job, targetWindow)), nil
	}
	if _, err := runGovernanceWindowDispatcherWithContext(ctx, "startup-recovery-repair"); err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}

	window, err := governanceStore.GetWindowByKey(collectorpkg.GovernanceWindowKey(job, targetWindow))
	if err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	if window == nil {
		return collectorpkg.GovernanceTaskStatusOpen, fmt.Sprintf("replay window missing for %s %s", job, targetWindow), nil
	}
	switch window.Status {
	case collectorpkg.GovernanceWindowStatusPassed, collectorpkg.GovernanceWindowStatusPartial, collectorpkg.GovernanceWindowStatusSkipped:
		return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("replayed %s window %s: %s", job, targetWindow, window.Status), nil
	case collectorpkg.GovernanceWindowStatusTerminalFailed, collectorpkg.GovernanceWindowStatusManualClosed:
		return collectorpkg.GovernanceTaskStatusDegraded, fmt.Sprintf("replay window %s ended as %s: %s", window.WindowKey, window.Status, window.LastError), nil
	default:
		return collectorpkg.GovernanceTaskStatusOpen, fmt.Sprintf("queued replay window %s status=%s", window.WindowKey, window.Status), nil
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
		if decision := decideProFinanceAutoPrefetch(proFinanceDataDir); decision.Disable {
			return collectorpkg.GovernanceTaskStatusBlocked, fmt.Sprintf("professional_finance background refresh paused: %s", decision.Reason), nil
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
		if isRetryableProviderRepairError(err.Error()) {
			return collectorpkg.GovernanceTaskStatusDegraded, err.Error(), nil
		}
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	return collectorpkg.GovernanceTaskStatusRepaired, fmt.Sprintf("%s repaired for %s", failure.Domain, failure.Date), nil
}

func executeDailyAuditRepairTask(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
	targetWindow := strings.TrimSpace(task.TargetWindow)
	if targetWindow == "" {
		return collectorpkg.GovernanceTaskStatusBlocked, "missing audit target window", nil
	}
	retryableErrors := splitAuditRepairErrors(task.Reason)
	if hasOnlyRetryableAuditErrors(retryableErrors) {
		return collectorpkg.GovernanceTaskStatusDegraded, task.Reason, nil
	}

	lock, err := collectorpkg.AcquireGovernanceLock(governancePaths.LockPath)
	if err != nil {
		return collectorpkg.GovernanceTaskStatusOpen, "", err
	}
	defer lock.Release()

	if governanceStore != nil {
		now := time.Now()
		_ = governanceStore.RecordLockMetadata(&collectorpkg.GovernanceLockMetadataRecord{
			LockName:        "system_governance",
			HolderPID:       int64(os.Getpid()),
			HolderJobName:   "repair_worker",
			HolderRunID:     fmt.Sprintf("repair-worker:%s", task.TaskKey),
			AcquiredAt:      now,
			LastHeartbeatAt: now,
		})
	}
	if collectorRuntime == nil {
		return collectorpkg.GovernanceTaskStatusBlocked, "collector runtime unavailable", nil
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
		status := classifyRepairAuditDomainStatus(domain.Status, domain.RepairAttempted, domain.Errors)
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

func classifyRepairAuditDomainStatus(status string, repairAttempted bool, errors []string) collectorpkg.GovernanceTaskStatus {
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
	if repairAttempted && hasOnlyRetryableAuditErrors(errors) {
		return collectorpkg.GovernanceTaskStatusDegraded
	}
	if len(errors) > 0 || status == "partial" || status == "best_effort" {
		return collectorpkg.GovernanceTaskStatusOpen
	}
	return collectorpkg.GovernanceTaskStatusRepaired
}

func hasOnlyRetryableAuditErrors(errors []string) bool {
	if len(errors) == 0 {
		return false
	}
	for _, item := range errors {
		if !isRetryableAuditError(item) {
			return false
		}
	}
	return true
}

func splitAuditRepairErrors(reason string) []string {
	parts := strings.Split(reason, ";")
	errors := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		errors = append(errors, part)
	}
	return errors
}

func isRetryableAuditError(message string) bool {
	text := strings.TrimSpace(strings.ToLower(message))
	if text == "" {
		return false
	}
	return strings.Contains(text, "timeout") ||
		strings.Contains(text, "超时") ||
		strings.Contains(text, "eof") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "context canceled") ||
		strings.Contains(text, "context cancelled") ||
		strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "connection refused") ||
		strings.Contains(text, "数据长度不足")
}

func isRetryableProviderRepairError(message string) bool {
	return isRetryableAuditError(message)
}
