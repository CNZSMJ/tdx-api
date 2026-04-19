package governance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestDeepAuditBackfillStoresHistoricalEvidenceAndTasks(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewDeepAuditBackfillRunner(DeepAuditBackfillConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 3, 0, 0, 0, time.Local)
		},
		Execute: func(ctx context.Context, req DeepAuditBackfillRequest, trigger string) (*DeepAuditBackfillResult, error) {
			return &DeepAuditBackfillResult{
				SummaryPath: filepath.Join(paths.ReportsDir, "deep-audit-summary-20260421.json"),
				Reports: []AuditResult{
					{
						Date:       "20260331",
						Status:     "partial",
						ReportPath: filepath.Join(paths.ReportsDir, "deep-audit-20260331.json"),
						Domains: []AuditDomainResult{
							{Domain: "kline", Status: "partial", Errors: []string{"historical gap remains"}, Details: "repair pending"},
							{Domain: "collector_gap_degraded", Status: "acknowledged", Details: "accepted degraded backlog"},
						},
					},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new deep audit runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "manual-deep-audit", DeepAuditBackfillRequest{
		StartDate: "20260301",
		EndDate:   "20260331",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("run deep audit backfill: %v", err)
	}
	if run.JobName != string(collectorpkg.GovernanceJobDeepAuditBackfill) {
		t.Fatalf("run job = %s, want %s", run.JobName, collectorpkg.GovernanceJobDeepAuditBackfill)
	}
	if run.TargetWindow != "20260301:20260331" {
		t.Fatalf("run target window = %s, want 20260301:20260331", run.TargetWindow)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}

	evidence, err := store.ListEvidenceForRun(run.RunID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(evidence))
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	statusByDomain := make(map[string]collectorpkg.GovernanceTaskStatus, len(tasks))
	for _, task := range tasks {
		statusByDomain[task.Domain] = task.Status
	}
	if statusByDomain["kline"] != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("kline task status = %s, want open", statusByDomain["kline"])
	}
	if statusByDomain["collector_gap_degraded"] != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("collector_gap_degraded status = %s, want degraded", statusByDomain["collector_gap_degraded"])
	}
}
