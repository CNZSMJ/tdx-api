package lifecycle

import "testing"

func TestPlanSteadyStateRetentionOnlySelectsRowsCrossingCutoff(t *testing.T) {
	report := StorageInventoryReport{Tables: []TableInventory{
		{Domain: "trade", Instrument: "sh600000", Table: "TradeHistory", FileBytes: 1000, RowCount: 10, MinDate: "20230101", MaxDate: "20260424"},
		{Domain: "trade", Instrument: "sh600001", Table: "TradeHistory", FileBytes: 1000, RowCount: 10, MinDate: "20260410", MaxDate: "20260424"},
	}}
	plan := PlanSteadyStateRetention(report, "20260401")
	if len(plan.Candidates) != 1 || plan.Candidates[0].Instrument != "sh600000" {
		t.Fatalf("plan = %+v, want only instrument crossing cutoff", plan)
	}
}

func TestPlanSteadyStateRetentionUsesTableSpecificCutoffs(t *testing.T) {
	report := StorageInventoryReport{Tables: []TableInventory{
		{Domain: "trade", Instrument: "sh600000", Table: "TradeHistory", FileBytes: 1000, RowCount: 10, MinDate: "20260115", MaxDate: "20260617"},
		{Domain: "live", Instrument: "sh600000", Table: "TradeLive", FileBytes: 1000, RowCount: 10, MinDate: "20260115", MaxDate: "20260617"},
	}}

	plan := PlanSteadyStateRetentionWithCutoffs(report, func(row TableInventory) string {
		if row.Domain == "trade" {
			return "20260320"
		}
		return "20251128"
	})

	if len(plan.Candidates) != 1 || plan.Candidates[0].Domain != "trade" || plan.Candidates[0].HotCutoffDate != "20260320" {
		t.Fatalf("plan = %+v, want only trade candidate with trade cutoff", plan)
	}
}

func TestLifecycleDebtTrackerSurfacesConsecutiveDebt(t *testing.T) {
	tracker := LifecycleDebtTracker{}
	tracker.RecordDay("20260420", 10)
	tracker.RecordDay("20260421", 12)
	tracker.RecordDay("20260422", 1)
	if tracker.ConsecutiveDebtDays() != 3 {
		t.Fatalf("consecutive debt days = %d", tracker.ConsecutiveDebtDays())
	}
	tracker.RecordDay("20260423", 0)
	if tracker.ConsecutiveDebtDays() != 0 {
		t.Fatalf("debt should reset after zero-debt day")
	}
}

func TestSteadyStateGatePausesOnDiskPressureAndGovernanceContention(t *testing.T) {
	gate := SteadyStateGate{FreeBytes: 99, WriteWatermarkBytes: 100}
	if decision := gate.Decide(); decision.Allowed || decision.Reason == "" {
		t.Fatalf("disk pressure decision = %+v", decision)
	}
	gate = SteadyStateGate{FreeBytes: 1_000, WriteWatermarkBytes: 100, HigherPriorityGovernanceActive: true}
	if decision := gate.Decide(); decision.Allowed || decision.Reason == "" {
		t.Fatalf("governance contention decision = %+v", decision)
	}
	gate = SteadyStateGate{FreeBytes: 1_000, WriteWatermarkBytes: 100}
	if decision := gate.Decide(); !decision.Allowed {
		t.Fatalf("expected allowed decision: %+v", decision)
	}
}
