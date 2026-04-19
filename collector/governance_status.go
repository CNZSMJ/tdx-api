package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GovernanceStatusView struct {
	Paths   GovernancePaths               `json:"paths"`
	Jobs    []GovernanceJobStatus         `json:"jobs"`
	Runs    []GovernanceRunRecord         `json:"runs,omitempty"`
	Tasks   []GovernanceTaskRecord        `json:"tasks,omitempty"`
	Domains []DomainHealthSnapshotRecord  `json:"domains,omitempty"`
	Lock    *GovernanceLockMetadataRecord `json:"lock,omitempty"`
}

type GovernanceJobStatus struct {
	Name        GovernanceJob        `json:"name"`
	Priority    int                  `json:"priority"`
	Schedule    string               `json:"schedule,omitempty"`
	LegacyNames []string             `json:"legacy_names,omitempty"`
	Description string               `json:"description,omitempty"`
	Status      GovernanceRunStatus  `json:"status"`
	LastRun     *GovernanceRunRecord `json:"last_run,omitempty"`
}

func (r *Runtime) UnifiedGovernanceStatus(store *GovernanceStore, paths GovernancePaths) (*GovernanceStatusView, error) {
	if r == nil {
		return nil, fmt.Errorf("collector runtime is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("governance store is nil")
	}

	now := r.cfg.Now()
	runtimeStatus, err := r.Status()
	if err != nil {
		return nil, err
	}
	if err := r.projectGovernanceDomains(store, now); err != nil {
		return nil, err
	}

	runs, err := store.ListRecentRuns(20)
	if err != nil {
		return nil, err
	}
	tasks, err := store.ListTasksByStatus()
	if err != nil {
		return nil, err
	}
	domains, err := store.ListLatestDomainHealthSnapshots()
	if err != nil {
		return nil, err
	}
	lock, err := store.LatestLockMetadata()
	if err != nil {
		return nil, err
	}

	return &GovernanceStatusView{
		Paths:   paths,
		Jobs:    buildGovernanceJobStatuses(runtimeStatus, runs),
		Runs:    runs,
		Tasks:   tasks,
		Domains: domains,
		Lock:    lock,
	}, nil
}

func buildGovernanceJobStatuses(runtimeStatus *RuntimeStatus, governanceRuns []GovernanceRunRecord) []GovernanceJobStatus {
	runMap := make(map[GovernanceJob]GovernanceRunRecord, len(governanceRuns))
	for _, run := range governanceRuns {
		job := GovernanceJob(run.JobName)
		if _, ok := runMap[job]; ok {
			continue
		}
		runMap[job] = run
	}

	legacyRuns := map[GovernanceJob]*GovernanceRunRecord{
		GovernanceJobStartupRecovery: legacyScheduleRunToGovernanceRun(GovernanceJobStartupRecovery, runtimeStatus.LastStartupRun),
		GovernanceJobDailyCloseSync:  legacyScheduleRunToGovernanceRun(GovernanceJobDailyCloseSync, runtimeStatus.LastFullSync),
		GovernanceJobDailyAudit:      legacyScheduleRunToGovernanceRun(GovernanceJobDailyAudit, runtimeStatus.LastReconcile),
	}

	jobs := make([]GovernanceJobStatus, 0, 5)
	for _, spec := range DefaultGovernanceJobCatalog() {
		item := GovernanceJobStatus{
			Name:        spec.Name,
			Priority:    spec.Priority,
			Schedule:    spec.Schedule,
			LegacyNames: append([]string(nil), spec.LegacyNames...),
			Description: spec.Description,
			Status:      spec.DefaultState,
		}
		if run, ok := runMap[spec.Name]; ok {
			copy := run
			item.LastRun = &copy
			item.Status = copy.Status
		} else if legacyRun := legacyRuns[spec.Name]; legacyRun != nil {
			item.LastRun = legacyRun
			item.Status = legacyRun.Status
		}
		jobs = append(jobs, item)
	}
	return jobs
}

func legacyScheduleRunToGovernanceRun(job GovernanceJob, run *ScheduleRunRecord) *GovernanceRunRecord {
	if run == nil {
		return nil
	}
	return &GovernanceRunRecord{
		RunID:     fmt.Sprintf("legacy:%s:%d", job, run.StartedAt.UnixNano()),
		JobName:   string(job),
		Trigger:   run.ScheduleName,
		Status:    normalizeLegacyGovernanceRunStatus(run.Status),
		Details:   run.Details,
		StartedAt: run.StartedAt,
		EndedAt:   run.EndedAt,
	}
}

func normalizeLegacyGovernanceRunStatus(status string) GovernanceRunStatus {
	switch strings.TrimSpace(status) {
	case string(GovernanceRunStatusPlanned):
		return GovernanceRunStatusPlanned
	case string(GovernanceRunStatusRunning):
		return GovernanceRunStatusRunning
	case string(GovernanceRunStatusPassed):
		return GovernanceRunStatusPassed
	case string(GovernanceRunStatusPartial):
		return GovernanceRunStatusPartial
	case string(GovernanceRunStatusFailed):
		return GovernanceRunStatusFailed
	case string(GovernanceRunStatusInterrupted):
		return GovernanceRunStatusInterrupted
	case string(GovernanceRunStatusSkipped):
		return GovernanceRunStatusSkipped
	default:
		return GovernanceRunStatusFailed
	}
}

func (r *Runtime) projectGovernanceDomains(store *GovernanceStore, now time.Time) error {
	for _, domain := range []string{"codes", "workday", "kline", "trade_history", "order_history", "live_capture", "finance", "f10"} {
		snapshot, err := r.buildDomainSnapshot(domain, now)
		if err != nil {
			return err
		}
		if err := store.UpsertDomainHealthSnapshot(snapshot); err != nil {
			return err
		}
	}

	blockSnapshot, err := r.buildBlockSnapshot(now)
	if err != nil {
		return err
	}
	if err := store.UpsertDomainHealthSnapshot(blockSnapshot); err != nil {
		return err
	}

	current, err := store.GetDomainHealthSnapshot("professional_finance")
	if err != nil {
		return err
	}
	if current != nil {
		return nil
	}

	return store.UpsertDomainHealthSnapshot(&DomainHealthSnapshotRecord{
		Domain:     "professional_finance",
		Status:     "unknown",
		Freshness:  "unknown",
		Coverage:   "unknown",
		Summary:    "projected by dedicated professional_finance governance in later sprints",
		SnapshotAt: now,
	})
}

func (r *Runtime) buildDomainSnapshot(domain string, now time.Time) (*DomainHealthSnapshotRecord, error) {
	cursor, count, err := r.latestDomainCursor(domain)
	if err != nil {
		return nil, err
	}
	openGaps, err := r.countDomainGaps(domain, CollectGapStatusOpen)
	if err != nil {
		return nil, err
	}
	degradedGaps, err := r.countDomainGaps(domain, CollectGapStatusDegraded)
	if err != nil {
		return nil, err
	}

	status := "healthy"
	freshness := "fresh"
	coverage := "covered"
	if count == 0 {
		status = "missing"
		freshness = "stale"
		coverage = "unknown"
	}
	if openGaps > 0 || degradedGaps > 0 {
		status = "degraded"
		coverage = "gap_open"
	}
	if domainFileHint(domain, r.cfg) == "" && count == 0 {
		status = "unknown"
	}
	return &DomainHealthSnapshotRecord{
		Domain:          domain,
		Status:          status,
		Freshness:       freshness,
		Coverage:        coverage,
		LatestCursor:    latestCursorValue(cursor),
		LatestWatermark: latestCursorValue(cursor),
		Summary:         fmt.Sprintf("cursor_records=%d open_gaps=%d degraded_gaps=%d", count, openGaps, degradedGaps),
		SnapshotAt:      now,
	}, nil
}

func (r *Runtime) buildBlockSnapshot(now time.Time) (*DomainHealthSnapshotRecord, error) {
	var count int64
	if r.store != nil && r.store.engine != nil {
		if ok, err := r.store.engine.IsTableExist(new(BlockGroupRecord)); err != nil {
			return nil, err
		} else if ok {
			value, err := r.store.engine.Table(new(BlockGroupRecord)).Count(new(BlockGroupRecord))
			if err != nil {
				return nil, err
			}
			count = value
		}
	}
	status := "healthy"
	freshness := "fresh"
	coverage := "covered"
	if count == 0 {
		status = "missing"
		freshness = "stale"
		coverage = "unknown"
	}
	return &DomainHealthSnapshotRecord{
		Domain:     "block",
		Status:     status,
		Freshness:  freshness,
		Coverage:   coverage,
		Summary:    fmt.Sprintf("published_block_groups=%d", count),
		SnapshotAt: now,
	}, nil
}

func (r *Runtime) latestDomainCursor(domain string) (*CollectCursorRecord, int64, error) {
	record := new(CollectCursorRecord)
	count, err := r.store.engine.Where("Domain = ?", domain).Count(new(CollectCursorRecord))
	if err != nil {
		return nil, 0, err
	}
	has, err := r.store.engine.Where("Domain = ?", domain).Desc("UpdatedAt").Get(record)
	if err != nil {
		return nil, 0, err
	}
	if !has {
		return nil, count, nil
	}
	return record, count, nil
}

func (r *Runtime) countDomainGaps(domain, status string) (int64, error) {
	return r.store.engine.Where("Domain = ? AND Status = ?", domain, status).Count(new(CollectGapRecord))
}

func latestCursorValue(record *CollectCursorRecord) string {
	if record == nil {
		return ""
	}
	return record.Cursor
}

func domainFileHint(domain string, cfg RuntimeConfig) string {
	switch domain {
	case "codes":
		return cfg.Metadata.CodesDBPath
	case "workday":
		return cfg.Metadata.WorkdayDBPath
	case "kline":
		return cfg.Kline.BaseDir
	case "trade_history":
		return cfg.Trade.BaseDir
	case "order_history":
		return cfg.OrderHistory.BaseDir
	case "live_capture":
		return cfg.Live.BaseDir
	case "finance", "f10":
		return cfg.Fundamentals.BaseDir
	case "block":
		return cfg.Block.BaseDir
	default:
		return ""
	}
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func ensureParentDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return filepath.Dir(path)
}
