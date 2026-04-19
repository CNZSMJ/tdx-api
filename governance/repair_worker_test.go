package governance

import (
	"context"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestRepairWorkerExecutesOpenTasksAndPersistsOutcome(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	for _, task := range []collectorpkg.GovernanceTaskRecord{
		{
			TaskKey:      "task-open",
			JobName:      string(collectorpkg.GovernanceJobDailyCloseSync),
			Domain:       "trade_history",
			Status:       collectorpkg.GovernanceTaskStatusOpen,
			Priority:     2,
			Reason:       "provider timeout",
			TargetWindow: "20260420",
		},
		{
			TaskKey:      "task-degraded",
			JobName:      string(collectorpkg.GovernanceJobDailyAudit),
			Domain:       "quote_snapshot",
			Status:       collectorpkg.GovernanceTaskStatusDegraded,
			Priority:     3,
			Reason:       "accepted degraded state",
			TargetWindow: "20260420",
		},
	} {
		task := task
		if err := store.UpsertTask(&task); err != nil {
			t.Fatalf("seed task %s: %v", task.TaskKey, err)
		}
	}

	executed := make([]string, 0, 1)
	runner, err := NewRepairWorkerRunner(RepairWorkerConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 10, 0, 0, 0, time.Local)
		},
		Execute: func(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
			executed = append(executed, task.TaskKey)
			return collectorpkg.GovernanceTaskStatusRepaired, "repair completed", nil
		},
	})
	if err != nil {
		t.Fatalf("new repair worker runner: %v", err)
	}

	if _, err := runner.Run(context.Background(), 10); err != nil {
		t.Fatalf("run repair worker: %v", err)
	}

	if len(executed) != 1 || executed[0] != "task-open" {
		t.Fatalf("executed tasks = %+v, want only task-open", executed)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	statusByKey := make(map[string]collectorpkg.GovernanceTaskStatus, len(tasks))
	for _, task := range tasks {
		statusByKey[task.TaskKey] = task.Status
	}
	if statusByKey["task-open"] != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task-open status = %s, want repaired", statusByKey["task-open"])
	}
	if statusByKey["task-degraded"] != collectorpkg.GovernanceTaskStatusDegraded {
		t.Fatalf("task-degraded status = %s, want degraded", statusByKey["task-degraded"])
	}
}

func TestRepairWorkerClaimsTaskBeforeExecutionAndReleasesLockForNestedGovernanceWork(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	task := collectorpkg.GovernanceTaskRecord{
		TaskKey:      "task-missed-open-refresh",
		JobName:      string(collectorpkg.GovernanceJobStartupRecovery),
		Domain:       string(collectorpkg.GovernanceJobDailyOpenRefresh),
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     1,
		Reason:       "missed governance window queued for recovery",
		TargetWindow: "20260421",
	}
	if err := store.UpsertTask(&task); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	claimedStatuses := make([]collectorpkg.GovernanceTaskStatus, 0, 1)
	runner, err := NewRepairWorkerRunner(RepairWorkerConfig{
		Store: store,
		Paths: paths,
		Now: func() time.Time {
			return time.Date(2026, 4, 21, 10, 0, 0, 0, time.Local)
		},
		Execute: func(ctx context.Context, task collectorpkg.GovernanceTaskRecord) (collectorpkg.GovernanceTaskStatus, string, error) {
			tasks, err := store.ListTasksByStatus(collectorpkg.GovernanceTaskStatusInProgress)
			if err != nil {
				return "", "", err
			}
			if len(tasks) != 1 || tasks[0].TaskKey != task.TaskKey {
				t.Fatalf("in-progress tasks during execute = %+v, want only %+v", tasks, task.TaskKey)
			}
			claimedStatuses = append(claimedStatuses, tasks[0].Status)

			lock, err := collectorpkg.AcquireGovernanceLock(paths.LockPath)
			if err != nil {
				t.Fatalf("nested governance lock acquisition failed: %v", err)
			}
			defer lock.Release()

			return collectorpkg.GovernanceTaskStatusRepaired, "nested governance work completed", nil
		},
	})
	if err != nil {
		t.Fatalf("new repair worker runner: %v", err)
	}

	updated, err := runner.Run(context.Background(), 1)
	if err != nil {
		t.Fatalf("run repair worker: %v", err)
	}
	if len(updated) != 1 || updated[0].TaskKey != task.TaskKey {
		t.Fatalf("updated tasks = %+v, want only %+v", updated, task.TaskKey)
	}
	if len(claimedStatuses) != 1 || claimedStatuses[0] != collectorpkg.GovernanceTaskStatusInProgress {
		t.Fatalf("claimed statuses = %+v, want [in_progress]", claimedStatuses)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	if tasks[0].Status != collectorpkg.GovernanceTaskStatusRepaired {
		t.Fatalf("task status = %s, want repaired", tasks[0].Status)
	}
}
