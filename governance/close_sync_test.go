package governance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestDailyCloseSyncSkipsNonTradingDayWindow(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	runner, err := NewDailyCloseSyncRunner(DailyCloseSyncConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 19, 18, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return false, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("resolve target dates should not run on non-trading day")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			t.Fatalf("execute should not run on non-trading day")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-18:00")
	if err != nil {
		t.Fatalf("run daily close sync: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusSkipped {
		t.Fatalf("run status = %s, want skipped", run.Status)
	}
	if run.Reason != "non_trading_day_window" {
		t.Fatalf("run reason = %s, want non_trading_day_window", run.Reason)
	}
}

func TestDailyCloseSyncCreatesRepairTasksForExecutionFailures(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(filepath.Join(paths.RootDir, "system_governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	var receivedDates []string
	runner, err := NewDailyCloseSyncRunner(DailyCloseSyncConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			return true, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			return []string{"20260417", "20260420"}, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			receivedDates = append(receivedDates, dates...)
			return []collectorpkg.CloseSyncFailure{
				{Domain: "trade_history", Date: "20260420", Instrument: "sh600000", Reason: "provider timeout"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	run, err := runner.Run(context.Background(), "daily-18:00")
	if err != nil {
		t.Fatalf("run daily close sync: %v", err)
	}
	if run.Status != collectorpkg.GovernanceRunStatusPartial {
		t.Fatalf("run status = %s, want partial", run.Status)
	}
	if len(receivedDates) != 2 || receivedDates[0] != "20260417" || receivedDates[1] != "20260420" {
		t.Fatalf("received dates = %+v, want [20260417 20260420]", receivedDates)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %d, want 1", len(tasks))
	}
	if tasks[0].Domain != "trade_history" || tasks[0].TargetWindow != "20260420" {
		t.Fatalf("unexpected repair task: %+v", tasks[0])
	}
	var failure collectorpkg.CloseSyncFailure
	if err := json.Unmarshal([]byte(tasks[0].PayloadJSON), &failure); err != nil {
		t.Fatalf("unmarshal failure payload: %v", err)
	}
	if failure.Domain != "trade_history" || failure.Date != "20260420" || failure.Instrument != "sh600000" {
		t.Fatalf("unexpected failure payload: %+v", failure)
	}
}
