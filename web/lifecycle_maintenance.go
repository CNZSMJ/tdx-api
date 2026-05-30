package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/collector/lifecycle"
	systemgov "github.com/injoyai/tdx/governance"
)

var dataLifecycleMaintenance *systemgov.DataLifecycleMaintenanceRunner
var dataLifecycleMaintenanceRetryDelays = []time.Duration{30 * time.Minute, 60 * time.Minute}

func collectorLifecycleMaintenanceSchedule() string {
	if !lifecycleEnvBoolDefault("TDX_LIFECYCLE_ENABLE", true) {
		return ""
	}
	if schedule := strings.TrimSpace(os.Getenv("TDX_LIFECYCLE_SCHEDULE")); schedule != "" {
		return schedule
	}
	return "0 0 21 * * *"
}

func initDataLifecycleMaintenanceRunner() {
	dataLifecycleMaintenance = nil
	if governanceStore == nil || !lifecycleEnvBoolDefault("TDX_LIFECYCLE_ENABLE", true) {
		return
	}
	runner, err := systemgov.NewDataLifecycleMaintenanceRunner(systemgov.DataLifecycleMaintenanceConfig{
		Store: governanceStore,
		Paths: governancePaths,
		Now:   time.Now,
		Execute: func(ctx context.Context, runID string) (lifecycle.MaintenanceResult, error) {
			return executeDataLifecycleMaintenance(ctx, runID)
		},
	})
	if err != nil {
		log.Printf("初始化 data_lifecycle_maintenance runner 失败: %v", err)
		return
	}
	dataLifecycleMaintenance = runner
}

func runDataLifecycleMaintenance(trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("service shutdown in progress, skip data_lifecycle_maintenance: trigger=%s", trigger)
	}
	ctx, cancel := context.WithCancel(context.Background())
	endRun := governanceActiveRun.begin("data_lifecycle_maintenance", cancel)
	defer func() {
		cancel()
		endRun()
	}()
	return runDataLifecycleMaintenanceWithContext(ctx, trigger)
}

func runScheduledDataLifecycleMaintenance(trigger string) error {
	return runDataLifecycleMaintenanceWithRetry(trigger, runDataLifecycleMaintenance, dataLifecycleMaintenanceRetryDelays, time.After)
}

func runDataLifecycleMaintenanceWithRetry(
	trigger string,
	run func(string) (*collectorpkg.GovernanceRunRecord, error),
	retryDelays []time.Duration,
	after func(time.Duration) <-chan time.Time,
) error {
	if run == nil {
		return fmt.Errorf("data_lifecycle_maintenance runner 未初始化")
	}
	if after == nil {
		after = time.After
	}

	var lastErr error
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		if attempt > 0 {
			delay := retryDelays[attempt-1]
			if delay > 0 {
				log.Printf("data_lifecycle_maintenance 等待治理锁重试: trigger=%s attempt=%d delay=%s previous_err=%v", trigger, attempt+1, delay, lastErr)
				<-after(delay)
			}
			if isServiceShuttingDown() {
				return fmt.Errorf("service shutdown in progress, skip data_lifecycle_maintenance retry: trigger=%s", trigger)
			}
		}

		if _, err := run(trigger); err != nil {
			lastErr = err
			if collectorpkg.IsGovernanceLockHeld(err) && attempt < len(retryDelays) {
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func runDataLifecycleMaintenanceWithContext(ctx context.Context, trigger string) (*collectorpkg.GovernanceRunRecord, error) {
	if dataLifecycleMaintenance == nil {
		return nil, fmt.Errorf("data_lifecycle_maintenance runner 未初始化")
	}
	run, err := dataLifecycleMaintenance.Run(ctx, trigger)
	if err != nil {
		return nil, err
	}
	log.Printf("data_lifecycle_maintenance 完成: trigger=%s status=%s target=%s details=%s", trigger, run.Status, run.TargetWindow, run.Details)
	return run, nil
}

func executeDataLifecycleMaintenance(ctx context.Context, runID string) (lifecycle.MaintenanceResult, error) {
	manifest, err := lifecycle.OpenManifestStore(filepath.Join(databaseDir, "cold_manifest.db"))
	if err != nil {
		return lifecycle.MaintenanceResult{}, err
	}
	defer manifest.Close()

	startedAt := time.Now()
	return lifecycle.MaintenanceRunner{
		MaintenanceResources: lifecycle.MaintenanceResources{
			Manifest: manifest,
			Storage:  lifecycle.NewLocalColdStorage(databaseDir),
		},
		MaintenancePaths: lifecycle.MaintenancePaths{
			DataDir:       databaseDir,
			WorkdayDBPath: filepath.Join(databaseDir, "workday.db"),
			Dataset:       lifecycleEnvString("TDX_LIFECYCLE_DATASET", "a-stock-market-tdx"),
			RestoreDir:    filepath.Join(databaseDir, "cold_restore"),
		},
		MaintenancePolicy: lifecycle.MaintenancePolicy{
			Enable:              lifecycleEnvBoolDefault("TDX_LIFECYCLE_ENABLE", true),
			AllowPrune:          lifecycleEnvBoolDefault("TDX_LIFECYCLE_ALLOW_PRUNE", true),
			KeepBackup:          lifecycleEnvBool("TDX_LIFECYCLE_KEEP_BACKUP"),
			KeepRestoreTestDB:   lifecycleEnvBool("TDX_LIFECYCLE_KEEP_RESTORE_TEST_DB"),
			MinVerifiedSegments: lifecycleEnvInt("TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS", 100),
		},
		MaintenanceRuntime: lifecycle.MaintenanceRuntime{
			BatchID:             runID,
			FreeBytes:           lifecycleEnvInt64("TDX_LIFECYCLE_FREE_BYTES_OVERRIDE", 0),
			WriteWatermarkBytes: lifecycleEnvInt64("TDX_LIFECYCLE_MIN_FREE_BYTES", 10*1024*1024*1024),
			SafetyMarginBytes:   lifecycleEnvInt64("TDX_LIFECYCLE_SAFETY_MARGIN_BYTES", 512*1024*1024),
			RuntimeBudget:       lifecycleEnvDuration("TDX_LIFECYCLE_RUNTIME_BUDGET", 30*time.Minute),
			StartedAt:           startedAt,
		},
		MaintenanceLimits: lifecycle.MaintenanceLimits{
			MaxCandidates:     lifecycleEnvInt("TDX_LIFECYCLE_MAX_CANDIDATES", 400),
			MaxArchiveDays:    lifecycleEnvInt("TDX_LIFECYCLE_MAX_ARCHIVE_DAYS", 366),
			MaxInventoryFiles: lifecycleEnvInt("TDX_LIFECYCLE_MAX_INVENTORY_FILES", 0),
			CandidateSort:     lifecycleEnvString("TDX_LIFECYCLE_CANDIDATE_SORT", "size_desc"),
		},
	}.RunWithContext(ctx)
}

func lifecycleEnvBool(name string) bool {
	return lifecycleEnvBoolDefault(name, false)
}

func lifecycleEnvBoolDefault(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func lifecycleEnvString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func lifecycleEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		log.Printf("lifecycle: 忽略无效的 %s=%q，使用默认 %d", name, raw, fallback)
		return fallback
	}
	return value
}

func lifecycleEnvInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		log.Printf("lifecycle: 忽略无效的 %s=%q，使用默认 %d", name, raw, fallback)
		return fallback
	}
	return value
}

func lifecycleEnvDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		log.Printf("lifecycle: 忽略无效的 %s=%q，使用默认 %s", name, raw, fallback)
		return fallback
	}
	return value
}
