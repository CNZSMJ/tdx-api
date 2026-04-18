package profinance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

type schemaMigration struct {
	Version int
	Name    string
	SQL     string
}

type sourceFileState struct {
	SourceFileID int64
	ParseStatus  string
}

type rebuildReportVersionMetadata struct {
	FirstSeenAt string
	LastSeenAt  string
	IngestedAt  string
}

type rebuildWatermarkState struct {
	ManifestFetchedAt string
	LatestSeen        string
	WatermarkDate     string
	Found             bool
}

var profFinanceMigrations = []schemaMigration{
	{
		Version: 1,
		Name:    "professional_finance_core_tables",
		SQL: `
CREATE TABLE IF NOT EXISTS prof_finance_schema_migration (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL,
	checksum TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS prof_finance_source_file (
	source_file_id INTEGER PRIMARY KEY AUTOINCREMENT,
	source_name TEXT NOT NULL,
	filename TEXT NOT NULL,
	report_date TEXT NOT NULL,
	remote_hash TEXT,
	remote_filesize INTEGER,
	stored_path TEXT NOT NULL,
	manifest_seen_at TEXT NOT NULL,
	downloaded_at TEXT NOT NULL,
	validated_at TEXT NOT NULL,
	parse_status TEXT NOT NULL,
	parse_error TEXT,
	supersedes_source_file_id INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prof_finance_source_file_filename_hash ON prof_finance_source_file(filename, remote_hash);

CREATE TABLE IF NOT EXISTS prof_finance_source_report (
	source_report_id INTEGER PRIMARY KEY AUTOINCREMENT,
	source_file_id INTEGER NOT NULL,
	report_date TEXT NOT NULL,
	field_count INTEGER NOT NULL,
	row_count INTEGER NOT NULL,
	parsed_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prof_finance_source_report_source_file ON prof_finance_source_report(source_file_id);

CREATE TABLE IF NOT EXISTS prof_finance_source_value_raw (
	source_value_id INTEGER PRIMARY KEY AUTOINCREMENT,
	source_file_id INTEGER NOT NULL,
	full_code TEXT NOT NULL,
	report_date TEXT NOT NULL,
	source_field_id INTEGER NOT NULL,
	raw_numeric_value REAL,
	raw_text_value TEXT,
	raw_value_type TEXT NOT NULL,
	parsed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prof_finance_source_value_full_code ON prof_finance_source_value_raw(full_code, report_date, source_field_id, source_file_id);
CREATE INDEX IF NOT EXISTS idx_prof_finance_source_value_report_date ON prof_finance_source_value_raw(report_date, source_field_id);
CREATE INDEX IF NOT EXISTS idx_prof_finance_source_value_source_file ON prof_finance_source_value_raw(source_file_id, full_code);

CREATE TABLE IF NOT EXISTS prof_finance_field_catalog (
	field_code TEXT PRIMARY KEY,
	source_field_id INTEGER NOT NULL UNIQUE,
	concept_code TEXT NOT NULL,
	field_name_cn TEXT NOT NULL,
	field_name_en TEXT NOT NULL,
	category TEXT NOT NULL,
	statement TEXT NOT NULL,
	period_semantics TEXT NOT NULL,
	unit TEXT NOT NULL,
	value_type TEXT NOT NULL,
	storage_precision TEXT,
	display_precision INTEGER,
	rounding_mode TEXT NOT NULL,
	nullable INTEGER NOT NULL,
	source TEXT NOT NULL,
	supported INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prof_finance_report_version (
	report_version_id INTEGER PRIMARY KEY AUTOINCREMENT,
	full_code TEXT NOT NULL,
	report_date TEXT NOT NULL,
	source_file_id INTEGER NOT NULL,
	version_hash TEXT NOT NULL,
	announce_date_raw TEXT,
	effective_announce_date TEXT NOT NULL,
	announce_date_source TEXT NOT NULL,
	preview_announce_date_raw TEXT,
	flash_report_announce_date_raw TEXT,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	ingested_at TEXT NOT NULL,
	is_latest_corrected INTEGER NOT NULL,
	supersedes_report_version_id INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prof_finance_report_version_source_file_code ON prof_finance_report_version(source_file_id, full_code);
CREATE INDEX IF NOT EXISTS idx_prof_finance_report_version_code_date ON prof_finance_report_version(full_code, report_date, effective_announce_date, report_version_id);

CREATE TABLE IF NOT EXISTS prof_finance_report_payload (
	report_version_id INTEGER PRIMARY KEY,
	full_code TEXT NOT NULL,
	report_date TEXT NOT NULL,
	field_values TEXT NOT NULL,
	missing_field_codes TEXT NOT NULL,
	supported_field_count INTEGER NOT NULL,
	available_field_count INTEGER NOT NULL,
	payload_hash TEXT NOT NULL,
	materialized_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prof_finance_report_payload_code_date ON prof_finance_report_payload(full_code, report_date, report_version_id);

CREATE TABLE IF NOT EXISTS prof_finance_source_watermark (
	source_name TEXT PRIMARY KEY,
	manifest_fetched_at TEXT NOT NULL,
	latest_report_date_seen TEXT NOT NULL,
	latest_report_date_ingested TEXT NOT NULL,
	watermark_date TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`,
	},
}

func (s *Service) openDB() (*sql.DB, error) {
	s.dbOnce.Do(func() {
		if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
			s.dbErr = err
			return
		}
		db, err := sql.Open("sqlite", s.dbPath)
		if err != nil {
			s.dbErr = err
			return
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			_ = db.Close()
			s.dbErr = err
			return
		}
		if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
			_ = db.Close()
			s.dbErr = err
			return
		}
		if err := applySchemaMigrations(db); err != nil {
			_ = db.Close()
			s.dbErr = err
			return
		}
		if err := verifySQLiteJSONSupport(db); err != nil {
			_ = db.Close()
			s.dbErr = err
			return
		}
		if err := syncFieldCatalog(db, s.registry); err != nil {
			_ = db.Close()
			s.dbErr = err
			return
		}
		s.db = db
	})
	return s.db, s.dbErr
}

func applySchemaMigrations(db *sql.DB) error {
	for _, migration := range profFinanceMigrations {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM prof_finance_schema_migration WHERE version = ?`, migration.Version).Scan(&count); err == nil && count > 0 {
			continue
		}
		if _, err := db.Exec(migration.SQL); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		checksum := checksumString(migration.SQL)
		if _, err := db.Exec(`INSERT OR REPLACE INTO prof_finance_schema_migration(version, name, applied_at, checksum) VALUES(?, ?, ?, ?)`,
			migration.Version, migration.Name, time.Now().UTC().Format(time.RFC3339Nano), checksum); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
	}
	return nil
}

func verifySQLiteJSONSupport(db *sql.DB) error {
	var valid int
	if err := db.QueryRow(`SELECT json_valid('{}')`).Scan(&valid); err != nil {
		return fmt.Errorf("sqlite json extension unavailable: %w", err)
	}
	if valid != 1 {
		return fmt.Errorf("sqlite json extension validation returned %d", valid)
	}
	return nil
}

func syncFieldCatalog(db *sql.DB, registry *Registry) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM prof_finance_field_catalog`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
INSERT INTO prof_finance_field_catalog(
	field_code, source_field_id, concept_code, field_name_cn, field_name_en, category, statement,
	period_semantics, unit, value_type, storage_precision, display_precision, rounding_mode,
	nullable, source, supported
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, field := range registry.All() {
		var storagePrecision any
		if field.StoragePrecision != nil {
			bs, err := json.Marshal(field.StoragePrecision)
			if err != nil {
				return err
			}
			storagePrecision = string(bs)
		}
		var displayPrecision any
		if field.DisplayPrecision != nil {
			displayPrecision = *field.DisplayPrecision
		}
		if _, err := stmt.Exec(
			field.FieldCode,
			field.SourceFieldID,
			field.ConceptCode,
			field.FieldNameCN,
			field.FieldNameEN,
			field.Category,
			field.Statement,
			field.PeriodSemantics,
			field.Unit,
			field.ValueType,
			storagePrecision,
			displayPrecision,
			field.RoundingMode,
			boolToInt(field.Nullable),
			field.Source,
			boolToInt(field.Supported),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Service) Sync(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	db, err := s.openDB()
	if err != nil {
		return err
	}

	reports, err := s.listReports(ctx, true)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	nowDate := now.Format("20060102")
	latestSeen := ""
	latestIngested := ""

	for _, report := range reports {
		if report.ReportDate > latestSeen {
			latestSeen = report.ReportDate
		}
		if report.ReportDate == "" || report.ReportDate > nowDate {
			continue
		}
		if err := s.ingestReport(ctx, db, report, now, nowText); err != nil {
			return err
		}
		if report.ReportDate > latestIngested {
			latestIngested = report.ReportDate
		}
	}

	if err := upsertSourceWatermark(ctx, db, nowText, latestSeen, latestIngested, nowDate, nowText); err != nil {
		return err
	}

	s.mu.Lock()
	s.codeCache = make(map[string]Snapshot)
	s.mu.Unlock()
	return nil
}

func (s *Service) Rebuild(ctx context.Context) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	db, err := s.openDB()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	versionMetadata, err := loadRebuildVersionMetadata(ctx, tx)
	if err != nil {
		return err
	}
	watermarkState, err := loadRebuildWatermarkState(ctx, tx)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM prof_finance_report_payload`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM prof_finance_report_version`); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `
SELECT source_file_id, filename, report_date, manifest_seen_at
FROM prof_finance_source_file
WHERE parse_status = 'success'
ORDER BY source_file_id ASC`)
	if err != nil {
		return err
	}
	type rebuildSourceFile struct {
		SourceFileID int64
		Filename     string
		ReportDate   string
		ManifestSeen string
	}
	var sourceFiles []rebuildSourceFile
	for rows.Next() {
		var sourceFile rebuildSourceFile
		if err := rows.Scan(&sourceFile.SourceFileID, &sourceFile.Filename, &sourceFile.ReportDate, &sourceFile.ManifestSeen); err != nil {
			rows.Close()
			return err
		}
		sourceFiles = append(sourceFiles, sourceFile)
	}
	rows.Close()

	now := s.now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	latestIngested := ""
	latestSeen := watermarkState.LatestSeen
	manifestFetchedAt := watermarkState.ManifestFetchedAt
	watermarkDate := watermarkState.WatermarkDate
	if !watermarkState.Found {
		for _, sourceFile := range sourceFiles {
			if sourceFile.ReportDate > latestSeen {
				latestSeen = sourceFile.ReportDate
			}
			if sourceFile.ManifestSeen > manifestFetchedAt {
				manifestFetchedAt = sourceFile.ManifestSeen
			}
		}
		if watermarkDate == "" {
			watermarkDate = timestampToDate(manifestFetchedAt)
		}
	}
	for _, sourceFile := range sourceFiles {
		parsed, err := rebuildParsedReport(ctx, tx, sourceFile.SourceFileID, sourceFile.ReportDate)
		if err != nil {
			return err
		}
		if err := materializeReportPayloads(
			tx,
			s.registry,
			sourceFile.SourceFileID,
			sourceFile.Filename,
			parsed,
			versionMetadata[sourceFile.SourceFileID],
			sourceFile.ManifestSeen,
			true,
			now,
			nowText,
		); err != nil {
			return err
		}
		if sourceFile.ReportDate > latestIngested {
			latestIngested = sourceFile.ReportDate
		}
	}

	if latestSeen == "" {
		latestSeen = latestIngested
	}
	if manifestFetchedAt == "" {
		manifestFetchedAt = nowText
	}
	if watermarkDate == "" {
		watermarkDate = timestampToDate(manifestFetchedAt)
	}
	if watermarkDate == "" {
		watermarkDate = now.Format("20060102")
	}
	if err := upsertSourceWatermark(ctx, tx, manifestFetchedAt, latestSeen, latestIngested, watermarkDate, nowText); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) ingestReport(ctx context.Context, db *sql.DB, report ReportFile, now time.Time, nowText string) error {
	bs, err := s.ensureReportZip(ctx, report)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sourceFile, err := upsertSourceFile(ctx, tx, report, filepath.Join(s.zipDir, report.Filename), nowText)
	if err != nil {
		return err
	}
	if sourceFile.ParseStatus == "success" {
		return tx.Commit()
	}

	parsed, err := parseZipReportRaw(bs, report, s.registry)
	if err != nil {
		_ = markSourceFileParseFailure(tx, sourceFile.SourceFileID, err.Error())
		return err
	}

	if err := clearSourceFileMaterialization(tx, sourceFile.SourceFileID); err != nil {
		return err
	}
	if err := insertSourceReport(tx, sourceFile.SourceFileID, parsed, nowText); err != nil {
		return err
	}
	if err := insertRawValues(tx, sourceFile.SourceFileID, parsed, nowText); err != nil {
		return err
	}
	if err := materializeReportPayloads(tx, s.registry, sourceFile.SourceFileID, report.Filename, parsed, nil, nowText, false, now, nowText); err != nil {
		return err
	}
	if err := markSourceFileParseSuccess(tx, sourceFile.SourceFileID); err != nil {
		return err
	}

	return tx.Commit()
}

func upsertSourceFile(ctx context.Context, tx *sql.Tx, report ReportFile, storedPath, nowText string) (sourceFileState, error) {
	var latestID int64
	var latestHash string
	var latestParseStatus string
	err := tx.QueryRowContext(ctx, `
SELECT source_file_id, COALESCE(remote_hash, ''), parse_status
FROM prof_finance_source_file
WHERE filename = ?
ORDER BY source_file_id DESC
LIMIT 1`, report.Filename).Scan(&latestID, &latestHash, &latestParseStatus)
	if err != nil && err != sql.ErrNoRows {
		return sourceFileState{}, err
	}

	if err == nil && latestHash == report.Hash {
		if _, err := tx.ExecContext(ctx, `
UPDATE prof_finance_source_file
SET manifest_seen_at = ?, downloaded_at = ?, validated_at = ?, stored_path = ?, remote_filesize = ?
WHERE source_file_id = ?`,
			nowText, nowText, nowText, storedPath, report.Filesize, latestID); err != nil {
			return sourceFileState{}, err
		}
		return sourceFileState{SourceFileID: latestID, ParseStatus: latestParseStatus}, nil
	}

	result, err := tx.ExecContext(ctx, `
INSERT INTO prof_finance_source_file(
	source_name, filename, report_date, remote_hash, remote_filesize, stored_path, manifest_seen_at,
	downloaded_at, validated_at, parse_status, parse_error, supersedes_source_file_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"gpcw",
		report.Filename,
		report.ReportDate,
		report.Hash,
		report.Filesize,
		storedPath,
		nowText,
		nowText,
		nowText,
		"pending",
		"",
		nullInt64(latestID),
	)
	if err != nil {
		return sourceFileState{}, err
	}
	sourceFileID, err := result.LastInsertId()
	if err != nil {
		return sourceFileState{}, err
	}
	return sourceFileState{SourceFileID: sourceFileID, ParseStatus: "pending"}, nil
}

func clearSourceFileMaterialization(tx *sql.Tx, sourceFileID int64) error {
	if _, err := tx.Exec(`DELETE FROM prof_finance_source_value_raw WHERE source_file_id = ?`, sourceFileID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM prof_finance_source_report WHERE source_file_id = ?`, sourceFileID); err != nil {
		return err
	}
	var reportVersionIDs []int64
	rows, err := tx.Query(`SELECT report_version_id FROM prof_finance_report_version WHERE source_file_id = ?`, sourceFileID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var reportVersionID int64
		if err := rows.Scan(&reportVersionID); err != nil {
			rows.Close()
			return err
		}
		reportVersionIDs = append(reportVersionIDs, reportVersionID)
	}
	rows.Close()
	for _, reportVersionID := range reportVersionIDs {
		if _, err := tx.Exec(`DELETE FROM prof_finance_report_payload WHERE report_version_id = ?`, reportVersionID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM prof_finance_report_version WHERE source_file_id = ?`, sourceFileID); err != nil {
		return err
	}
	return nil
}

func insertSourceReport(tx *sql.Tx, sourceFileID int64, parsed *parsedRawReport, nowText string) error {
	_, err := tx.Exec(`
INSERT INTO prof_finance_source_report(source_file_id, report_date, field_count, row_count, parsed_at)
VALUES (?, ?, ?, ?, ?)`,
		sourceFileID,
		parsed.ReportDate,
		parsed.FieldCount,
		parsed.RowCount,
		nowText,
	)
	return err
}

func insertRawValues(tx *sql.Tx, sourceFileID int64, parsed *parsedRawReport, nowText string) error {
	stmt, err := tx.Prepare(`
INSERT INTO prof_finance_source_value_raw(
	source_file_id, full_code, report_date, source_field_id, raw_numeric_value, raw_text_value, raw_value_type, parsed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range parsed.Rows {
		sourceFieldIDs := make([]int, 0, len(row.Fields))
		for sourceFieldID := range row.Fields {
			sourceFieldIDs = append(sourceFieldIDs, sourceFieldID)
		}
		sort.Ints(sourceFieldIDs)
		for _, sourceFieldID := range sourceFieldIDs {
			value := row.Fields[sourceFieldID]
			var numeric any
			var text any
			if value.ValueType == "date" {
				text = value.Text
				numeric = value.Numeric
			} else {
				numeric = value.Numeric
			}
			if _, err := stmt.Exec(
				sourceFileID,
				row.FullCode,
				row.ReportDate,
				sourceFieldID,
				numeric,
				text,
				value.ValueType,
				nowText,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func materializeReportPayloads(
	tx *sql.Tx,
	registry *Registry,
	sourceFileID int64,
	sourceReportFile string,
	parsed *parsedRawReport,
	versionMetadata map[string]rebuildReportVersionMetadata,
	fallbackSeenAt string,
	preserveTimestamps bool,
	now time.Time,
	nowText string,
) error {
	for _, row := range parsed.Rows {
		firstSeenAt, lastSeenAt, ingestedAt := resolveMaterializationTimestamps(versionMetadata[row.FullCode], fallbackSeenAt, nowText)
		payload, missing := buildPayloadForRow(registry, row)
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		missingJSON, err := json.Marshal(missing)
		if err != nil {
			return err
		}

		announceDateRaw := pickFirstNonEmpty(
			stringValue(payload["financial_report_announcement_date"]),
			stringValue(payload["earnings_preview_announcement_date"]),
			stringValue(payload["flash_report_announcement_date"]),
		)
		announceDateSource := ""
		switch {
		case stringValue(payload["financial_report_announcement_date"]) != "":
			announceDateSource = "gpcw_314"
		case stringValue(payload["earnings_preview_announcement_date"]) != "":
			announceDateSource = "gpcw_313"
		case stringValue(payload["flash_report_announcement_date"]) != "":
			announceDateSource = "gpcw_315"
		default:
			announceDateSource = "first_seen_fallback"
		}
		effectiveAnnounceDate := announceDateRaw
		if effectiveAnnounceDate == "" {
			effectiveAnnounceDate = timestampToDate(firstSeenAt)
		}
		if effectiveAnnounceDate == "" {
			effectiveAnnounceDate = now.Format("20060102")
		}

		versionHash := checksumBytes(payloadJSON, []byte(announceDateRaw), []byte(row.FullCode), []byte(row.ReportDate), []byte(sourceReportFile))
		payloadHash := checksumBytes(payloadJSON, missingJSON)

		var previousLatest sql.NullInt64
		if err := tx.QueryRow(`
SELECT report_version_id
FROM prof_finance_report_version
WHERE full_code = ? AND report_date = ? AND is_latest_corrected = 1
ORDER BY report_version_id DESC
LIMIT 1`, row.FullCode, row.ReportDate).Scan(&previousLatest); err != nil && err != sql.ErrNoRows {
			return err
		}

		if previousLatest.Valid {
			var err error
			if preserveTimestamps {
				_, err = tx.Exec(`UPDATE prof_finance_report_version SET is_latest_corrected = 0 WHERE report_version_id = ?`, previousLatest.Int64)
			} else {
				_, err = tx.Exec(`UPDATE prof_finance_report_version SET is_latest_corrected = 0, last_seen_at = ? WHERE report_version_id = ?`, nowText, previousLatest.Int64)
			}
			if err != nil {
				return err
			}
		}

		result, err := tx.Exec(`
INSERT INTO prof_finance_report_version(
	full_code, report_date, source_file_id, version_hash, announce_date_raw, effective_announce_date,
	announce_date_source, preview_announce_date_raw, flash_report_announce_date_raw, first_seen_at, last_seen_at,
	ingested_at, is_latest_corrected, supersedes_report_version_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.FullCode,
			row.ReportDate,
			sourceFileID,
			versionHash,
			nullString(announceDateRaw),
			effectiveAnnounceDate,
			announceDateSource,
			nullString(stringValue(payload["earnings_preview_announcement_date"])),
			nullString(stringValue(payload["flash_report_announcement_date"])),
			firstSeenAt,
			lastSeenAt,
			ingestedAt,
			1,
			nullInt64FromNull(previousLatest),
		)
		if err != nil {
			return err
		}
		reportVersionID, err := result.LastInsertId()
		if err != nil {
			return err
		}

		if _, err := tx.Exec(`
INSERT INTO prof_finance_report_payload(
	report_version_id, full_code, report_date, field_values, missing_field_codes, supported_field_count,
	available_field_count, payload_hash, materialized_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			reportVersionID,
			row.FullCode,
			row.ReportDate,
			string(payloadJSON),
			string(missingJSON),
			len(registry.All()),
			len(payload),
			payloadHash,
			nowText,
		); err != nil {
			return err
		}
	}
	return nil
}

func markSourceFileParseSuccess(tx *sql.Tx, sourceFileID int64) error {
	_, err := tx.Exec(`UPDATE prof_finance_source_file SET parse_status = 'success', parse_error = '' WHERE source_file_id = ?`, sourceFileID)
	return err
}

func markSourceFileParseFailure(tx *sql.Tx, sourceFileID int64, parseError string) error {
	_, err := tx.Exec(`UPDATE prof_finance_source_file SET parse_status = 'failed', parse_error = ? WHERE source_file_id = ?`, parseError, sourceFileID)
	return err
}

type execContexter interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func upsertSourceWatermark(ctx context.Context, execer execContexter, manifestFetchedAt, latestSeen, latestIngested, watermarkDate, updatedAt string) error {
	_, err := execer.ExecContext(ctx, `
INSERT INTO prof_finance_source_watermark(
	source_name, manifest_fetched_at, latest_report_date_seen, latest_report_date_ingested, watermark_date, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(source_name) DO UPDATE SET
	manifest_fetched_at = excluded.manifest_fetched_at,
	latest_report_date_seen = excluded.latest_report_date_seen,
	latest_report_date_ingested = excluded.latest_report_date_ingested,
	watermark_date = excluded.watermark_date,
	updated_at = excluded.updated_at`,
		"gpcw",
		manifestFetchedAt,
		latestSeen,
		latestIngested,
		watermarkDate,
		updatedAt,
	)
	return err
}

func loadRebuildVersionMetadata(ctx context.Context, tx *sql.Tx) (map[int64]map[string]rebuildReportVersionMetadata, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT source_file_id, full_code, first_seen_at, last_seen_at, ingested_at
FROM prof_finance_report_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]map[string]rebuildReportVersionMetadata)
	for rows.Next() {
		var (
			sourceFileID int64
			fullCode     string
			metadata     rebuildReportVersionMetadata
		)
		if err := rows.Scan(&sourceFileID, &fullCode, &metadata.FirstSeenAt, &metadata.LastSeenAt, &metadata.IngestedAt); err != nil {
			return nil, err
		}
		if out[sourceFileID] == nil {
			out[sourceFileID] = make(map[string]rebuildReportVersionMetadata)
		}
		out[sourceFileID][fullCode] = metadata
	}
	return out, rows.Err()
}

func loadRebuildWatermarkState(ctx context.Context, tx *sql.Tx) (rebuildWatermarkState, error) {
	var state rebuildWatermarkState
	err := tx.QueryRowContext(ctx, `
SELECT manifest_fetched_at, latest_report_date_seen, watermark_date
FROM prof_finance_source_watermark
WHERE source_name = ?`, "gpcw").Scan(&state.ManifestFetchedAt, &state.LatestSeen, &state.WatermarkDate)
	if err == sql.ErrNoRows {
		return rebuildWatermarkState{}, nil
	}
	if err != nil {
		return rebuildWatermarkState{}, err
	}
	state.Found = true
	return state, nil
}

func resolveMaterializationTimestamps(metadata rebuildReportVersionMetadata, fallbackSeenAt, nowText string) (string, string, string) {
	firstSeenAt := strings.TrimSpace(metadata.FirstSeenAt)
	lastSeenAt := strings.TrimSpace(metadata.LastSeenAt)
	ingestedAt := strings.TrimSpace(metadata.IngestedAt)

	if firstSeenAt == "" {
		firstSeenAt = strings.TrimSpace(fallbackSeenAt)
	}
	if firstSeenAt == "" {
		firstSeenAt = nowText
	}
	if lastSeenAt == "" {
		lastSeenAt = firstSeenAt
	}
	if ingestedAt == "" {
		ingestedAt = firstSeenAt
	}
	return firstSeenAt, lastSeenAt, ingestedAt
}

func timestampToDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("20060102")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format("20060102")
	}
	return normalizeOptionalDate(value)
}

func rebuildParsedReport(ctx context.Context, tx *sql.Tx, sourceFileID int64, reportDate string) (*parsedRawReport, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT full_code, source_field_id, raw_numeric_value, COALESCE(raw_text_value, ''), raw_value_type
FROM prof_finance_source_value_raw
WHERE source_file_id = ?
ORDER BY full_code ASC, source_field_id ASC`, sourceFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCode := make(map[string]map[int]rawFieldValue)
	maxFieldCount := 0
	for rows.Next() {
		var fullCode string
		var sourceFieldID int
		var numeric sql.NullFloat64
		var text string
		var valueType string
		if err := rows.Scan(&fullCode, &sourceFieldID, &numeric, &text, &valueType); err != nil {
			return nil, err
		}
		fields := byCode[fullCode]
		if fields == nil {
			fields = make(map[int]rawFieldValue)
			byCode[fullCode] = fields
		}
		fields[sourceFieldID] = rawFieldValue{
			Numeric:   numeric.Float64,
			Text:      text,
			ValueType: valueType,
		}
		if sourceFieldID > maxFieldCount {
			maxFieldCount = sourceFieldID
		}
	}

	result := &parsedRawReport{
		ReportDate: reportDate,
		FieldCount: maxFieldCount,
		RowCount:   len(byCode),
		Rows:       make([]parsedFinanceRow, 0, len(byCode)),
	}
	for fullCode, fields := range byCode {
		result.Rows = append(result.Rows, parsedFinanceRow{
			FullCode:   fullCode,
			ReportDate: reportDate,
			Fields:     fields,
		})
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].FullCode < result.Rows[j].FullCode })
	return result, rows.Err()
}

func checksumString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func checksumBytes(parts ...[]byte) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt64FromNull(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func stringValue(v interface{}) string {
	if value, ok := v.(string); ok {
		return value
	}
	return ""
}

func pickFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
