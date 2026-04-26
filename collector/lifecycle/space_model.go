package lifecycle

import (
	"errors"
	"fmt"
	"math"
)

type PeakSpaceInput struct {
	ColdStagingBytes      int64
	ReplacementHotBytes   int64
	SafetyMarginBytes     int64
	ExistingColdBytes     int64
	ExpectedSourceDBBytes int64
}

type PeakSpaceResult struct {
	ColdStagingBytes            int64
	ReplacementHotBytes         int64
	SafetyMarginBytes           int64
	ExistingColdBytes           int64
	ExpectedSourceDBBytes       int64
	AdditionalFreeSpaceRequired int64
}

type CandidateSpaceInput struct {
	SourceDBBytes             int64
	ColdRowShare              float64
	EstimatedParquetRatio     float64
	EstimatedReplacementRatio float64
	SafetyMarginBytes         int64
}

func CalculatePeakSpaceRequirement(input PeakSpaceInput) (PeakSpaceResult, error) {
	if input.ColdStagingBytes < 0 || input.ReplacementHotBytes < 0 || input.SafetyMarginBytes < 0 || input.ExistingColdBytes < 0 || input.ExpectedSourceDBBytes < 0 {
		return PeakSpaceResult{}, errors.New("space model inputs must be non-negative")
	}
	additional := input.ColdStagingBytes + input.ReplacementHotBytes + input.SafetyMarginBytes
	return PeakSpaceResult{
		ColdStagingBytes:            input.ColdStagingBytes,
		ReplacementHotBytes:         input.ReplacementHotBytes,
		SafetyMarginBytes:           input.SafetyMarginBytes,
		ExistingColdBytes:           input.ExistingColdBytes,
		ExpectedSourceDBBytes:       input.ExpectedSourceDBBytes,
		AdditionalFreeSpaceRequired: additional,
	}, nil
}

func EstimatePeakSpaceForCandidate(input CandidateSpaceInput) (PeakSpaceResult, error) {
	if input.SourceDBBytes < 0 || input.SafetyMarginBytes < 0 {
		return PeakSpaceResult{}, errors.New("candidate bytes must be non-negative")
	}
	if input.ColdRowShare < 0 || input.ColdRowShare > 1 {
		return PeakSpaceResult{}, fmt.Errorf("cold row share must be between 0 and 1: %v", input.ColdRowShare)
	}
	if input.EstimatedParquetRatio < 0 || input.EstimatedReplacementRatio < 0 {
		return PeakSpaceResult{}, errors.New("estimated ratios must be non-negative")
	}
	coldStaging := int64(math.Round(float64(input.SourceDBBytes) * input.ColdRowShare * input.EstimatedParquetRatio))
	// Even an all-cold source DB needs a replacement hot DB shell with schema,
	// indexes, and SQLite page overhead, so replacement ratio is source-scoped.
	replacementHot := int64(math.Round(float64(input.SourceDBBytes) * input.EstimatedReplacementRatio))
	return CalculatePeakSpaceRequirement(PeakSpaceInput{
		ColdStagingBytes:      coldStaging,
		ReplacementHotBytes:   replacementHot,
		SafetyMarginBytes:     input.SafetyMarginBytes,
		ExpectedSourceDBBytes: input.SourceDBBytes,
	})
}

func (r PeakSpaceResult) Fits(freeBytes int64) bool {
	return freeBytes >= r.AdditionalFreeSpaceRequired
}
