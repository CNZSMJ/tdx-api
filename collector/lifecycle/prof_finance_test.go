package lifecycle

import "testing"

func TestProfessionalFinanceSlimmingPlanSeparatesServingAndRawTables(t *testing.T) {
	inventory := StorageInventoryReport{Tables: []TableInventory{
		{Domain: "professional_finance", Table: "prof_finance_source_value_raw", FileBytes: 1000, RowCount: 100, MinDate: "20230101", MaxDate: "20251231"},
		{Domain: "professional_finance", Table: "prof_finance_report_payload", FileBytes: 500, RowCount: 50, MinDate: "20230101", MaxDate: "20251231"},
	}}
	plan := PlanProfessionalFinanceSlimming(inventory)
	if len(plan.RawSourceArchiveCandidates) != 1 || plan.RawSourceArchiveCandidates[0].Table != "prof_finance_source_value_raw" {
		t.Fatalf("raw candidates = %+v", plan.RawSourceArchiveCandidates)
	}
	if len(plan.HotServingTables) == 0 || !containsString(plan.HotServingTables, "prof_finance_report_payload") {
		t.Fatalf("serving tables = %+v", plan.HotServingTables)
	}
	if plan.EstimatedReclaimBytes <= 0 {
		t.Fatalf("estimated reclaim not computed: %+v", plan)
	}
}

func TestProfessionalFinanceSlimmingPlanDoesNotDoubleCountDBFileBytes(t *testing.T) {
	inventory := StorageInventoryReport{Tables: []TableInventory{
		{Domain: "professional_finance", DBPath: "/tmp/professional_finance.db", Table: "prof_finance_source_file", FileBytes: 1000, RowCount: 100},
		{Domain: "professional_finance", DBPath: "/tmp/professional_finance.db", Table: "prof_finance_source_report", FileBytes: 1000, RowCount: 100},
		{Domain: "professional_finance", DBPath: "/tmp/professional_finance.db", Table: "prof_finance_source_value_raw", FileBytes: 1000, RowCount: 100},
	}}

	plan := PlanProfessionalFinanceSlimming(inventory)
	if plan.EstimatedReclaimBytes != 1000 {
		t.Fatalf("estimated reclaim bytes = %d, want one DB file size", plan.EstimatedReclaimBytes)
	}
}

func TestProfessionalFinanceArchiveChunksAreTableLevelAndBounded(t *testing.T) {
	chunks := PlanProfessionalFinanceArchiveChunks(TableInventory{
		Domain:   "professional_finance",
		Table:    "prof_finance_source_value_raw",
		RowCount: 1_200_000,
		MinDate:  "20200101",
		MaxDate:  "20251231",
	}, 500_000)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %+v, want 3 chunks", chunks)
	}
	if chunks[0].Table != "prof_finance_source_value_raw" || chunks[0].MaxRows != 500_000 {
		t.Fatalf("unexpected first chunk: %+v", chunks[0])
	}
}
