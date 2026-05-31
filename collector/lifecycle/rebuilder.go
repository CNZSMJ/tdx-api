package lifecycle

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type RebuildRequest struct {
	SourceDBPath      string
	ReplacementDBPath string
	TableName         string
	HotCutoffDate     string
	TableCutoffs      map[string]string
}

type RebuildResult struct {
	SourceDBPath      string
	ReplacementDBPath string
	TableName         string
	HotCutoffDate     string
	SourceRows        int64
	RetainedRows      int64
	PrunedRows        int64
}

type AtomicReplaceResult struct {
	SourcePath string
	BackupPath string
}

type RecoveryAction struct {
	Action string
	Detail string
}

type InterruptedPruningRecoveryResult struct {
	PruningSegments int
	RecoveredGroups int
	FailedSegments  int
}

func BuildReplacementHotDB(req RebuildRequest) (RebuildResult, error) {
	tableCutoffs := req.effectiveTableCutoffs()
	if req.SourceDBPath == "" || req.ReplacementDBPath == "" || len(tableCutoffs) == 0 {
		return RebuildResult{}, errors.New("source, replacement, and table cutoffs are required")
	}
	_ = os.Remove(req.ReplacementDBPath)
	dest, err := openLifecycleSQLite(req.ReplacementDBPath)
	if err != nil {
		return RebuildResult{}, err
	}
	defer dest.Close()
	if err := configureReplacementSQLite(dest); err != nil {
		return RebuildResult{}, err
	}
	if _, err := dest.Exec(`ATTACH DATABASE '` + strings.ReplaceAll(req.SourceDBPath, "'", "''") + `' AS src`); err != nil {
		return RebuildResult{}, err
	}
	defer dest.Exec(`DETACH DATABASE src`)

	tables, err := attachedTableNames(dest)
	if err != nil {
		return RebuildResult{}, err
	}
	var sourceRows int64
	var retainedRows int64

	for _, table := range tables {
		tableSQL, err := sourceSQL(dest, "table", table)
		if err != nil {
			return RebuildResult{}, err
		}
		if _, err := dest.Exec(tableSQL); err != nil {
			return RebuildResult{}, err
		}
		insertSQL := fmt.Sprintf(`INSERT INTO %s SELECT * FROM src.%s`, quoteIdent(table), quoteIdent(table))
		args := []any{}
		if cutoff, ok := tableCutoffs[table]; ok {
			tableSourceRows, err := countAttachedRows(dest, table, "")
			if err != nil {
				return RebuildResult{}, err
			}
			sourceRows += tableSourceRows
			filter, filterArgs, err := retainedRowsFilter(dest, table, cutoff)
			if err != nil {
				return RebuildResult{}, err
			}
			if filter != "" {
				insertSQL += " WHERE " + filter
				args = append(args, filterArgs...)
			}
		}
		if _, err := dest.Exec(insertSQL, args...); err != nil {
			return RebuildResult{}, err
		}
		if _, ok := tableCutoffs[table]; ok {
			tableRetainedRows, err := countRowsInDB(dest, table)
			if err != nil {
				return RebuildResult{}, err
			}
			retainedRows += tableRetainedRows
		}
	}

	indexSQL, err := allSourceIndexSQL(dest)
	if err != nil {
		return RebuildResult{}, err
	}
	for _, stmt := range indexSQL {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := dest.Exec(stmt); err != nil {
			return RebuildResult{}, err
		}
	}
	if err := integrityCheck(dest); err != nil {
		return RebuildResult{}, err
	}
	tableName := req.TableName
	if tableName == "" {
		tableName = strings.Join(sortedTableNames(tableCutoffs), ",")
	}
	hotCutoff := req.HotCutoffDate
	if hotCutoff == "" && len(tableCutoffs) == 1 {
		for _, cutoff := range tableCutoffs {
			hotCutoff = cutoff
		}
	}
	return RebuildResult{
		SourceDBPath:      req.SourceDBPath,
		ReplacementDBPath: req.ReplacementDBPath,
		TableName:         tableName,
		HotCutoffDate:     hotCutoff,
		SourceRows:        sourceRows,
		RetainedRows:      retainedRows,
		PrunedRows:        sourceRows - retainedRows,
	}, nil
}

func configureReplacementSQLite(db *sql.DB) error {
	pragmas := []string{
		`PRAGMA journal_mode=OFF`,
		`PRAGMA synchronous=OFF`,
		`PRAGMA temp_store=MEMORY`,
		`PRAGMA cache_size=-200000`,
		`PRAGMA locking_mode=EXCLUSIVE`,
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

func VerifyHotRetainedRows(sourceDB, replacementDB, table, cutoff string) error {
	return VerifyHotRetainedRowsForTables(sourceDB, replacementDB, map[string]string{table: cutoff})
}

func VerifyHotRetainedRowsForTables(sourceDB, replacementDB string, tableCutoffs map[string]string) error {
	source, err := openLifecycleSQLiteReadOnly(sourceDB)
	if err != nil {
		return err
	}
	defer source.Close()
	replacement, err := openLifecycleSQLiteReadOnly(replacementDB)
	if err != nil {
		return err
	}
	defer replacement.Close()
	for table, cutoff := range tableCutoffs {
		var sourceRetained int64
		filter, args, err := retainedRowsFilter(source, table, cutoff)
		if err != nil {
			return err
		}
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, quoteIdent(table))
		if filter != "" {
			query += " WHERE " + filter
		}
		if err := source.QueryRow(query, args...).Scan(&sourceRetained); err != nil {
			return err
		}
		replacementRows, err := countRowsInDB(replacement, table)
		if err != nil {
			return err
		}
		if sourceRetained != replacementRows {
			return fmt.Errorf("retained row mismatch for %s: source=%d replacement=%d", table, sourceRetained, replacementRows)
		}
	}
	if err := compareIndexNames(source, replacement); err != nil {
		return err
	}
	return integrityCheck(replacement)
}

func (req RebuildRequest) effectiveTableCutoffs() map[string]string {
	if len(req.TableCutoffs) > 0 {
		out := make(map[string]string, len(req.TableCutoffs))
		for table, cutoff := range req.TableCutoffs {
			table = strings.TrimSpace(table)
			cutoff = strings.TrimSpace(cutoff)
			if table == "" || cutoff == "" {
				continue
			}
			out[table] = cutoff
		}
		return out
	}
	if strings.TrimSpace(req.TableName) == "" || strings.TrimSpace(req.HotCutoffDate) == "" {
		return nil
	}
	return map[string]string{req.TableName: req.HotCutoffDate}
}

func sortedTableNames(tableCutoffs map[string]string) []string {
	names := make([]string, 0, len(tableCutoffs))
	for table := range tableCutoffs {
		names = append(names, table)
	}
	sort.Strings(names)
	return names
}

func AtomicReplaceHotDB(sourcePath, replacementPath, batchID string) (AtomicReplaceResult, error) {
	replacement, err := openLifecycleSQLiteReadOnly(replacementPath)
	if err != nil {
		return AtomicReplaceResult{}, err
	}
	if err := integrityCheck(replacement); err != nil {
		_ = replacement.Close()
		return AtomicReplaceResult{}, err
	}
	_ = replacement.Close()

	backupPath := sourcePath + ".pre_lifecycle." + batchID + ".bak"
	if err := os.Rename(sourcePath, backupPath); err != nil {
		return AtomicReplaceResult{}, err
	}
	if err := os.Rename(replacementPath, sourcePath); err != nil {
		_ = os.Rename(backupPath, sourcePath)
		return AtomicReplaceResult{}, err
	}
	return AtomicReplaceResult{SourcePath: sourcePath, BackupPath: backupPath}, nil
}

func RollbackHotDBReplacement(sourcePath, backupPath string) error {
	_ = os.Remove(sourcePath)
	return os.Rename(backupPath, sourcePath)
}

func RecoverHotReplacementArtifacts(sourcePath, batchID string) (RecoveryAction, error) {
	backupPath := sourcePath + ".pre_lifecycle." + batchID + ".bak"
	tmpPath := sourcePath + ".replacement." + batchID + ".tmp"
	sourceExists := fileExists(sourcePath)
	backupExists := fileExists(backupPath)
	tmpExists := fileExists(tmpPath)
	if tmpExists {
		if err := os.Remove(tmpPath); err != nil {
			return RecoveryAction{}, err
		}
	}
	if !sourceExists && backupExists {
		if err := os.Rename(backupPath, sourcePath); err != nil {
			return RecoveryAction{}, err
		}
		return RecoveryAction{Action: "restored_backup", Detail: "source missing and backup restored; tmp removed if present"}, nil
	}
	if tmpExists {
		return RecoveryAction{Action: "removed_tmp", Detail: "source exists; interrupted replacement tmp removed"}, nil
	}
	if sourceExists && backupExists {
		db, err := openLifecycleSQLiteReadOnly(sourcePath)
		if err != nil {
			return RecoveryAction{}, err
		}
		defer db.Close()
		if err := integrityCheck(db); err != nil {
			return RecoveryAction{}, err
		}
		return RecoveryAction{Action: "source_and_backup_present", Detail: "source verified; backup retained for explicit cleanup"}, nil
	}
	return RecoveryAction{Action: "none"}, nil
}

func RecoverInterruptedPruningSegments(store *ManifestStore) (InterruptedPruningRecoveryResult, error) {
	var result InterruptedPruningRecoveryResult
	if store == nil {
		return result, errors.New("manifest store is required")
	}
	segments, err := store.ListSegments()
	if err != nil {
		return result, err
	}
	groups := make(map[string][]ColdSegment)
	for _, segment := range segments {
		if segment.Status != SegmentPruning {
			continue
		}
		result.PruningSegments++
		sourcePath := strings.TrimSpace(segment.SourceDBPath)
		batchID := strings.TrimSpace(segment.ArchiveBatchID)
		if sourcePath == "" || batchID == "" {
			return result, fmt.Errorf("pruning segment %s missing source_db_path or archive_batch_id", segment.SegmentID)
		}
		key := sourcePath + "\x00" + batchID
		groups[key] = append(groups[key], segment)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		action, err := RecoverInterruptedPruningArtifacts(parts[0], parts[1])
		if err != nil {
			return result, err
		}
		result.RecoveredGroups++
		reason := "interrupted during hot pruning; " + action.Detail
		for _, segment := range groups[key] {
			if err := store.UpdateSegmentStatus(segment.SegmentID, SegmentFailed, reason); err != nil {
				return result, err
			}
			result.FailedSegments++
		}
	}
	return result, nil
}

func RecoverInterruptedPruningArtifacts(sourcePath, batchID string) (RecoveryAction, error) {
	backupPath := sourcePath + ".pre_lifecycle." + batchID + ".bak"
	tmpPath := sourcePath + ".replacement." + batchID + ".tmp"
	sourceExists := fileExists(sourcePath)
	backupExists := fileExists(backupPath)
	tmpExists := fileExists(tmpPath)
	if tmpExists {
		if err := os.Remove(tmpPath); err != nil {
			return RecoveryAction{}, err
		}
	}
	if sourceExists && backupExists {
		if err := RollbackHotDBReplacement(sourcePath, backupPath); err != nil {
			return RecoveryAction{}, err
		}
		return RecoveryAction{Action: "rolled_back_backup", Detail: "source and backup present; restored backup after interrupted pruning"}, nil
	}
	if !sourceExists && backupExists {
		if err := os.Rename(backupPath, sourcePath); err != nil {
			return RecoveryAction{}, err
		}
		return RecoveryAction{Action: "restored_backup", Detail: "source missing and backup restored; tmp removed if present"}, nil
	}
	if tmpExists {
		return RecoveryAction{Action: "removed_tmp", Detail: "source exists; interrupted replacement tmp removed"}, nil
	}
	if sourceExists {
		return RecoveryAction{Action: "source_only", Detail: "source exists; no replacement artifacts found for pruning segment"}, nil
	}
	return RecoveryAction{}, fmt.Errorf("cannot recover interrupted pruning artifacts for %s: source and backup are missing", sourcePath)
}

func SQLiteIndexNames(dbPath string) ([]string, error) {
	db, err := openLifecycleSQLiteReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_autoindex%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func sourceSQL(db *sql.DB, typ, name string) (string, error) {
	var sqlText string
	err := db.QueryRow(`SELECT sql FROM src.sqlite_master WHERE type=? AND name=?`, typ, name).Scan(&sqlText)
	return sqlText, err
}

func allSourceIndexSQL(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT sql FROM src.sqlite_master WHERE type='index' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return nil, err
		}
		out = append(out, stmt)
	}
	return out, rows.Err()
}

func attachedTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM src.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func retainedRowsFilter(db *sql.DB, table, cutoff string) (string, []any, error) {
	columns, err := sqliteColumns(db, table)
	if err != nil {
		return "", nil, err
	}
	if _, ok := columns["TradeDate"]; ok {
		return normalizedSQLiteDateExpr("TradeDate") + ` >= ?`, []any{cutoff}, nil
	}
	if _, ok := columns["trade_date"]; ok {
		return normalizedSQLiteDateExpr("trade_date") + ` >= ?`, []any{cutoff}, nil
	}
	if _, ok := columns["CaptureTime"]; ok {
		start, err := cutoffUnixStart(cutoff)
		if err != nil {
			return "", nil, err
		}
		return `CaptureTime >= ?`, []any{start}, nil
	}
	if _, ok := columns["capture_time"]; ok {
		start, err := cutoffUnixStart(cutoff)
		if err != nil {
			return "", nil, err
		}
		return `capture_time >= ?`, []any{start}, nil
	}
	return "", nil, nil
}

func cutoffUnixStart(cutoff string) (int64, error) {
	loc, err := shanghaiLocation()
	if err != nil {
		return 0, err
	}
	day, err := time.ParseInLocation("20060102", cutoff, loc)
	if err != nil {
		return 0, err
	}
	return dayStart(day, loc).Unix(), nil
}

func countAttachedRows(db *sql.DB, table, where string) (int64, error) {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM src.%s`, quoteIdent(table))
	if where != "" {
		query += " WHERE " + where
	}
	var count int64
	err := db.QueryRow(query).Scan(&count)
	return count, err
}

func countRowsInDB(db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(table)).Scan(&count)
	return count, err
}

func compareIndexNames(left, right *sql.DB) error {
	leftNames, err := indexNamesFromDB(left)
	if err != nil {
		return err
	}
	rightNames, err := indexNamesFromDB(right)
	if err != nil {
		return err
	}
	if strings.Join(leftNames, "\n") != strings.Join(rightNames, "\n") {
		return fmt.Errorf("index mismatch: source=%v replacement=%v", leftNames, rightNames)
	}
	return nil
}

func indexNamesFromDB(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_autoindex%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func integrityCheck(db *sql.DB) error {
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity_check failed: %s", result)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
