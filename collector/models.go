package collector

import "time"

const SchemaVersionCurrent = 1

type SchemaVersion struct {
	ID        int64     `xorm:"pk autoincr"`
	Version   int       `xorm:"notnull"`
	AppliedAt time.Time `xorm:"notnull"`
}

func (*SchemaVersion) TableName() string {
	return "collector_schema_version"
}

type PhaseStateRecord struct {
	ID                 int64     `xorm:"pk autoincr"`
	PhaseID            string    `xorm:"varchar(64) unique notnull"`
	PhaseName          string    `xorm:"varchar(128) notnull"`
	Status             string    `xorm:"varchar(32) notnull"`
	CurrentTaskID      string    `xorm:"varchar(128)"`
	CurrentTask        string    `xorm:"text"`
	NextPhaseID        string    `xorm:"varchar(64)"`
	NextPhaseName      string    `xorm:"varchar(128)"`
	AllowedToCommit    bool      `xorm:"notnull"`
	AllowedToAdvance   bool      `xorm:"notnull"`
	LastVerifiedCommit string    `xorm:"varchar(64)"`
	LastVerifiedAt     time.Time `xorm:"null"`
	LastTestSuiteJSON  string    `xorm:"text"`
	BlockingIssuesJSON string    `xorm:"text"`
	UpdatedAt          time.Time `xorm:"notnull updated"`
}

func (*PhaseStateRecord) TableName() string {
	return "collector_phase_state"
}

type TaskRunRecord struct {
	ID        int64     `xorm:"pk autoincr"`
	RunID     string    `xorm:"varchar(64) unique notnull"`
	PhaseID   string    `xorm:"varchar(64) index notnull"`
	TaskID    string    `xorm:"varchar(128) index notnull"`
	TaskType  string    `xorm:"varchar(64) notnull"`
	Status    string    `xorm:"varchar(32) notnull"`
	Summary   string    `xorm:"text"`
	ErrorText string    `xorm:"text"`
	StartedAt time.Time `xorm:"notnull"`
	EndedAt   time.Time `xorm:"null"`
}

func (*TaskRunRecord) TableName() string {
	return "collector_task_run"
}

type ValidationRunRecord struct {
	ID          int64     `xorm:"pk autoincr"`
	RunID       string    `xorm:"varchar(64) unique notnull"`
	PhaseID     string    `xorm:"varchar(64) index notnull"`
	SuiteName   string    `xorm:"varchar(128) notnull"`
	Status      string    `xorm:"varchar(32) notnull"`
	Blocking    bool      `xorm:"notnull"`
	CommandText string    `xorm:"text"`
	OutputText  string    `xorm:"text"`
	CreatedAt   time.Time `xorm:"notnull"`
}

func (*ValidationRunRecord) TableName() string {
	return "collector_validation_run"
}

type OperationLogRecord struct {
	ID           int64     `xorm:"pk autoincr"`
	PhaseID      string    `xorm:"varchar(64) index notnull"`
	SessionID    string    `xorm:"varchar(64) index"`
	Summary      string    `xorm:"text"`
	CommandsJSON string    `xorm:"text"`
	ResultsJSON  string    `xorm:"text"`
	FilesJSON    string    `xorm:"text"`
	CommitSHA    string    `xorm:"varchar(64)"`
	CreatedAt    time.Time `xorm:"notnull"`
}

func (*OperationLogRecord) TableName() string {
	return "collector_operation_log"
}

type CollectCursorRecord struct {
	ID         int64     `xorm:"pk autoincr"`
	Domain     string    `xorm:"varchar(64) index notnull"`
	AssetType  string    `xorm:"varchar(64) index notnull"`
	Instrument string    `xorm:"varchar(64) index notnull"`
	Period     string    `xorm:"varchar(32) index"`
	Cursor     string    `xorm:"text"`
	UpdatedAt  time.Time `xorm:"notnull updated"`
}

func (*CollectCursorRecord) TableName() string {
	return "collector_cursor"
}

type CollectGapRecord struct {
	ID         int64     `xorm:"pk autoincr"`
	Domain     string    `xorm:"varchar(64) index notnull"`
	AssetType  string    `xorm:"varchar(64) index notnull"`
	Instrument string    `xorm:"varchar(64) index notnull"`
	Period     string    `xorm:"varchar(32) index"`
	StartKey   string    `xorm:"varchar(64) notnull"`
	EndKey     string    `xorm:"varchar(64) notnull"`
	Status     string    `xorm:"varchar(32) notnull"`
	Reason     string    `xorm:"text"`
	CreatedAt  time.Time `xorm:"notnull"`
	UpdatedAt  time.Time `xorm:"notnull updated"`
}

func (*CollectGapRecord) TableName() string {
	return "collector_gap"
}

type ScheduleRunRecord struct {
	ID           int64     `xorm:"pk autoincr"`
	ScheduleName string    `xorm:"varchar(128) index notnull"`
	Status       string    `xorm:"varchar(32) notnull"`
	StartedAt    time.Time `xorm:"notnull"`
	EndedAt      time.Time `xorm:"null"`
	Details      string    `xorm:"text"`
}

func (*ScheduleRunRecord) TableName() string {
	return "collector_schedule_run"
}
