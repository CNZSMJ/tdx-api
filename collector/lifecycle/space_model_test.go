package lifecycle

import "testing"

func TestPeakSpaceRequirementIncludesColdHotAndSafetyMargin(t *testing.T) {
	got, err := CalculatePeakSpaceRequirement(PeakSpaceInput{
		ColdStagingBytes:      100,
		ReplacementHotBytes:   20,
		SafetyMarginBytes:     30,
		ExistingColdBytes:     500,
		ExpectedSourceDBBytes: 1000,
	})
	if err != nil {
		t.Fatalf("peak space: %v", err)
	}
	if got.AdditionalFreeSpaceRequired != 150 {
		t.Fatalf("additional free space = %d, want 150", got.AdditionalFreeSpaceRequired)
	}
	if !got.Fits(150) || got.Fits(149) {
		t.Fatalf("fits boundary wrong: %+v", got)
	}
}

func TestPeakSpaceWorstCaseAllColdDB(t *testing.T) {
	got, err := EstimatePeakSpaceForCandidate(CandidateSpaceInput{
		SourceDBBytes:             1_000,
		ColdRowShare:              1.0,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.05,
		SafetyMarginBytes:         100,
	})
	if err != nil {
		t.Fatalf("estimate peak space: %v", err)
	}
	if got.ColdStagingBytes != 400 || got.ReplacementHotBytes != 50 || got.AdditionalFreeSpaceRequired != 550 {
		t.Fatalf("worst-case estimate = %+v", got)
	}
}
