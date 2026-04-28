package governance

import (
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestWindowReconcilerEnqueuesOverdueIntentOnce(t *testing.T) {
	paths := collectorpkg.ResolveGovernancePaths(t.TempDir())
	store, err := collectorpkg.OpenGovernanceStore(paths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer store.Close()

	dueAt := time.Date(2026, 4, 28, 9, 0, 0, 0, time.Local)
	now := time.Date(2026, 4, 28, 9, 40, 0, 0, time.Local)
	reconciler := NewWindowReconciler(WindowReconcilerConfig{
		Store: store,
		Now: func() time.Time {
			return now
		},
	})

	spec := GovernanceWindowIntent{
		Job:          collectorpkg.GovernanceJobDailyOpenRefresh,
		TargetWindow: "20260428",
		DueAt:        dueAt,
		Priority:     2,
	}
	created, err := reconciler.EnqueueDue([]GovernanceWindowIntent{spec})
	if err != nil {
		t.Fatalf("enqueue due: %v", err)
	}
	if created != 1 {
		t.Fatalf("created windows = %d, want 1", created)
	}

	created, err = reconciler.EnqueueDue([]GovernanceWindowIntent{spec})
	if err != nil {
		t.Fatalf("enqueue due again: %v", err)
	}
	if created != 0 {
		t.Fatalf("created windows after duplicate enqueue = %d, want 0", created)
	}

	windows, err := store.ListWindowsByStatus(collectorpkg.GovernanceWindowStatusQueued)
	if err != nil {
		t.Fatalf("list queued windows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("queued windows = %d, want 1", len(windows))
	}
	if !windows[0].ScheduledAt.Equal(dueAt) || !windows[0].EnqueuedAt.Equal(now) {
		t.Fatalf("unexpected schedule timestamps: %+v", windows[0])
	}
}
