package main

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestCollectStartupRecoverySnapshotRecognizesCompletedCloseAndAuditWindows(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.Local)
	for _, run := range []collectorpkg.GovernanceRunRecord{
		{
			RunID:        "close-sync-friday",
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			Status:       collectorpkg.GovernanceRunStatusPassed,
			TargetWindow: "20260416,20260417",
			StartedAt:    now.Add(-14 * time.Hour),
			EndedAt:      now.Add(-13 * time.Hour),
		},
		{
			RunID:        "daily-audit-friday",
			JobName:      string(collectorpkg.GovernanceJobDailyAudit),
			Status:       collectorpkg.GovernanceRunStatusPassed,
			TargetWindow: "20260416,20260417",
			StartedAt:    now.Add(-13 * time.Hour),
			EndedAt:      now.Add(-12 * time.Hour),
		},
	} {
		run := run
		if err := store.AddRun(&run); err != nil {
			t.Fatalf("seed governance run %s: %v", run.RunID, err)
		}
	}

	snapshot, err := collectStartupRecoverySnapshot(
		context.Background(),
		now,
		store,
		func(day time.Time) (bool, error) {
			switch day.Format("20060102") {
			case "20260416", "20260417", "20260420":
				return true, nil
			default:
				return false, nil
			}
		},
		func(ctx context.Context, anchor time.Time, limit int) ([]string, error) {
			switch anchor.Format("20060102") {
			case "20260417":
				return []string{"20260416", "20260417"}, nil
			case "20260420":
				return []string{"20260417", "20260420"}, nil
			default:
				t.Fatalf("unexpected anchor for target-date resolution: %s", anchor.Format(time.RFC3339))
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect startup recovery snapshot: %v", err)
	}
	if len(snapshot.MissedJobs) != 1 {
		t.Fatalf("missed jobs = %+v, want only daily_open_refresh", snapshot.MissedJobs)
	}
	if snapshot.MissedJobs[0].Job != collectorpkg.GovernanceJobDailyOpenRefresh || snapshot.MissedJobs[0].TargetWindow != "20260420" {
		t.Fatalf("unexpected missed jobs: %+v", snapshot.MissedJobs)
	}
}

func TestCollectStartupRecoverySnapshotQueuesPreviousTradingWindowsBeforeEveningCutoff(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.Local)
	snapshot, err := collectStartupRecoverySnapshot(
		context.Background(),
		now,
		store,
		func(day time.Time) (bool, error) {
			switch day.Format("20060102") {
			case "20260416", "20260417", "20260420":
				return true, nil
			default:
				return false, nil
			}
		},
		func(ctx context.Context, anchor time.Time, limit int) ([]string, error) {
			switch anchor.Format("20060102") {
			case "20260417":
				return []string{"20260416", "20260417"}, nil
			case "20260420":
				return []string{"20260417", "20260420"}, nil
			default:
				t.Fatalf("unexpected anchor for target-date resolution: %s", anchor.Format(time.RFC3339))
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect startup recovery snapshot: %v", err)
	}

	missedByJob := make(map[collectorpkg.GovernanceJob]string, len(snapshot.MissedJobs))
	for _, missed := range snapshot.MissedJobs {
		missedByJob[missed.Job] = missed.TargetWindow
	}
	if missedByJob[collectorpkg.GovernanceJobDailyOpenRefresh] != "20260420" {
		t.Fatalf("open refresh target window = %q, want 20260420", missedByJob[collectorpkg.GovernanceJobDailyOpenRefresh])
	}
	if missedByJob[collectorpkg.GovernanceJobDailyCloseSync] != "20260416,20260417" {
		t.Fatalf("close sync target window = %q, want 20260416,20260417", missedByJob[collectorpkg.GovernanceJobDailyCloseSync])
	}
	if missedByJob[collectorpkg.GovernanceJobDailyAudit] != "20260416,20260417" {
		t.Fatalf("daily audit target window = %q, want 20260416,20260417", missedByJob[collectorpkg.GovernanceJobDailyAudit])
	}
}

func TestMissedGovernanceWindowCreatesDurableWindowIntent(t *testing.T) {
	originalStore := governanceStore
	defer func() {
		governanceStore = originalStore
	}()

	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()
	governanceStore = store

	if err := upsertMissedGovernanceWindowTask(
		collectorpkg.GovernanceJobDailyAudit,
		"20260427,20260428",
		"scheduled daily-19:00 blocked by active governance lock",
	); err != nil {
		t.Fatalf("upsert missed governance window: %v", err)
	}

	tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("open tasks = %d, want 1", len(tasks))
	}

	window, err := store.GetWindowByKey(collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyAudit, "20260427,20260428"))
	if err != nil {
		t.Fatalf("get governance window: %v", err)
	}
	if window == nil {
		t.Fatalf("expected missed audit window intent")
	}
	if window.Status != collectorpkg.GovernanceWindowStatusQueued || window.TargetWindow != "20260427,20260428" {
		t.Fatalf("unexpected window intent: %+v", window)
	}
	wantDependency := collectorpkg.GovernanceWindowKey(collectorpkg.GovernanceJobDailyCloseSync, "20260427,20260428")
	if window.DependencyKey != wantDependency {
		t.Fatalf("window dependency = %q, want %q", window.DependencyKey, wantDependency)
	}
}
