package lifecycle

import "fmt"

type SegmentStatus string

const (
	SegmentPlanned        SegmentStatus = "planned"
	SegmentExporting      SegmentStatus = "exporting"
	SegmentExported       SegmentStatus = "exported"
	SegmentVerifying      SegmentStatus = "verifying"
	SegmentVerified       SegmentStatus = "verified"
	SegmentRestoreTesting SegmentStatus = "restore_testing"
	SegmentRestoreTested  SegmentStatus = "restore_tested"
	SegmentPruning        SegmentStatus = "pruning"
	SegmentHotPruned      SegmentStatus = "hot_pruned"
	SegmentActive         SegmentStatus = "active"
	SegmentFailed         SegmentStatus = "failed"
	SegmentSuperseded     SegmentStatus = "superseded"
)

func AllSegmentStatuses() []SegmentStatus {
	return []SegmentStatus{
		SegmentPlanned,
		SegmentExporting,
		SegmentExported,
		SegmentVerifying,
		SegmentVerified,
		SegmentRestoreTesting,
		SegmentRestoreTested,
		SegmentPruning,
		SegmentHotPruned,
		SegmentActive,
		SegmentFailed,
		SegmentSuperseded,
	}
}

func ValidateSegmentTransition(from, to SegmentStatus) error {
	if from == to {
		return nil
	}
	if to == SegmentFailed && from != SegmentFailed && from != SegmentSuperseded {
		return nil
	}
	allowed := map[SegmentStatus]SegmentStatus{
		SegmentPlanned:        SegmentExporting,
		SegmentExporting:      SegmentExported,
		SegmentExported:       SegmentVerifying,
		SegmentVerifying:      SegmentVerified,
		SegmentVerified:       SegmentRestoreTesting,
		SegmentRestoreTesting: SegmentRestoreTested,
		SegmentRestoreTested:  SegmentPruning,
		SegmentPruning:        SegmentHotPruned,
		SegmentHotPruned:      SegmentActive,
		SegmentFailed:         SegmentPlanned,
		SegmentActive:         SegmentSuperseded,
	}
	if allowed[from] == to {
		return nil
	}
	return fmt.Errorf("invalid segment transition %s -> %s", from, to)
}
