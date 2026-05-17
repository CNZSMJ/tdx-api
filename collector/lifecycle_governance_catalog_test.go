package collector

import "testing"

func TestGovernanceCatalogIncludesLifecycleJobsAfterDeepAudit(t *testing.T) {
	catalog := DefaultGovernanceJobCatalog()
	priorities := map[GovernanceJob]int{}
	for _, spec := range catalog {
		priorities[spec.Name] = spec.Priority
	}
	if priorities[GovernanceJobMarketBillboardSync] != 6 {
		t.Fatalf("market billboard priority = %d, want 6", priorities[GovernanceJobMarketBillboardSync])
	}
	if priorities[GovernanceJobDataLifecycleRestore] != 7 {
		t.Fatalf("restore priority = %d, want 7", priorities[GovernanceJobDataLifecycleRestore])
	}
	if priorities[GovernanceJobDataLifecycleMaintenance] != 8 {
		t.Fatalf("maintenance priority = %d, want 8", priorities[GovernanceJobDataLifecycleMaintenance])
	}
	if priorities[GovernanceJobDeepAuditBackfill] >= priorities[GovernanceJobDataLifecycleMaintenance] {
		t.Fatalf("lifecycle maintenance must run after deep audit: %+v", priorities)
	}
}
