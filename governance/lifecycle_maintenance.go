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

type DataLifecycleMaintenanceConfig struct {
	Store    *collectorpkg.GovernanceStore
	Paths    collectorpkg.GovernancePaths
	Now      func() time.Time
	Hostname string
	Execute  func(context.Context, string) (lifecycle.MaintenanceResult, error)
}

type DataLifecycleMaintenanceRunner struct {
	cfg DataLifecycleMaintenanceConfig
}

func NewDataLifecycleMaintenanceRunner(cfg DataLifecycleMaintenanceConfig) (*DataLifecycleMaintenanceRunner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("data lifecycle maintenance requires governance store")
	}
	if cfg.Paths.DBPath == "" || cfg.Paths.LockPath == "" {
		return nil, fmt.Errorf("data lifecycle maintenance requires governance paths")
	}
	if cfg.Execute == nil {
		return nil, fmt.Errorf("data lifecycle maintenance requires executor")
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
	return &DataLifecycleMaintenanceRunner{cfg: cfg}, nil
}

func (r *DataLifecycleMaintenanceRunner) Run(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	lock, err := collectorpkg.AcquireGovernanceLock(r.cfg.Paths.LockPath)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	startedAt := r.cfg.Now()
	run := &collectorpkg.GovernanceRunRecord{
		RunID:        fmt.Sprintf("data-lifecycle-maintenance-%d", startedAt.UnixNano()),
		JobName:      string(collectorpkg.GovernanceJobDataLifecycleMaintenance),
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
		HolderJobName:   string(collectorpkg.GovernanceJobDataLifecycleMaintenance),
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

	result, err := r.cfg.Execute(ctx, run.RunID)
	if err != nil {
		resultErr = err
		return nil, err
	}
	run.TargetWindow = maintenanceTargetWindow(startedAt, result)
	run.Details = maintenanceDetails(result)
	switch result.Status {
	case "passed":
		run.Status = collectorpkg.GovernanceRunStatusPassed
	case "skipped":
		run.Status = collectorpkg.GovernanceRunStatusSkipped
		run.Reason = result.Reason
	case "interrupted":
		run.Status = collectorpkg.GovernanceRunStatusInterrupted
		run.Reason = result.Reason
	case "partial":
		run.Status = collectorpkg.GovernanceRunStatusPartial
		run.Reason = result.Reason
	default:
		run.Status = collectorpkg.GovernanceRunStatusPartial
		run.Reason = "unknown lifecycle maintenance result status: " + result.Status
	}
	return run, nil
}

func maintenanceTargetWindow(startedAt time.Time, result lifecycle.MaintenanceResult) string {
	if strings.TrimSpace(result.HotCutoffDate) == "" {
		return startedAt.Format("20060102")
	}
	return "hot_cutoff=" + result.HotCutoffDate
}

func maintenanceDetails(result lifecycle.MaintenanceResult) string {
	parts := []string{
		fmt.Sprintf("hot_cutoff=%s", result.HotCutoffDate),
		fmt.Sprintf("selected_candidates=%d", result.SelectedCandidates),
		fmt.Sprintf("skipped_candidates=%d", result.SkippedCandidates),
		fmt.Sprintf("processed_segments=%d", result.ProcessedSegments),
		fmt.Sprintf("pruned_segments=%d", result.PrunedSegments),
		fmt.Sprintf("rows_archived=%d", result.RowsArchived),
		fmt.Sprintf("bytes_released=%d", result.BytesReleased),
	}
	if result.Reason != "" {
		parts = append(parts, "reason="+result.Reason)
	}
	return strings.Join(parts, " ")
}
