package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	systemgov "github.com/injoyai/tdx/governance"
)

func TestRunDailyCloseSyncUsesGeneralRunTimeout(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	originalDailyCloseSync := dailyCloseSync
	originalShuttingDown := serviceShuttingDown.Load()
	t.Cleanup(func() {
		dailyCloseSync = originalDailyCloseSync
		serviceShuttingDown.Store(originalShuttingDown)
	})
	serviceShuttingDown.Store(false)
	t.Setenv("COLLECTOR_RUN_TIMEOUT", "20ms")

	dailyCloseSync, err = systemgov.NewDailyCloseSyncRunner(systemgov.DailyCloseSyncConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			t.Fatalf("explicit target dates should not use calendar gate")
			return false, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("explicit target dates should not resolve dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			if _, ok := ctx.Deadline(); !ok {
				return nil, errors.New("daily_close_sync context has no deadline")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("new daily close sync runner: %v", err)
	}

	run, err := runDailyCloseSyncWithDates("timeout-test", []string{"20260420"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run daily close sync err = %v, want context deadline exceeded", err)
	}
	if run == nil || run.Status != collectorpkg.GovernanceRunStatusInterrupted {
		t.Fatalf("run = %+v, want interrupted run returned with timeout error", run)
	}
}

func TestExecuteGovernanceWindowMirrorsTimedOutCloseSyncRun(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	originalDailyCloseSync := dailyCloseSync
	originalShuttingDown := serviceShuttingDown.Load()
	t.Cleanup(func() {
		dailyCloseSync = originalDailyCloseSync
		serviceShuttingDown.Store(originalShuttingDown)
	})
	serviceShuttingDown.Store(false)
	t.Setenv("COLLECTOR_RUN_TIMEOUT", "20ms")

	dailyCloseSync, err = systemgov.NewDailyCloseSyncRunner(systemgov.DailyCloseSyncConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local)
		},
		CalendarGate: func(day time.Time) (bool, error) {
			t.Fatalf("explicit target dates should not use calendar gate")
			return false, nil
		},
		ResolveTargetDates: func(ctx context.Context, now time.Time) ([]string, error) {
			t.Fatalf("explicit target dates should not resolve dates")
			return nil, nil
		},
		Execute: func(ctx context.Context, dates []string) ([]collectorpkg.CloseSyncFailure, error) {
			if _, ok := ctx.Deadline(); !ok {
				return nil, errors.New("daily_close_sync context has no deadline")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("new daily close sync runner: %v", err)
	}

	result, err := executeGovernanceWindow(context.Background(), collectorpkg.GovernanceWindowRecord{
		WindowKey:    collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260420"),
		JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
		TargetWindow: "20260420",
	})
	if err != nil {
		t.Fatalf("execute governance window: %v", err)
	}
	if result.Status != collectorpkg.GovernanceWindowStatusTerminalFailed {
		t.Fatalf("result status = %s, want terminal_failed", result.Status)
	}
	if result.RunID == "" || !strings.Contains(result.Summary, "status=interrupted") {
		t.Fatalf("result = %+v, want interrupted run summary", result)
	}
}
