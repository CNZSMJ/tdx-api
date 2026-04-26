package collector

import "testing"

func TestGovernanceCatalogIncludesLifecycleJobsAfterDeepAudit(t *testing.T) {
	catalog := DefaultGovernanceJobCatalog()
	priorities := map[GovernanceJob]int{}
	for _, spec := range catalog {
		priorities[spec.Name] = spec.Priority
	}
	if priorities[GovernanceJobDataLifecycleRestore] != 6 {
		t.Fatalf("restore priority = %d, want 6", priorities[GovernanceJobDataLifecycleRestore])
	}
	if priorities[GovernanceJobDataLifecycleMaintenance] != 7 {
		t.Fatalf("maintenance priority = %d, want 7", priorities[GovernanceJobDataLifecycleMaintenance])
	}
	if priorities[GovernanceJobDeepAuditBackfill] >= priorities[GovernanceJobDataLifecycleMaintenance] {
		t.Fatalf("lifecycle maintenance must run after deep audit: %+v", priorities)
	}
}
