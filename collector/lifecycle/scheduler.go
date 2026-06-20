package lifecycle

import (
	"context"
	"fmt"
	"time"
)

type LifecycleCandidate struct {
	DBPath                    string
	Domain                    string
	TableName                 string
	Instrument                string
	MinDate                   string
	MaxDate                   string
	HotCutoffDate             string
	SourceDBBytes             int64
	ColdRowShare              float64
	EstimatedParquetRatio     float64
	EstimatedReplacementRatio float64
}

type SchedulerOptions struct {
	FreeBytes         int64
	SafetyMarginBytes int64
	MaxCandidates     int
}

type SkippedCandidate struct {
	Candidate LifecycleCandidate
	Reason    string
}

type CandidatePlan struct {
	Selected []LifecycleCandidate
	Skipped  []SkippedCandidate
}

func SelectLifecycleCandidates(candidates []LifecycleCandidate, opts SchedulerOptions) (CandidatePlan, error) {
	ordered := append([]LifecycleCandidate(nil), candidates...)
	if opts.MaxCandidates <= 0 {
		opts.MaxCandidates = len(ordered)
	}
	remainingFree := opts.FreeBytes
	plan := CandidatePlan{}
	for _, candidate := range ordered {
		peak, err := EstimatePeakSpaceForCandidate(CandidateSpaceInput{
			SourceDBBytes:             candidate.SourceDBBytes,
			ColdRowShare:              candidate.ColdRowShare,
			EstimatedParquetRatio:     candidate.EstimatedParquetRatio,
			EstimatedReplacementRatio: candidate.EstimatedReplacementRatio,
			SafetyMarginBytes:         opts.SafetyMarginBytes,
		})
		if err != nil {
			return plan, err
		}
		if !peak.Fits(remainingFree) {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: fmt.Sprintf("peak_space_required=%d free=%d", peak.AdditionalFreeSpaceRequired, remainingFree)})
			continue
		}
		if len(plan.Selected) >= opts.MaxCandidates {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: "batch candidate budget reached"})
			continue
		}
		plan.Selected = append(plan.Selected, candidate)
		remainingFree -= peak.AdditionalFreeSpaceRequired
	}
	return plan, nil
}

type LifecycleDebt struct {
	FailedSegments int
	Skipped        int
}

type MaintenanceResources struct {
	Manifest   *ManifestStore
	Storage    ColdStorage
	Candidates []LifecycleCandidate
}

type MaintenancePaths struct {
	DataDir       string
	WorkdayDBPath string
	Dataset       string
	RestoreDir    string
}

type MaintenancePolicy struct {
	Enable              bool
	AllowPrune          bool
	KeepBackup          bool
	KeepRestoreTestDB   bool
	MinVerifiedSegments int
}

type MaintenanceRuntime struct {
	BatchID              string
	HigherPriorityActive bool
	FreeBytes            int64
	WriteWatermarkBytes  int64
	SafetyMarginBytes    int64
	RuntimeBudget        time.Duration
	ProcessWorkers       int
	StartedAt            time.Time
}

type MaintenanceLimits struct {
	MaxCandidates     int
	MaxArchiveDays    int
	MaxInventoryFiles int
	CandidateSort     string
	HotCutoffDate     string
	CandidateDomains  []string
}

type MaintenanceRunner struct {
	MaintenanceResources
	MaintenancePaths
	MaintenancePolicy
	MaintenanceRuntime
	MaintenanceLimits
}

type MaintenanceResult struct {
	Status             string
	Reason             string
	SelectedCandidates int
	SkippedCandidates  int
	LifecycleDebt      LifecycleDebt
	ProcessedSegments  int
	PrunedSegments     int
	RowsArchived       int64
	BytesReleased      int64
	HotCutoffDate      string
}

func (r MaintenanceRunner) Run() (MaintenanceResult, error) {
	return r.RunWithContext(context.Background())
}

func (r MaintenanceRunner) planningOnlyRun() (MaintenanceResult, error) {
	if r.HigherPriorityActive {
		return MaintenanceResult{Status: "skipped", Reason: "higher-priority governance job active"}, nil
	}
	if r.RuntimeBudget <= 0 {
		return MaintenanceResult{Status: "skipped", Reason: "runtime budget exhausted or not configured"}, nil
	}
	plan, err := SelectLifecycleCandidates(r.Candidates, SchedulerOptions{
		FreeBytes:         r.FreeBytes,
		SafetyMarginBytes: r.SafetyMarginBytes,
		MaxCandidates:     effectiveMaxCandidates(r.MaxCandidates, len(r.Candidates)),
	})
	if err != nil {
		return MaintenanceResult{}, err
	}
	debt := LifecycleDebt{Skipped: len(plan.Skipped)}
	if r.Manifest != nil {
		status, err := LifecycleStatusFromManifest(r.Manifest)
		if err != nil {
			return MaintenanceResult{}, err
		}
		debt.FailedSegments = status.SegmentCounts[string(SegmentFailed)]
	}
	return MaintenanceResult{
		Status:             "passed",
		SelectedCandidates: len(plan.Selected),
		SkippedCandidates:  len(plan.Skipped),
		LifecycleDebt:      debt,
		HotCutoffDate:      r.HotCutoffDate,
	}, nil
}

func effectiveMaxCandidates(configured, total int) int {
	if configured > 0 {
		return configured
	}
	return total
}
