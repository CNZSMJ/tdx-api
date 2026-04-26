package lifecycle

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSchedulerSelectsOnlyCandidatesThatFitPeakSpace(t *testing.T) {
	candidates := []LifecycleCandidate{
		{Domain: "trade", TableName: "TradeHistory", Instrument: "large", SourceDBBytes: 1_000, ColdRowShare: 0.9, EstimatedParquetRatio: 0.5, EstimatedReplacementRatio: 0.1},
		{Domain: "trade", TableName: "TradeHistory", Instrument: "small", SourceDBBytes: 100, ColdRowShare: 0.9, EstimatedParquetRatio: 0.5, EstimatedReplacementRatio: 0.1},
	}
	plan, err := SelectLifecycleCandidates(candidates, SchedulerOptions{
		FreeBytes:         100,
		SafetyMarginBytes: 10,
		MaxCandidates:     10,
	})
	if err != nil {
		t.Fatalf("select candidates: %v", err)
	}
	if len(plan.Selected) != 1 || plan.Selected[0].Instrument != "small" {
		t.Fatalf("selected = %+v, want only small feasible candidate", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason == "" {
		t.Fatalf("skipped = %+v", plan.Skipped)
	}
}

func TestMaintenanceRunnerSkipsWhenHigherPriorityGovernanceActive(t *testing.T) {
	runner := MaintenanceRunner{
		MaintenanceRuntime: MaintenanceRuntime{
			HigherPriorityActive: true,
			FreeBytes:            1_000_000,
			RuntimeBudget:        time.Minute,
		},
	}
	result, err := runner.Run()
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "skipped" || result.Reason == "" {
		t.Fatalf("unexpected maintenance result: %+v", result)
	}
}

func TestMaintenanceRunnerRecordsLifecycleDebtAndStatus(t *testing.T) {
	store, err := OpenManifestStore(filepath.Join(t.TempDir(), "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer store.Close()
	if err := store.CreateSegment(ColdSegment{
		SegmentID:      "seg-failed",
		ArchiveBatchID: "batch-1",
		Domain:         "trade",
		TableName:      "TradeHistory",
		Instrument:     "sh600000",
		StartDate:      "20230101",
		EndDate:        "20230131",
		ColdURI:        "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2023/part-failed-000.parquet",
		Status:         SegmentFailed,
		SchemaVersion:  1,
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("create failed segment: %v", err)
	}
	runner := MaintenanceRunner{
		MaintenanceResources: MaintenanceResources{
			Manifest: store,
			Candidates: []LifecycleCandidate{
				{Domain: "trade", TableName: "TradeHistory", Instrument: "sh600001", SourceDBBytes: 100, ColdRowShare: 0.8, EstimatedParquetRatio: 0.4, EstimatedReplacementRatio: 0.1},
			},
		},
		MaintenanceRuntime: MaintenanceRuntime{
			FreeBytes:     1_000_000,
			RuntimeBudget: time.Minute,
		},
	}
	result, err := runner.Run()
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "passed" || result.SelectedCandidates != 1 || result.LifecycleDebt.FailedSegments != 1 {
		t.Fatalf("unexpected maintenance result: %+v", result)
	}
	status, err := LifecycleStatusFromManifest(store)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.SegmentCounts[string(SegmentFailed)] != 1 || len(status.Alerts) == 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
}
