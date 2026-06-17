package lifecycle

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

type ManifestStore struct {
	db *sql.DB
}

func OpenManifestStore(path string) (*ManifestStore, error) {
	if path == "" {
		return nil, errors.New("manifest db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &ManifestStore{db: db}
	if err := store.EnsureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *ManifestStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func RequiredManifestIndexes() []string {
	return []string{
		"idx_cold_segment_domain_instrument_date",
		"idx_cold_segment_domain_table_date",
		"idx_cold_segment_batch",
		"idx_cold_segment_status_updated",
		"idx_lifecycle_batch_status_started",
	}
}

func (s *ManifestStore) EnsureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cold_segment (
			segment_id TEXT PRIMARY KEY,
			dataset_id TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL,
			table_name TEXT NOT NULL,
			instrument TEXT,
			partition_key TEXT NOT NULL DEFAULT '',
			start_date TEXT NOT NULL,
			end_date TEXT NOT NULL,
			trading_day_count INTEGER NOT NULL DEFAULT 0,
			row_count INTEGER NOT NULL DEFAULT 0,
			byte_size INTEGER NOT NULL DEFAULT 0,
			file_checksum TEXT,
			logical_checksum TEXT,
			schema_version INTEGER NOT NULL,
			storage_scheme TEXT NOT NULL DEFAULT 'local-v1',
			finalization_strategy TEXT NOT NULL DEFAULT 'atomic_rename_same_device',
			cold_uri TEXT NOT NULL,
			archive_batch_id TEXT NOT NULL DEFAULT '',
			source_db_path TEXT,
			source_db_size INTEGER NOT NULL DEFAULT 0,
			source_selection_hash TEXT,
			status TEXT NOT NULL,
			error_code TEXT,
			error_message TEXT,
			created_at TEXT NOT NULL,
			exporting_at TEXT,
			exported_at TEXT,
			verified_at TEXT,
			restore_tested_at TEXT,
			pruning_at TEXT,
			hot_pruned_at TEXT,
			active_at TEXT,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS lifecycle_batch (
			batch_id TEXT PRIMARY KEY,
			domain TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS hot_retention_policy (
			domain TEXT NOT NULL,
			table_name TEXT NOT NULL,
			hot_retention_trading_days INTEGER NOT NULL,
			cold_retention_years INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(domain, table_name)
		)`,
		`CREATE TABLE IF NOT EXISTS lifecycle_watermark (
			domain TEXT NOT NULL,
			table_name TEXT NOT NULL,
			watermark TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(domain, table_name)
		)`,
		`CREATE TABLE IF NOT EXISTS lifecycle_restore (
			restore_id TEXT PRIMARY KEY,
			segment_id TEXT NOT NULL,
			restore_mode TEXT NOT NULL,
			status TEXT NOT NULL,
			target_path TEXT,
			expires_at TEXT,
			retention_override_until TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColdSegmentColumns(); err != nil {
		return err
	}
	indexStmts := []string{
		`DROP INDEX IF EXISTS idx_cold_segment_batch`,
		`CREATE INDEX IF NOT EXISTS idx_cold_segment_domain_instrument_date ON cold_segment(domain, instrument, start_date, end_date)`,
		`CREATE INDEX IF NOT EXISTS idx_cold_segment_domain_table_date ON cold_segment(domain, table_name, start_date, end_date)`,
		`CREATE INDEX IF NOT EXISTS idx_cold_segment_batch ON cold_segment(archive_batch_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cold_segment_status_updated ON cold_segment(status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_lifecycle_batch_status_started ON lifecycle_batch(status, started_at)`,
	}
	for _, stmt := range indexStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.ensureDefaultRetentionPolicies()
}

func (s *ManifestStore) ensureColdSegmentColumns() error {
	columns, err := s.tableColumns("cold_segment")
	if err != nil {
		return err
	}
	required := []struct {
		name string
		ddl  string
	}{
		{"dataset_id", "dataset_id TEXT NOT NULL DEFAULT ''"},
		{"partition_key", "partition_key TEXT NOT NULL DEFAULT ''"},
		{"trading_day_count", "trading_day_count INTEGER NOT NULL DEFAULT 0"},
		{"byte_size", "byte_size INTEGER NOT NULL DEFAULT 0"},
		{"storage_scheme", "storage_scheme TEXT NOT NULL DEFAULT 'local-v1'"},
		{"finalization_strategy", "finalization_strategy TEXT NOT NULL DEFAULT 'atomic_rename_same_device'"},
		{"archive_batch_id", "archive_batch_id TEXT NOT NULL DEFAULT ''"},
		{"source_db_path", "source_db_path TEXT"},
		{"source_db_size", "source_db_size INTEGER NOT NULL DEFAULT 0"},
		{"source_selection_hash", "source_selection_hash TEXT"},
		{"error_code", "error_code TEXT"},
		{"error_message", "error_message TEXT"},
		{"exporting_at", "exporting_at TEXT"},
		{"exported_at", "exported_at TEXT"},
		{"verified_at", "verified_at TEXT"},
		{"restore_tested_at", "restore_tested_at TEXT"},
		{"pruning_at", "pruning_at TEXT"},
		{"hot_pruned_at", "hot_pruned_at TEXT"},
		{"active_at", "active_at TEXT"},
	}
	for _, column := range required {
		if columns[column.name] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE cold_segment ADD COLUMN ` + column.ddl); err != nil {
			return err
		}
	}
	if columns["batch_id"] {
		if _, err := s.db.Exec(`UPDATE cold_segment SET archive_batch_id = batch_id WHERE archive_batch_id = ''`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_cold_segment_batch`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`ALTER TABLE cold_segment DROP COLUMN batch_id`); err != nil {
			return err
		}
	}
	return nil
}

func (s *ManifestStore) ensureDefaultRetentionPolicies() error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	policies := [][2]string{
		{"trade", "TradeHistory"},
		{"trade", "TradeMinute1Bar"},
		{"trade", "TradeMinute5Bar"},
		{"trade", "TradeMinute15Bar"},
		{"trade", "TradeMinute30Bar"},
		{"trade", "TradeMinute60Bar"},
		{"live", "TradeLive"},
		{"live", "MinuteLive"},
		{"live", "QuoteSnapshot"},
		{"order_history", "OrderHistory"},
		{"auction", "AuctionSnapshot"},
	}
	for _, policy := range policies {
		if _, err := s.db.Exec(`INSERT INTO hot_retention_policy(domain, table_name, hot_retention_trading_days, cold_retention_years, updated_at) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(domain, table_name) DO UPDATE SET hot_retention_trading_days=excluded.hot_retention_trading_days, cold_retention_years=excluded.cold_retention_years, updated_at=excluded.updated_at`,
			policy[0], policy[1], DefaultHotRetentionTradingDays, 7, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *ManifestStore) HasTable(table string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}

func (s *ManifestStore) HasIndex(index string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count)
	return count > 0, err
}

func (s *ManifestStore) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (s *ManifestStore) GetRetentionPolicy(domain, table string) (*HotRetentionPolicy, error) {
	row := s.db.QueryRow(`SELECT domain, table_name, hot_retention_trading_days, cold_retention_years, updated_at FROM hot_retention_policy WHERE domain=? AND table_name=?`, domain, table)
	var policy HotRetentionPolicy
	var updated string
	if err := row.Scan(&policy.Domain, &policy.TableName, &policy.HotRetentionTradingDays, &policy.ColdRetentionYears, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	policy.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &policy, nil
}

const coldSegmentSelectColumns = `segment_id, dataset_id, domain, table_name, COALESCE(instrument, ''), partition_key, start_date, end_date, trading_day_count, row_count, byte_size, COALESCE(file_checksum, ''), COALESCE(logical_checksum, ''), schema_version, storage_scheme, finalization_strategy, cold_uri, archive_batch_id, COALESCE(source_db_path, ''), source_db_size, COALESCE(source_selection_hash, ''), status, COALESCE(error_code, ''), COALESCE(error_message, ''), COALESCE(created_at, ''), COALESCE(exporting_at, ''), COALESCE(exported_at, ''), COALESCE(verified_at, ''), COALESCE(restore_tested_at, ''), COALESCE(pruning_at, ''), COALESCE(hot_pruned_at, ''), COALESCE(active_at, ''), COALESCE(updated_at, '')`

func (s *ManifestStore) CreateSegment(segment ColdSegment) error {
	now := time.Now().UTC()
	if segment.CreatedAt.IsZero() {
		segment.CreatedAt = now
	}
	if segment.UpdatedAt.IsZero() {
		segment.UpdatedAt = now
	}
	if segment.Status == "" {
		segment.Status = SegmentPlanned
	}
	if segment.DatasetID == "" {
		segment.DatasetID = defaultLifecycleDataset
		if parsed, err := ParseColdURI(segment.ColdURI); err == nil {
			segment.DatasetID = parsed.Dataset
		}
	}
	if segment.PartitionKey == "" {
		segment.PartitionKey = partitionKeyFromColdURI(segment.ColdURI)
	}
	if segment.StorageScheme == "" {
		segment.StorageScheme = "local-v1"
	}
	if segment.FinalizationStrategy == "" {
		segment.FinalizationStrategy = "atomic_rename_same_device"
	}
	_, err := s.db.Exec(`INSERT INTO cold_segment(segment_id, dataset_id, domain, table_name, instrument, partition_key, start_date, end_date, trading_day_count, row_count, byte_size, file_checksum, logical_checksum, schema_version, storage_scheme, finalization_strategy, cold_uri, archive_batch_id, source_db_path, source_db_size, source_selection_hash, status, error_code, error_message, created_at, exporting_at, exported_at, verified_at, restore_tested_at, pruning_at, hot_pruned_at, active_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		segment.SegmentID, segment.DatasetID, segment.Domain, segment.TableName, segment.Instrument, segment.PartitionKey, segment.StartDate, segment.EndDate, segment.TradingDayCount, segment.RowCount, segment.ByteSize, segment.FileChecksum, segment.LogicalChecksum, segment.SchemaVersion, segment.StorageScheme, segment.FinalizationStrategy, segment.ColdURI, segment.ArchiveBatchID, segment.SourceDBPath, segment.SourceDBSize, segment.SourceSelectionHash, string(segment.Status), segment.ErrorCode, segment.ErrorMessage, formatTime(segment.CreatedAt), formatTime(segment.ExportingAt), formatTime(segment.ExportedAt), formatTime(segment.VerifiedAt), formatTime(segment.RestoreTestedAt), formatTime(segment.PruningAt), formatTime(segment.HotPrunedAt), formatTime(segment.ActiveAt), formatTime(segment.UpdatedAt))
	return err
}

func (s *ManifestStore) GetSegment(segmentID string) (*ColdSegment, error) {
	row := s.db.QueryRow(`SELECT `+coldSegmentSelectColumns+` FROM cold_segment WHERE segment_id=?`, segmentID)
	segment, err := scanSegment(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return segment, nil
}

func (s *ManifestStore) UpdateSegmentStatus(segmentID string, next SegmentStatus, reason string) error {
	segment, err := s.GetSegment(segmentID)
	if err != nil {
		return err
	}
	if segment == nil {
		return fmt.Errorf("segment not found: %s", segmentID)
	}
	if err := ValidateSegmentTransition(segment.Status, next); err != nil {
		return err
	}
	now := time.Now().UTC()
	assignments := []string{"status=?", "updated_at=?"}
	args := []any{string(next), formatTime(now)}
	switch next {
	case SegmentExporting:
		assignments = append(assignments, "exporting_at=?")
		args = append(args, formatTime(now))
	case SegmentExported:
		assignments = append(assignments, "exported_at=?")
		args = append(args, formatTime(now))
	case SegmentVerified:
		assignments = append(assignments, "verified_at=?")
		args = append(args, formatTime(now))
	case SegmentRestoreTested:
		assignments = append(assignments, "restore_tested_at=?")
		args = append(args, formatTime(now))
	case SegmentPruning:
		assignments = append(assignments, "pruning_at=?")
		args = append(args, formatTime(now))
	case SegmentHotPruned:
		assignments = append(assignments, "hot_pruned_at=?")
		args = append(args, formatTime(now))
	case SegmentActive:
		assignments = append(assignments, "active_at=?")
		args = append(args, formatTime(now))
	case SegmentFailed:
		assignments = append(assignments, "error_code=?", "error_message=?")
		args = append(args, "LIFECYCLE_SEGMENT_FAILED", reason)
	}
	args = append(args, segmentID)
	_, err = s.db.Exec(`UPDATE cold_segment SET `+strings.Join(assignments, ", ")+` WHERE segment_id=?`, args...)
	return err
}

func (s *ManifestStore) UpdateSegmentArchiveMetadata(segmentID string, result SegmentExportResult) error {
	_, err := s.db.Exec(`UPDATE cold_segment SET row_count=?, byte_size=?, file_checksum=?, logical_checksum=?, schema_version=?, start_date=?, end_date=?, cold_uri=?, updated_at=? WHERE segment_id=?`,
		result.RowCount, result.ByteSize, result.FileChecksum, result.LogicalChecksum, result.SchemaVersion, result.MinDate, result.MaxDate, result.ColdURI, formatTime(time.Now().UTC()), segmentID)
	return err
}

func (s *ManifestStore) CreateRestore(record LifecycleRestore) error {
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	_, err := s.db.Exec(`INSERT INTO lifecycle_restore(restore_id, segment_id, restore_mode, status, target_path, expires_at, retention_override_until, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RestoreID, record.SegmentID, record.RestoreMode, record.Status, record.TargetPath, formatTime(record.ExpiresAt), formatTime(record.RetentionOverrideUntil), formatTime(record.CreatedAt), formatTime(record.UpdatedAt))
	return err
}

func (s *ManifestStore) CountSegmentsByStatus(statuses ...SegmentStatus) (int, error) {
	if len(statuses) == 0 {
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM cold_segment`).Scan(&count)
		return count, err
	}
	placeholders := make([]string, 0, len(statuses))
	args := make([]any, 0, len(statuses))
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, string(status))
	}
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM cold_segment WHERE status IN (`+strings.Join(placeholders, ",")+`)`, args...).Scan(&count)
	return count, err
}

func (s *ManifestStore) ListSegments() ([]ColdSegment, error) {
	rows, err := s.db.Query(`SELECT ` + coldSegmentSelectColumns + ` FROM cold_segment ORDER BY updated_at DESC, segment_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ColdSegment, 0)
	for rows.Next() {
		segment, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *segment)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSegment(row rowScanner) (*ColdSegment, error) {
	var segment ColdSegment
	var status, created, exporting, exported, verified, restoreTested, pruning, hotPruned, active, updated string
	err := row.Scan(&segment.SegmentID, &segment.DatasetID, &segment.Domain, &segment.TableName, &segment.Instrument, &segment.PartitionKey, &segment.StartDate, &segment.EndDate, &segment.TradingDayCount, &segment.RowCount, &segment.ByteSize, &segment.FileChecksum, &segment.LogicalChecksum, &segment.SchemaVersion, &segment.StorageScheme, &segment.FinalizationStrategy, &segment.ColdURI, &segment.ArchiveBatchID, &segment.SourceDBPath, &segment.SourceDBSize, &segment.SourceSelectionHash, &status, &segment.ErrorCode, &segment.ErrorMessage, &created, &exporting, &exported, &verified, &restoreTested, &pruning, &hotPruned, &active, &updated)
	if err != nil {
		return nil, err
	}
	segment.Status = SegmentStatus(status)
	segment.CreatedAt = parseManifestTime(created)
	segment.ExportingAt = parseManifestTime(exporting)
	segment.ExportedAt = parseManifestTime(exported)
	segment.VerifiedAt = parseManifestTime(verified)
	segment.RestoreTestedAt = parseManifestTime(restoreTested)
	segment.PruningAt = parseManifestTime(pruning)
	segment.HotPrunedAt = parseManifestTime(hotPruned)
	segment.ActiveAt = parseManifestTime(active)
	segment.UpdatedAt = parseManifestTime(updated)
	return &segment, nil
}

func parseManifestTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func partitionKeyFromColdURI(raw string) string {
	parsed, err := ParseColdURI(raw)
	if err != nil {
		return ""
	}
	dir := path.Dir(parsed.Path)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.TrimPrefix(dir, "cold/")
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
