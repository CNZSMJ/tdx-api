package lifecycle

import "time"

type ColdSegment struct {
	SegmentID            string        `json:"segment_id"`
	DatasetID            string        `json:"dataset_id"`
	Domain               string        `json:"domain"`
	TableName            string        `json:"table_name"`
	Instrument           string        `json:"instrument,omitempty"`
	PartitionKey         string        `json:"partition_key"`
	StartDate            string        `json:"start_date"`
	EndDate              string        `json:"end_date"`
	TradingDayCount      int           `json:"trading_day_count"`
	RowCount             int64         `json:"row_count"`
	ByteSize             int64         `json:"byte_size"`
	FileChecksum         string        `json:"file_checksum,omitempty"`
	LogicalChecksum      string        `json:"logical_checksum,omitempty"`
	SchemaVersion        int           `json:"schema_version"`
	StorageScheme        string        `json:"storage_scheme"`
	FinalizationStrategy string        `json:"finalization_strategy"`
	ColdURI              string        `json:"cold_uri"`
	ArchiveBatchID       string        `json:"archive_batch_id"`
	SourceDBPath         string        `json:"source_db_path,omitempty"`
	SourceDBSize         int64         `json:"source_db_size"`
	SourceSelectionHash  string        `json:"source_selection_hash,omitempty"`
	Status               SegmentStatus `json:"status"`
	ErrorCode            string        `json:"error_code,omitempty"`
	ErrorMessage         string        `json:"error_message,omitempty"`
	CreatedAt            time.Time     `json:"created_at"`
	ExportingAt          time.Time     `json:"exporting_at,omitempty"`
	ExportedAt           time.Time     `json:"exported_at,omitempty"`
	VerifiedAt           time.Time     `json:"verified_at,omitempty"`
	RestoreTestedAt      time.Time     `json:"restore_tested_at,omitempty"`
	PruningAt            time.Time     `json:"pruning_at,omitempty"`
	HotPrunedAt          time.Time     `json:"hot_pruned_at,omitempty"`
	ActiveAt             time.Time     `json:"active_at,omitempty"`
	UpdatedAt            time.Time     `json:"updated_at"`
}

type LifecycleBatch struct {
	BatchID   string
	Domain    string
	Status    string
	StartedAt time.Time
	EndedAt   time.Time
}

type HotRetentionPolicy struct {
	Domain                  string
	TableName               string
	HotRetentionTradingDays int
	ColdRetentionYears      int
	UpdatedAt               time.Time
}

type LifecycleWatermark struct {
	Domain    string
	TableName string
	Watermark string
	UpdatedAt time.Time
}

type LifecycleRestore struct {
	RestoreID              string
	SegmentID              string
	RestoreMode            string
	Status                 string
	TargetPath             string
	ExpiresAt              time.Time
	RetentionOverrideUntil time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}
