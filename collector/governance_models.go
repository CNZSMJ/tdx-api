package collector

import (
	"fmt"
	"strings"
	"time"
)

type GovernanceJob string

const (
	GovernanceJobStartupRecovery          GovernanceJob = "startup_recovery"
	GovernanceJobDailyOpenRefresh         GovernanceJob = "daily_open_refresh"
	GovernanceJobDailyCloseSync           GovernanceJob = "daily_close_sync"
	GovernanceJobDailyAudit               GovernanceJob = "daily_audit"
	GovernanceJobDeepAuditBackfill        GovernanceJob = "deep_audit_backfill"
	GovernanceJobMarketBillboardSync      GovernanceJob = "market_billboard_sync"
	GovernanceJobDataLifecycleMaintenance GovernanceJob = "data_lifecycle_maintenance"
	GovernanceJobDataLifecycleRestore     GovernanceJob = "data_lifecycle_restore"
)

type GovernanceRunStatus string

const (
	GovernanceRunStatusPlanned     GovernanceRunStatus = "planned"
	GovernanceRunStatusRunning     GovernanceRunStatus = "running"
	GovernanceRunStatusPassed      GovernanceRunStatus = "passed"
	GovernanceRunStatusPartial     GovernanceRunStatus = "partial"
	GovernanceRunStatusFailed      GovernanceRunStatus = "failed"
	GovernanceRunStatusInterrupted GovernanceRunStatus = "interrupted"
	GovernanceRunStatusSkipped     GovernanceRunStatus = "skipped"
)

type GovernanceTaskStatus string

const (
	GovernanceTaskStatusOpen        GovernanceTaskStatus = "open"
	GovernanceTaskStatusInProgress  GovernanceTaskStatus = "in_progress"
	GovernanceTaskStatusRepaired    GovernanceTaskStatus = "repaired"
	GovernanceTaskStatusDegraded    GovernanceTaskStatus = "degraded"
	GovernanceTaskStatusBlocked     GovernanceTaskStatus = "blocked"
	GovernanceTaskStatusUnsupported GovernanceTaskStatus = "unsupported"
	GovernanceTaskStatusClosed      GovernanceTaskStatus = "closed"
)

type GovernanceWindowStatus string

const (
	GovernanceWindowStatusPlanned           GovernanceWindowStatus = "planned"
	GovernanceWindowStatusQueued            GovernanceWindowStatus = "queued"
	GovernanceWindowStatusWaitingDependency GovernanceWindowStatus = "waiting_dependency"
	GovernanceWindowStatusRunning           GovernanceWindowStatus = "running"
	GovernanceWindowStatusContinued         GovernanceWindowStatus = "continued"
	GovernanceWindowStatusPassed            GovernanceWindowStatus = "passed"
	GovernanceWindowStatusPartial           GovernanceWindowStatus = "partial"
	GovernanceWindowStatusSkipped           GovernanceWindowStatus = "skipped"
	GovernanceWindowStatusTerminalFailed    GovernanceWindowStatus = "terminal_failed"
	GovernanceWindowStatusManualClosed      GovernanceWindowStatus = "manual_closed"
)

func GovernanceWindowKey(job GovernanceJob, targetWindow string) string {
	return fmt.Sprintf("%s:%s", job, strings.TrimSpace(targetWindow))
}

func GovernanceWindowStatusIsTerminal(status GovernanceWindowStatus) bool {
	switch status {
	case GovernanceWindowStatusPassed,
		GovernanceWindowStatusPartial,
		GovernanceWindowStatusSkipped,
		GovernanceWindowStatusTerminalFailed,
		GovernanceWindowStatusManualClosed:
		return true
	default:
		return false
	}
}

type GovernanceJobSpec struct {
	Name         GovernanceJob       `json:"name"`
	Priority     int                 `json:"priority"`
	Schedule     string              `json:"schedule,omitempty"`
	LegacyNames  []string            `json:"legacy_names,omitempty"`
	Description  string              `json:"description,omitempty"`
	DefaultState GovernanceRunStatus `json:"default_state"`
}

func DefaultGovernanceJobCatalog() []GovernanceJobSpec {
	return []GovernanceJobSpec{
		{
			Name:         GovernanceJobStartupRecovery,
			Priority:     1,
			LegacyNames:  []string{"collector_startup_catchup"},
			Description:  "service-start backlog recovery and missed-window replay",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDailyOpenRefresh,
			Priority:     2,
			Schedule:     "0 0 9 * * *",
			LegacyNames:  []string{"codes_auto_update", "workday_auto_update", "block_auto_refresh", "professional_finance_auto_prefetch"},
			Description:  "09:00 reference-domain refresh",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDailyCloseSync,
			Priority:     3,
			Schedule:     "0 0 18 * * *",
			LegacyNames:  []string{"collector_daily_full_sync"},
			Description:  "18:00 recent-window sync",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDailyAudit,
			Priority:     4,
			Schedule:     "0 0 19 * * *",
			LegacyNames:  []string{"collector_daily_reconcile"},
			Description:  "19:00 audit, classify, and light repair",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDeepAuditBackfill,
			Priority:     5,
			Description:  "low-peak historical audit and backfill",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobMarketBillboardSync,
			Priority:     6,
			Schedule:     "0 30 21 * * *",
			Description:  "21:30 market billboard RPT sync",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDataLifecycleRestore,
			Priority:     7,
			Description:  "explicit cold segment restore and rehydrate operations",
			DefaultState: GovernanceRunStatusPlanned,
		},
		{
			Name:         GovernanceJobDataLifecycleMaintenance,
			Priority:     8,
			Description:  "bounded hot/cold lifecycle maintenance after higher-priority governance jobs",
			DefaultState: GovernanceRunStatusPlanned,
		},
	}
}

func GovernanceJobPriority(job GovernanceJob) int {
	for _, spec := range DefaultGovernanceJobCatalog() {
		if spec.Name == job {
			return spec.Priority
		}
	}
	return 100
}

func GovernanceWindowDependencyKey(job GovernanceJob, targetWindow string) string {
	switch job {
	case GovernanceJobDailyAudit:
		return GovernanceWindowKey(GovernanceJobDailyCloseSync, targetWindow)
	default:
		return ""
	}
}

func MapLegacyGovernanceJob(name string) (GovernanceJob, bool) {
	for _, spec := range DefaultGovernanceJobCatalog() {
		for _, legacyName := range spec.LegacyNames {
			if legacyName == name {
				return spec.Name, true
			}
		}
	}
	return "", false
}

type GovernanceSchemaVersion struct {
	ID        int64     `xorm:"pk autoincr"`
	Version   int       `xorm:"notnull"`
	AppliedAt time.Time `xorm:"notnull"`
}

func (*GovernanceSchemaVersion) TableName() string {
	return "governance_schema_version"
}

const GovernanceSchemaVersionCurrent = 1

type GovernanceRunRecord struct {
	ID           int64               `xorm:"pk autoincr" json:"id"`
	RunID        string              `xorm:"varchar(128) unique notnull" json:"run_id"`
	JobName      string              `xorm:"varchar(64) index notnull" json:"job_name"`
	Trigger      string              `xorm:"varchar(128)" json:"trigger,omitempty"`
	Status       GovernanceRunStatus `xorm:"varchar(32) index notnull" json:"status"`
	Reason       string              `xorm:"text" json:"reason,omitempty"`
	TargetWindow string              `xorm:"varchar(64)" json:"target_window,omitempty"`
	Details      string              `xorm:"text" json:"details,omitempty"`
	EvidenceID   string              `xorm:"varchar(128)" json:"evidence_id,omitempty"`
	StartedAt    time.Time           `xorm:"index notnull" json:"started_at"`
	EndedAt      time.Time           `json:"ended_at,omitempty"`
}

func (*GovernanceRunRecord) TableName() string {
	return "governance_run"
}

type GovernanceTaskRecord struct {
	ID           int64                `xorm:"pk autoincr" json:"id"`
	TaskKey      string               `xorm:"varchar(128) unique notnull" json:"task_key"`
	JobName      string               `xorm:"varchar(64) index notnull" json:"job_name"`
	Domain       string               `xorm:"varchar(64) index notnull" json:"domain"`
	Status       GovernanceTaskStatus `xorm:"varchar(32) index notnull" json:"status"`
	Priority     int                  `xorm:"notnull" json:"priority"`
	Attempts     int                  `json:"attempts"`
	Reason       string               `xorm:"text" json:"reason,omitempty"`
	TargetWindow string               `xorm:"varchar(64)" json:"target_window,omitempty"`
	PayloadJSON  string               `xorm:"text" json:"payload_json,omitempty"`
	CreatedAt    time.Time            `xorm:"created" json:"created_at"`
	UpdatedAt    time.Time            `xorm:"updated" json:"updated_at"`
}

func (*GovernanceTaskRecord) TableName() string {
	return "governance_task"
}

type GovernanceWindowRecord struct {
	ID            int64                  `xorm:"pk autoincr" json:"id"`
	WindowKey     string                 `xorm:"varchar(160) unique notnull" json:"window_key"`
	JobName       string                 `xorm:"varchar(64) index notnull" json:"job_name"`
	TargetWindow  string                 `xorm:"varchar(64) index notnull" json:"target_window"`
	DueAt         time.Time              `xorm:"index notnull" json:"due_at"`
	Priority      int                    `xorm:"index notnull" json:"priority"`
	Status        GovernanceWindowStatus `xorm:"varchar(32) index notnull" json:"status"`
	DependencyKey string                 `xorm:"varchar(160) index" json:"dependency_key,omitempty"`
	Attempts      int                    `json:"attempts"`
	NextRunAt     time.Time              `xorm:"index" json:"next_run_at,omitempty"`
	LeaseOwner    string                 `xorm:"varchar(128)" json:"lease_owner,omitempty"`
	LeaseUntil    time.Time              `xorm:"index" json:"lease_until,omitempty"`
	RunID         string                 `xorm:"varchar(128) index" json:"run_id,omitempty"`
	LastError     string                 `xorm:"text" json:"last_error,omitempty"`
	ResultSummary string                 `xorm:"text" json:"result_summary,omitempty"`
	ScheduledAt   time.Time              `xorm:"index" json:"scheduled_at,omitempty"`
	EnqueuedAt    time.Time              `xorm:"index" json:"enqueued_at,omitempty"`
	StartedAt     time.Time              `json:"started_at,omitempty"`
	EndedAt       time.Time              `json:"ended_at,omitempty"`
	CreatedAt     time.Time              `xorm:"created" json:"created_at"`
	UpdatedAt     time.Time              `xorm:"updated" json:"updated_at"`
}

func (*GovernanceWindowRecord) TableName() string {
	return "governance_window"
}

type DomainHealthSnapshotRecord struct {
	ID              int64     `xorm:"pk autoincr" json:"id"`
	Domain          string    `xorm:"varchar(64) unique notnull" json:"domain"`
	Status          string    `xorm:"varchar(32) index notnull" json:"status"`
	Freshness       string    `xorm:"varchar(32)" json:"freshness,omitempty"`
	Coverage        string    `xorm:"varchar(32)" json:"coverage,omitempty"`
	LatestCursor    string    `xorm:"varchar(128)" json:"latest_cursor,omitempty"`
	LatestWatermark string    `xorm:"varchar(128)" json:"latest_watermark,omitempty"`
	Summary         string    `xorm:"text" json:"summary,omitempty"`
	EvidenceJSON    string    `xorm:"text" json:"evidence_json,omitempty"`
	SnapshotAt      time.Time `xorm:"index notnull" json:"snapshot_at"`
	UpdatedAt       time.Time `xorm:"updated" json:"updated_at"`
}

func (*DomainHealthSnapshotRecord) TableName() string {
	return "governance_domain_health_snapshot"
}

type GovernanceLockMetadataRecord struct {
	ID              int64     `xorm:"pk autoincr" json:"id"`
	LockName        string    `xorm:"varchar(64) unique notnull" json:"lock_name"`
	HolderPID       int64     `json:"holder_pid"`
	HolderHostname  string    `xorm:"varchar(255)" json:"holder_hostname,omitempty"`
	HolderJobName   string    `xorm:"varchar(64)" json:"holder_job_name,omitempty"`
	HolderRunID     string    `xorm:"varchar(128)" json:"holder_run_id,omitempty"`
	AcquiredAt      time.Time `json:"acquired_at,omitempty"`
	LastHeartbeatAt time.Time `xorm:"index" json:"last_heartbeat_at,omitempty"`
	Active          bool      `xorm:"-" json:"active"`
	State           string    `xorm:"-" json:"state,omitempty"`
	UpdatedAt       time.Time `xorm:"updated" json:"updated_at"`
}

func (*GovernanceLockMetadataRecord) TableName() string {
	return "governance_lock_metadata"
}

type GovernanceEvidenceRecord struct {
	ID         int64     `xorm:"pk autoincr" json:"id"`
	EvidenceID string    `xorm:"varchar(128) index notnull" json:"evidence_id"`
	RunID      string    `xorm:"varchar(128) index notnull" json:"run_id"`
	Kind       string    `xorm:"varchar(64) notnull" json:"kind"`
	Path       string    `xorm:"text" json:"path,omitempty"`
	Summary    string    `xorm:"text" json:"summary,omitempty"`
	CreatedAt  time.Time `xorm:"index notnull" json:"created_at"`
}

func (*GovernanceEvidenceRecord) TableName() string {
	return "governance_evidence"
}
