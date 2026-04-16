package collector

import "time"

type RuntimeStatus struct {
	Now            time.Time           `json:"now"`
	OpenGapCount   int64               `json:"open_gap_count"`
	LastStartupRun *ScheduleRunRecord  `json:"last_startup_run,omitempty"`
	LastFullSync   *ScheduleRunRecord  `json:"last_full_sync,omitempty"`
	LastReconcile  *ScheduleRunRecord  `json:"last_reconcile,omitempty"`
	RecentRuns     []ScheduleRunRecord `json:"recent_runs,omitempty"`
	NextActions    []string            `json:"next_actions,omitempty"`
}

func (r *Runtime) Status() (*RuntimeStatus, error) {
	openGapCount, err := r.store.CountOpenCollectGaps()
	if err != nil {
		return nil, err
	}
	lastStartupRun, err := r.store.LatestScheduleRun(r.cfg.ScheduleName)
	if err != nil {
		return nil, err
	}
	lastFullSync, err := r.store.LatestScheduleRun(r.cfg.DailySyncScheduleName)
	if err != nil {
		return nil, err
	}
	lastReconcile, err := r.store.LatestScheduleRun(r.cfg.ReconcileScheduleName)
	if err != nil {
		return nil, err
	}
	recentRuns, err := r.store.ListRecentScheduleRuns(10)
	if err != nil {
		return nil, err
	}

	status := &RuntimeStatus{
		Now:            r.cfg.Now(),
		OpenGapCount:   openGapCount,
		LastStartupRun: lastStartupRun,
		LastFullSync:   lastFullSync,
		LastReconcile:  lastReconcile,
		RecentRuns:     recentRuns,
		NextActions:    make([]string, 0, 2),
	}
	if openGapCount > 0 {
		status.NextActions = append(status.NextActions, "collector_gap has open records; inspect /api/collector/status and run reconciliation")
	}
	if lastFullSync == nil {
		status.NextActions = append(status.NextActions, "daily full sync has not succeeded yet")
	}
	if lastReconcile == nil {
		status.NextActions = append(status.NextActions, "daily reconcile has not succeeded yet")
	}
	return status, nil
}

func (r *Runtime) StartupScheduleName() string {
	return r.cfg.ScheduleName
}

func (r *Runtime) DailySyncScheduleName() string {
	return r.cfg.DailySyncScheduleName
}

func (r *Runtime) ReconcileScheduleName() string {
	return r.cfg.ReconcileScheduleName
}

func (r *Runtime) HasSuccessfulRunInWindow(scheduleName string, windowStart, windowEnd time.Time) (bool, error) {
	return r.store.HasScheduleRunInWindow(scheduleName, windowStart, windowEnd, "passed")
}

func (r *Runtime) HasSuccessfulReconcileForDate(date string) (bool, error) {
	return r.store.HasScheduleRunWithDetails(r.cfg.ReconcileScheduleName, "date="+date, "passed")
}
