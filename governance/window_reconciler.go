package governance

import (
	"fmt"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type GovernanceWindowIntent struct {
	Job           collectorpkg.GovernanceJob
	TargetWindow  string
	DueAt         time.Time
	Priority      int
	DependencyKey string
}

type WindowReconcilerConfig struct {
	Store *collectorpkg.GovernanceStore
	Now   func() time.Time
}

type WindowReconciler struct {
	cfg WindowReconcilerConfig
}

func NewWindowReconciler(cfg WindowReconcilerConfig) *WindowReconciler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &WindowReconciler{cfg: cfg}
}

func (r *WindowReconciler) EnqueueDue(intents []GovernanceWindowIntent) (int, error) {
	if r == nil || r.cfg.Store == nil {
		return 0, fmt.Errorf("window reconciler requires governance store")
	}
	now := r.cfg.Now()
	created := 0
	for _, intent := range intents {
		if intent.Job == "" || intent.TargetWindow == "" || intent.DueAt.After(now) {
			continue
		}
		windowKey := collectorpkg.GovernanceWindowKey(intent.Job, intent.TargetWindow)
		existing, err := r.cfg.Store.GetWindowByKey(windowKey)
		if err != nil {
			return created, err
		}
		if existing != nil {
			continue
		}
		window := &collectorpkg.GovernanceWindowRecord{
			WindowKey:     windowKey,
			JobName:       string(intent.Job),
			TargetWindow:  intent.TargetWindow,
			DueAt:         intent.DueAt,
			Priority:      intent.Priority,
			Status:        collectorpkg.GovernanceWindowStatusQueued,
			DependencyKey: intent.DependencyKey,
			ScheduledAt:   intent.DueAt,
			EnqueuedAt:    now,
		}
		if err := r.cfg.Store.UpsertWindow(window); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
