package lifecycle

import "testing"

func TestSegmentStateMachineAllowsOnlyDocumentedTransitions(t *testing.T) {
	legal := [][2]SegmentStatus{
		{SegmentPlanned, SegmentExporting},
		{SegmentExporting, SegmentExported},
		{SegmentExported, SegmentVerifying},
		{SegmentVerifying, SegmentVerified},
		{SegmentVerified, SegmentRestoreTesting},
		{SegmentRestoreTesting, SegmentRestoreTested},
		{SegmentRestoreTested, SegmentPruning},
		{SegmentPruning, SegmentHotPruned},
		{SegmentHotPruned, SegmentActive},
		{SegmentFailed, SegmentPlanned},
		{SegmentActive, SegmentSuperseded},
	}
	for _, pair := range legal {
		if err := ValidateSegmentTransition(pair[0], pair[1]); err != nil {
			t.Fatalf("%s -> %s should be legal: %v", pair[0], pair[1], err)
		}
	}
	for _, status := range AllSegmentStatuses() {
		if status == SegmentFailed || status == SegmentSuperseded {
			continue
		}
		if err := ValidateSegmentTransition(status, SegmentFailed); err != nil {
			t.Fatalf("%s -> failed should be legal: %v", status, err)
		}
	}
}

func TestSegmentStateMachineRejectsUnsafeSkips(t *testing.T) {
	illegal := [][2]SegmentStatus{
		{SegmentPlanned, SegmentVerified},
		{SegmentVerified, SegmentPruning},
		{SegmentHotPruned, SegmentSuperseded},
		{SegmentActive, SegmentPruning},
		{SegmentSuperseded, SegmentActive},
	}
	for _, pair := range illegal {
		if err := ValidateSegmentTransition(pair[0], pair[1]); err == nil {
			t.Fatalf("%s -> %s should be illegal", pair[0], pair[1])
		}
	}
}
