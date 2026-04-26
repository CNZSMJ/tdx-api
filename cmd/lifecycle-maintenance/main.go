package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/collector/lifecycle"
	systemgov "github.com/injoyai/tdx/governance"
	"github.com/injoyai/tdx/internal/appenv"
)

const (
	defaultLifecycleDataset = "a-stock-market-tdx"
	defaultMinFreeBytes     = 10 * 1024 * 1024 * 1024
	defaultSafetyBytes      = 512 * 1024 * 1024
)

type cliConfig struct {
	DataDir             string
	ManifestPath        string
	ColdRoot            string
	RestoreDir          string
	WorkdayDBPath       string
	Dataset             string
	HotCutoffDate       string
	Enable              bool
	AllowPrune          bool
	KeepBackup          bool
	KeepRestoreTestDB   bool
	MinVerifiedSegments int
	MaxCandidates       int
	MaxArchiveDays      int
	MaxInventoryFiles   int
	CandidateSort       string
	MinFreeBytes        int64
	SafetyMarginBytes   int64
	RuntimeBudget       time.Duration
	MaxRuns             int
	UntilClean          bool
	DryRun              bool
	GovernanceRecord    bool
	GovernanceDBPath    string
	GovernanceLockPath  string
	BatchPrefix         string
	SleepBetweenRuns    time.Duration
}

type commandRunRecord struct {
	RunNumber          int                         `json:"run_number"`
	BatchID            string                      `json:"batch_id"`
	DryRun             bool                        `json:"dry_run"`
	DataDir            string                      `json:"data_dir"`
	ManifestPath       string                      `json:"manifest_path,omitempty"`
	GovernanceDBPath   string                      `json:"governance_db_path,omitempty"`
	GovernanceRunID    string                      `json:"governance_run_id,omitempty"`
	GovernanceStatus   string                      `json:"governance_status,omitempty"`
	StartedAt          time.Time                   `json:"started_at"`
	FinishedAt         time.Time                   `json:"finished_at"`
	FreeBytesBefore    int64                       `json:"free_bytes_before"`
	FreeBytesAfter     int64                       `json:"free_bytes_after"`
	MaintenanceResult  lifecycle.MaintenanceResult `json:"maintenance_result"`
	Error              string                      `json:"error,omitempty"`
	StopReason         string                      `json:"stop_reason,omitempty"`
	SelectedCandidates int                         `json:"selected_candidates"`
}

func main() {
	appenv.EnsureLoaded()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Printf("lifecycle-maintenance failed: %v", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	if cfg.DryRun {
		return runDryRun(ctx, cfg, stdout)
	}
	return runMaintenanceLoop(ctx, cfg, stdout)
}

func parseConfig(args []string, stderr io.Writer) (cliConfig, error) {
	appenv.EnsureLoaded()
	defaultDataDir := appenv.ResolveTDXDataDir(filepath.Join(".", "data", "database"))
	cfg := cliConfig{
		DataDir:             defaultDataDir,
		Dataset:             envString("TDX_LIFECYCLE_DATASET", defaultLifecycleDataset),
		Enable:              envBool("TDX_LIFECYCLE_ENABLE", false),
		AllowPrune:          envBool("TDX_LIFECYCLE_ALLOW_PRUNE", false),
		KeepBackup:          envBool("TDX_LIFECYCLE_KEEP_BACKUP", false),
		KeepRestoreTestDB:   envBool("TDX_LIFECYCLE_KEEP_RESTORE_TEST_DB", false),
		MinVerifiedSegments: envInt("TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS", 100),
		MaxCandidates:       envInt("TDX_LIFECYCLE_MAX_CANDIDATES", 1),
		MaxArchiveDays:      envInt("TDX_LIFECYCLE_MAX_ARCHIVE_DAYS", 366),
		MaxInventoryFiles:   envInt("TDX_LIFECYCLE_MAX_INVENTORY_FILES", 0),
		CandidateSort:       envString("TDX_LIFECYCLE_CANDIDATE_SORT", "size_asc"),
		MinFreeBytes:        envInt64("TDX_LIFECYCLE_MIN_FREE_BYTES", defaultMinFreeBytes),
		SafetyMarginBytes:   envInt64("TDX_LIFECYCLE_SAFETY_MARGIN_BYTES", defaultSafetyBytes),
		RuntimeBudget:       envDuration("TDX_LIFECYCLE_RUNTIME_BUDGET", 30*time.Minute),
		MaxRuns:             1,
		GovernanceRecord:    envBool("TDX_LIFECYCLE_GOVERNANCE_RECORD", true),
		BatchPrefix:         "manual-lifecycle",
	}

	fs := flag.NewFlagSet("lifecycle-maintenance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "TDX data directory")
	fs.StringVar(&cfg.ManifestPath, "manifest", "", "cold manifest DB path, defaults to <data-dir>/cold_manifest.db")
	fs.StringVar(&cfg.ColdRoot, "cold-root", "", "local cold storage root, defaults to <data-dir>")
	fs.StringVar(&cfg.RestoreDir, "restore-dir", "", "restore-test output directory, defaults to <data-dir>/cold_restore")
	fs.StringVar(&cfg.WorkdayDBPath, "workday-db", "", "workday DB path, defaults to <data-dir>/workday.db")
	fs.StringVar(&cfg.Dataset, "dataset", cfg.Dataset, "cold URI dataset")
	fs.StringVar(&cfg.HotCutoffDate, "hot-cutoff-date", "", "explicit hot cutoff date in YYYYMMDD; otherwise computed from workday DB")
	fs.BoolVar(&cfg.Enable, "enable", cfg.Enable, "enable lifecycle execution")
	fs.BoolVar(&cfg.AllowPrune, "allow-prune", cfg.AllowPrune, "allow hot DB pruning after export, verify, and restore-test")
	fs.BoolVar(&cfg.KeepBackup, "keep-backup", cfg.KeepBackup, "keep pre-lifecycle hot DB backup after replacement")
	fs.BoolVar(&cfg.KeepRestoreTestDB, "keep-restore-test-db", cfg.KeepRestoreTestDB, "keep temporary restore-test DBs after successful restore verification")
	fs.IntVar(&cfg.MinVerifiedSegments, "min-verified-segments", cfg.MinVerifiedSegments, "minimum verified/restored segments required before pruning")
	fs.IntVar(&cfg.MaxCandidates, "max-candidates", cfg.MaxCandidates, "maximum candidates per maintenance run")
	fs.IntVar(&cfg.MaxArchiveDays, "max-archive-days", cfg.MaxArchiveDays, "maximum calendar-day span archived per segment")
	fs.IntVar(&cfg.MaxInventoryFiles, "max-inventory-files", cfg.MaxInventoryFiles, "maximum SQLite files to deep-inventory after size sorting; 0 means no cap")
	fs.StringVar(&cfg.CandidateSort, "candidate-sort", cfg.CandidateSort, "candidate discovery sort: size_asc or size_desc")
	fs.Int64Var(&cfg.MinFreeBytes, "min-free-bytes", cfg.MinFreeBytes, "free-space watermark required before starting a run")
	fs.Int64Var(&cfg.SafetyMarginBytes, "safety-margin-bytes", cfg.SafetyMarginBytes, "extra free-space safety margin for candidate planning")
	fs.DurationVar(&cfg.RuntimeBudget, "runtime-budget", cfg.RuntimeBudget, "per-run runtime budget")
	fs.IntVar(&cfg.MaxRuns, "max-runs", cfg.MaxRuns, "maximum loop iterations")
	fs.BoolVar(&cfg.UntilClean, "until-clean", false, "continue until no candidates or max-runs is reached")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "plan only; does not write manifest, cold storage, or hot DBs")
	fs.BoolVar(&cfg.GovernanceRecord, "governance-record", cfg.GovernanceRecord, "record run through governance store and acquire system governance lock")
	fs.StringVar(&cfg.GovernanceDBPath, "governance-db", "", "governance DB path, defaults to <data-dir>/governance/system_governance.db")
	fs.StringVar(&cfg.GovernanceLockPath, "governance-lock", "", "governance lock path, defaults to <data-dir>/governance/system_governance.lock")
	fs.StringVar(&cfg.BatchPrefix, "batch-prefix", cfg.BatchPrefix, "batch ID prefix")
	fs.DurationVar(&cfg.SleepBetweenRuns, "sleep-between-runs", 0, "delay between loop iterations")
	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}

	cfg.DataDir = filepath.Clean(cfg.DataDir)
	if cfg.ManifestPath == "" {
		cfg.ManifestPath = filepath.Join(cfg.DataDir, "cold_manifest.db")
	}
	if cfg.ColdRoot == "" {
		cfg.ColdRoot = cfg.DataDir
	}
	if cfg.RestoreDir == "" {
		cfg.RestoreDir = filepath.Join(cfg.DataDir, "cold_restore")
	}
	if cfg.WorkdayDBPath == "" {
		cfg.WorkdayDBPath = filepath.Join(cfg.DataDir, "workday.db")
	}
	governancePaths := collectorpkg.ResolveGovernancePaths(cfg.DataDir)
	if cfg.GovernanceDBPath == "" {
		cfg.GovernanceDBPath = governancePaths.DBPath
	}
	if cfg.GovernanceLockPath == "" {
		cfg.GovernanceLockPath = governancePaths.LockPath
	}
	if strings.TrimSpace(cfg.BatchPrefix) == "" {
		return cliConfig{}, fmt.Errorf("batch-prefix is required")
	}
	if cfg.MaxRuns <= 0 {
		return cliConfig{}, fmt.Errorf("max-runs must be positive")
	}
	if cfg.RuntimeBudget <= 0 {
		return cliConfig{}, fmt.Errorf("runtime-budget must be positive")
	}
	if cfg.MinVerifiedSegments < 0 || cfg.MaxCandidates < 0 || cfg.MaxArchiveDays < 0 || cfg.MaxInventoryFiles < 0 || cfg.MinFreeBytes < 0 || cfg.SafetyMarginBytes < 0 {
		return cliConfig{}, fmt.Errorf("lifecycle numeric limits must be non-negative")
	}
	if cfg.CandidateSort != "size_asc" && cfg.CandidateSort != "size_desc" {
		return cliConfig{}, fmt.Errorf("candidate-sort must be size_asc or size_desc")
	}
	return cfg, nil
}

func runMaintenanceLoop(ctx context.Context, cfg cliConfig, stdout io.Writer) error {
	encoder := json.NewEncoder(stdout)
	for runNumber := 1; runNumber <= cfg.MaxRuns; runNumber++ {
		record, err := runOneMaintenance(ctx, cfg, runNumber)
		if encodeErr := encoder.Encode(record); encodeErr != nil {
			return encodeErr
		}
		if err != nil {
			return err
		}
		if stopReason := loopStopReason(cfg, runNumber, record.MaintenanceResult); stopReason != "" {
			record.StopReason = stopReason
			return nil
		}
		if cfg.SleepBetweenRuns > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.SleepBetweenRuns):
			}
		}
	}
	return nil
}

func runOneMaintenance(ctx context.Context, cfg cliConfig, runNumber int) (commandRunRecord, error) {
	if cfg.GovernanceRecord {
		return runOneGovernedMaintenance(ctx, cfg, runNumber)
	}
	startedAt := time.Now()
	batchID := fmt.Sprintf("%s-%s-%03d", sanitizeBatchPart(cfg.BatchPrefix), startedAt.UTC().Format("20060102T150405Z"), runNumber)
	return runOneRawMaintenance(ctx, cfg, runNumber, batchID, startedAt)
}

func runOneGovernedMaintenance(ctx context.Context, cfg cliConfig, runNumber int) (commandRunRecord, error) {
	startedAt := time.Now()
	freeBefore, _ := freeBytes(cfg.DataDir)
	record := commandRunRecord{
		RunNumber:        runNumber,
		DataDir:          cfg.DataDir,
		ManifestPath:     cfg.ManifestPath,
		GovernanceDBPath: cfg.GovernanceDBPath,
		StartedAt:        startedAt.UTC(),
		FreeBytesBefore:  freeBefore,
	}

	store, err := collectorpkg.OpenGovernanceStore(cfg.GovernanceDBPath)
	if err != nil {
		record.FinishedAt = time.Now().UTC()
		record.FreeBytesAfter, _ = freeBytes(cfg.DataDir)
		record.Error = err.Error()
		return record, err
	}
	defer store.Close()

	paths := collectorpkg.ResolveGovernancePaths(cfg.DataDir)
	paths.DBPath = cfg.GovernanceDBPath
	paths.LockPath = cfg.GovernanceLockPath
	var maintenanceResult lifecycle.MaintenanceResult
	runner, err := systemgov.NewDataLifecycleMaintenanceRunner(systemgov.DataLifecycleMaintenanceConfig{
		Store: store,
		Paths: paths,
		Now:   time.Now,
		Execute: func(ctx context.Context, runID string) (lifecycle.MaintenanceResult, error) {
			record.BatchID = runID
			result, runErr := executeMaintenance(ctx, cfg, runID, time.Now())
			maintenanceResult = result
			return result, runErr
		},
	})
	if err != nil {
		record.FinishedAt = time.Now().UTC()
		record.FreeBytesAfter, _ = freeBytes(cfg.DataDir)
		record.Error = err.Error()
		return record, err
	}
	run, err := runner.Run(ctx, "manual-cli")
	record.FinishedAt = time.Now().UTC()
	record.FreeBytesAfter, _ = freeBytes(cfg.DataDir)
	record.MaintenanceResult = maintenanceResult
	record.SelectedCandidates = maintenanceResult.SelectedCandidates
	if run != nil {
		record.GovernanceRunID = run.RunID
		record.GovernanceStatus = string(run.Status)
		if record.BatchID == "" {
			record.BatchID = run.RunID
		}
	}
	if err != nil {
		record.Error = err.Error()
	}
	return record, err
}

func runOneRawMaintenance(ctx context.Context, cfg cliConfig, runNumber int, batchID string, startedAt time.Time) (commandRunRecord, error) {
	freeBefore, _ := freeBytes(cfg.DataDir)
	record := commandRunRecord{
		RunNumber:       runNumber,
		BatchID:         batchID,
		DataDir:         cfg.DataDir,
		ManifestPath:    cfg.ManifestPath,
		StartedAt:       startedAt.UTC(),
		FreeBytesBefore: freeBefore,
	}
	result, err := executeMaintenance(ctx, cfg, batchID, startedAt)
	record.FinishedAt = time.Now().UTC()
	record.FreeBytesAfter, _ = freeBytes(cfg.DataDir)
	record.MaintenanceResult = result
	record.SelectedCandidates = result.SelectedCandidates
	if err != nil {
		record.Error = err.Error()
	}
	return record, err
}

func executeMaintenance(ctx context.Context, cfg cliConfig, batchID string, startedAt time.Time) (lifecycle.MaintenanceResult, error) {
	manifest, err := lifecycle.OpenManifestStore(cfg.ManifestPath)
	if err != nil {
		return lifecycle.MaintenanceResult{}, err
	}
	defer manifest.Close()

	result, err := lifecycle.MaintenanceRunner{
		MaintenanceResources: lifecycle.MaintenanceResources{
			Manifest: manifest,
			Storage:  lifecycle.NewLocalColdStorage(cfg.ColdRoot),
		},
		MaintenancePaths: lifecycle.MaintenancePaths{
			DataDir:       cfg.DataDir,
			WorkdayDBPath: cfg.WorkdayDBPath,
			Dataset:       cfg.Dataset,
			RestoreDir:    cfg.RestoreDir,
		},
		MaintenancePolicy: lifecycle.MaintenancePolicy{
			Enable:              cfg.Enable,
			AllowPrune:          cfg.AllowPrune,
			KeepBackup:          cfg.KeepBackup,
			KeepRestoreTestDB:   cfg.KeepRestoreTestDB,
			MinVerifiedSegments: cfg.MinVerifiedSegments,
		},
		MaintenanceRuntime: lifecycle.MaintenanceRuntime{
			BatchID:             batchID,
			WriteWatermarkBytes: cfg.MinFreeBytes,
			SafetyMarginBytes:   cfg.SafetyMarginBytes,
			RuntimeBudget:       cfg.RuntimeBudget,
			StartedAt:           startedAt,
		},
		MaintenanceLimits: lifecycle.MaintenanceLimits{
			MaxCandidates:     cfg.MaxCandidates,
			MaxArchiveDays:    cfg.MaxArchiveDays,
			MaxInventoryFiles: cfg.MaxInventoryFiles,
			CandidateSort:     cfg.CandidateSort,
			HotCutoffDate:     cfg.HotCutoffDate,
		},
	}.RunWithContext(ctx)
	return result, err
}

func runDryRun(ctx context.Context, cfg cliConfig, stdout io.Writer) error {
	startedAt := time.Now()
	freeBefore, _ := freeBytes(cfg.DataDir)
	cutoff := cfg.HotCutoffDate
	if cutoff == "" {
		resolved, err := lifecycle.ComputeHotCutoffFromWorkdayDB(cfg.WorkdayDBPath, startedAt, lifecycle.DefaultHotRetentionTradingDays)
		if err != nil {
			return err
		}
		cutoff = resolved.HotCutoffTradeDate
	}
	report, err := lifecycle.InventoryStorage(cfg.DataDir)
	if err != nil {
		return err
	}
	candidates := lifecycle.PlanSteadyStateRetention(report, cutoff).Candidates
	result, err := lifecycle.MaintenanceRunner{
		MaintenanceResources: lifecycle.MaintenanceResources{Candidates: candidates},
		MaintenanceRuntime: lifecycle.MaintenanceRuntime{
			FreeBytes:           freeBefore,
			SafetyMarginBytes:   cfg.SafetyMarginBytes,
			RuntimeBudget:       cfg.RuntimeBudget,
			WriteWatermarkBytes: cfg.MinFreeBytes,
		},
		MaintenanceLimits: lifecycle.MaintenanceLimits{
			MaxCandidates: cfg.MaxCandidates,
			HotCutoffDate: cutoff,
		},
	}.RunWithContext(ctx)
	record := commandRunRecord{
		RunNumber:          1,
		BatchID:            "dry-run",
		DryRun:             true,
		DataDir:            cfg.DataDir,
		StartedAt:          startedAt.UTC(),
		FinishedAt:         time.Now().UTC(),
		FreeBytesBefore:    freeBefore,
		FreeBytesAfter:     freeBefore,
		MaintenanceResult:  result,
		SelectedCandidates: result.SelectedCandidates,
	}
	if err != nil {
		record.Error = err.Error()
	}
	if encodeErr := json.NewEncoder(stdout).Encode(record); encodeErr != nil {
		return encodeErr
	}
	return err
}

func loopStopReason(cfg cliConfig, runNumber int, result lifecycle.MaintenanceResult) string {
	if !cfg.UntilClean {
		return "single-run mode"
	}
	if runNumber >= cfg.MaxRuns {
		return "max-runs reached"
	}
	if result.Status == "skipped" && result.Reason == "no lifecycle candidates" {
		return "no lifecycle candidates"
	}
	if result.SelectedCandidates == 0 && result.ProcessedSegments == 0 {
		return "no selected candidates"
	}
	return ""
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func sanitizeBatchPart(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	value = replacer.Replace(value)
	if value == "" {
		return "manual-lifecycle"
	}
	return value
}
