package collector

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGovernanceRepairBatchDryRunDoesNotApplyChanges(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		fakeGovernanceRepairOperation{},
	})
	if err != nil {
		t.Fatalf("run dry-run repair: %v", err)
	}
	if result.Mode != GovernanceRepairModeDryRun || result.BackupPath != "" {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 0 {
		t.Fatalf("unexpected dry-run operation summary: %+v", result.Operations)
	}

	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("dry-run wrote tasks: %+v", tasks)
	}
}

func TestGovernanceRepairBatchApplyBacksUpBeforeApplyingChanges(t *testing.T) {
	baseDir := t.TempDir()
	paths := ResolveGovernancePaths(baseDir)
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	result, err := RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath:    paths.DBPath,
		BackupDir: filepath.Join(baseDir, "backups"),
		Mode:      GovernanceRepairModeApply,
		Now:       fixedRepairNow,
	}, []GovernanceRepairOperation{
		fakeGovernanceRepairOperation{},
	})
	if err != nil {
		t.Fatalf("run apply repair: %v", err)
	}
	if result.Mode != GovernanceRepairModeApply || result.BackupPath == "" {
		t.Fatalf("apply did not report backup: %+v", result)
	}
	if _, err := OpenGovernanceStore(result.BackupPath); err != nil {
		t.Fatalf("backup is not a readable governance DB: %v", err)
	}
	if len(result.Operations) != 1 || result.Operations[0].Planned != 1 || result.Operations[0].Applied != 1 {
		t.Fatalf("unexpected apply operation summary: %+v", result.Operations)
	}

	tasks, err := store.ListTasksByStatus(GovernanceTaskStatusOpen)
	if err != nil {
		t.Fatalf("list open tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskKey != "repair:test" {
		t.Fatalf("apply did not persist repair task: %+v", tasks)
	}
}

func TestGovernanceRepairBatchDryRunUsesReadOnlyStore(t *testing.T) {
	paths := ResolveGovernancePaths(t.TempDir())
	store, err := OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close governance store: %v", err)
	}

	_, err = RunGovernanceRepairBatch(GovernanceRepairBatchOptions{
		DBPath: paths.DBPath,
		Mode:   GovernanceRepairModeDryRun,
		Now:    fixedRepairNow,
	}, []GovernanceRepairOperation{
		writingPlanGovernanceRepairOperation{},
	})
	if err == nil {
		t.Fatalf("dry-run allowed a write during planning")
	}

	store, err = OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("reopen governance store: %v", err)
	}
	defer store.Close()
	tasks, err := store.ListTasksByStatus()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("dry-run persisted a planning write: %+v", tasks)
	}
}

func fixedRepairNow() time.Time {
	return time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)
}

type fakeGovernanceRepairOperation struct{}

func (fakeGovernanceRepairOperation) Name() string {
	return "fake_repair"
}

func (fakeGovernanceRepairOperation) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	return []GovernanceRepairChange{{
		Operation: "fake_repair",
		Target:    "repair:test",
		Action:    "upsert_open_task",
		Reason:    "test repair operation",
	}}, nil
}

func (fakeGovernanceRepairOperation) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey: "repair:test",
		JobName: string(GovernanceJobStartupRecovery),
		Domain:  "test",
		Status:  GovernanceTaskStatusOpen,
		Reason:  "test repair operation",
	})
	if err != nil {
		return nil, err
	}
	return []GovernanceRepairChange{{
		Operation: "fake_repair",
		Target:    "repair:test",
		Action:    "upsert_open_task",
		Reason:    "test repair operation",
	}}, nil
}

type writingPlanGovernanceRepairOperation struct{}

func (writingPlanGovernanceRepairOperation) Name() string {
	return "bad_plan"
}

func (writingPlanGovernanceRepairOperation) Plan(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	err := store.UpsertTask(&GovernanceTaskRecord{
		TaskKey: "repair:bad-plan",
		JobName: string(GovernanceJobStartupRecovery),
		Domain:  "test",
		Status:  GovernanceTaskStatusOpen,
		Reason:  "bad dry-run write",
	})
	return nil, err
}

func (writingPlanGovernanceRepairOperation) Apply(store *GovernanceStore) ([]GovernanceRepairChange, error) {
	return nil, nil
}
