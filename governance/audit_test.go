package governance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestDailyAuditSkipsNonTradingDayWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewDailyAuditRunner(DailyAuditConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 19, 19, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return false, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("resolve target dates should not run on non-trading day")
			return nil, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*AuditResult, error) {
			t.Fatalf("execute should not run on non-trading day")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-19:00")
	if err != nil {
		t.Fatalf("run daily audit: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusSkipped {
		t.Fatalf("run status = %s, want skipped", run.Status)
	}
	if run.Reason != "non_trading_day_window" {
		t.Fatalf("run reason = %s, want non_trading_day_window", run.Reason)
	}
}

func TestDailyAuditRunWithDatesUsesExplicitWindowOnNonTradingExecutionDay(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	var receivedDates []string
	runner, err := NewDailyAuditRunner(DailyAuditConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 5, 1, 0, 28, 54, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return false, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("explicit target dates should not resolve from execution day")
			return nil, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*AuditResult, error) {
			receivedDates = append(receivedDates, date)
			return &AuditResult{Date: date, Status: "passed"}, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.RunWithDates(context.Background(), "window-dispatcher:daily_audit:20260429,20260430", []string{"20260429", "20260430"})
	if err != nil {
		t.Fatalf("run explicit daily audit: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPassed {
		t.Fatalf("run status = %s, want passed", run.Status)
	}
	if run.TargetWindow != "20260429,20260430" {
		t.Fatalf("run target window = %q, want 20260429,20260430", run.TargetWindow)
	}
	if run.Reason == "non_trading_day_window" {
		t.Fatalf("explicit target window was skipped by execution-day trading gate")
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260429" || receivedDates[1] != "20260430" {
		t.Fatalf("received dates = %+v, want [20260429 20260430]", receivedDates)
	}
}

func TestDailyAuditStoresEvidenceAndClassifiedTasks(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(paths.RootDir, "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewDailyAuditRunner(DailyAuditConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 20, 19, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			return []string{"20260420"}, nil
		},
		Execute: func(ctx context.Context, date string, trigger string) (*AuditResult, error) {
			return &AuditResult{
				Date:       date,
				Status:     "partial",
				ReportPath: filepath.Join(paths.ReportsDir, "reconcile-"+date+".json"),
				Domains: []AuditDomainResult{
					{Domain: "kline", Status: "partial", Errors: []string{"gap remains"}, Details: "repair pending"},
					{Domain: "collector_gap_degraded", Status: "acknowledged", Details: "degraded gap remains"},
					{Domain: "quote_snapshot", Status: "unsupported_historical_rebuild", Details: "historical rebuild unavailable"},
					{Domain: "trade_history", Status: "reconciled", RepairAttempted: true, Details: "auto repaired"},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-19:00")
	if err != nil {
		t.Fatalf("run daily audit: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}

	evidence, err := store.ListEvidenceForRun(run.RunID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].Path == "" {
		t.Fatalf("unexpected evidence rows: %+v", evidence)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("task count = %d, want 3", len(tasks))
	}

	statusByDomain := make(map[string]collectorpkg.GovernanceTaskStatus, len(tasks))
	for _, task := range tasks {
		statusByDomain[task.Domain] = task.Status
	}
	if statusByDomain["kline"] != collectorpkg.GovernanceTaskStatusOpen {
		t.Fatalf("kline task status = %s, want open", statusByDomain["kline"])
	}
	if _, ok := statusByDomain["collector_gap_degraded"]; ok {
		t.Fatalf("collector_gap_degraded acknowledged entry should not create a backlog task")
	}
	if statusByDomain["quote_snapshot"] != collectorpkg.GovernanceTaskStatusUnsupported {
		t.Fatalf("quote_snapshot status = %s, want unsupported", statusByDomain["quote_snapshot"])
	}
	if statusByDomain["trade_history"] != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("trade_history status = %s, want repaired", statusByDomain["trade_history"])
	}
}
