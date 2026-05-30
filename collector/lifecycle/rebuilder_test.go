package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReplacementHotDBRetainsRowsAndIndexes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	replacement := filepath.Join(root, "sh600000.replacement.tmp")
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`CREATE INDEX idx_trade_history_date ON TradeHistory(TradeDate, Seq)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1,1,11000)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',2,1,12000)`,
	})

	result, err := BuildReplacementHotDB(RebuildRequest{
		SourceDBPath:      source,
		ReplacementDBPath: replacement,
		TableName:         "TradeHistory",
		HotCutoffDate:     "20260401",
	})
	if err != nil {
		t.Fatalf("build replacement: %v", err)
	}
	if result.SourceRows != 2 || result.RetainedRows != 1 || result.PrunedRows != 1 {
		t.Fatalf("unexpected rebuild result: %+v", result)
	}
	if err := VerifyHotRetainedRows(source, replacement, "TradeHistory", "20260401"); err != nil {
		t.Fatalf("verify retained rows: %v", err)
	}
	indexes, err := SQLiteIndexNames(replacement)
	if err != nil {
		t.Fatalf("replacement indexes: %v", err)
	}
	if !containsString(indexes, "idx_trade_history_date") {
		t.Fatalf("replacement indexes = %#v, missing original index", indexes)
	}
}

func TestAtomicReplaceHotDBRollbackAndRecovery(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	replacement := filepath.Join(root, "sh600000.replacement.tmp")
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1,1,11000)`,
	})
	mustCreateSQLite(t, replacement, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',2,1,12000)`,
	})

	replace, err := AtomicReplaceHotDB(source, replacement, "batch-1")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replace.BackupPath == "" {
		t.Fatalf("missing backup path: %+v", replace)
	}
	got, err := countRows(source, "TradeHistory")
	if err != nil || got != 1 {
		t.Fatalf("replacement source rows=%d err=%v", got, err)
	}
	if _, err := os.Stat(replace.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	if err := RollbackHotDBReplacement(source, replace.BackupPath); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rolledBack, err := countRows(source, "TradeHistory")
	if err != nil || rolledBack != 1 {
		t.Fatalf("rolled back rows=%d err=%v", rolledBack, err)
	}
}

func TestStartupRecoveryHandlesInterruptedReplacementArtifacts(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	backup := source + ".pre_lifecycle.batch-1.bak"
	tmp := source + ".replacement.batch-1.tmp"
	mustCreateSQLite(t, backup, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102')`,
	})
	mustCreateSQLite(t, tmp, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
	})

	action, err := RecoverHotReplacementArtifacts(source, "batch-1")
	if err != nil {
		t.Fatalf("recover missing source: %v", err)
	}
	if action.Action != "restored_backup" {
		t.Fatalf("action = %+v", action)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not restored: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("tmp should be deleted, stat err=%v", err)
	}

	mustCreateSQLite(t, tmp, []string{`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`})
	action, err = RecoverHotReplacementArtifacts(source, "batch-1")
	if err != nil {
		t.Fatalf("recover tmp with source: %v", err)
	}
	if action.Action != "removed_tmp" {
		t.Fatalf("action = %+v", action)
	}
}

func TestRecoverInterruptedPruningSegmentsRemovesTmpAndFailsGroupedSegments(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sz159206.db")
	tmp := source + ".replacement.batch-1.tmp"
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE MinuteLive(Code TEXT, TradeDate TEXT)`,
		`CREATE TABLE TradeLive(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO MinuteLive VALUES('sz159206','20250728')`,
		`INSERT INTO TradeLive VALUES('sz159206','20250728')`,
	})
	mustCreateSQLite(t, tmp, []string{
		`CREATE TABLE MinuteLive(Code TEXT, TradeDate TEXT)`,
		`CREATE TABLE TradeLive(Code TEXT, TradeDate TEXT)`,
	})
	store, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer store.Close()
	for _, table := range []string{"MinuteLive", "TradeLive"} {
		if err := store.CreateSegment(ColdSegment{
			SegmentID:            "seg-" + table,
			DatasetID:            "a-stock-market-tdx",
			ArchiveBatchID:       "batch-1",
			Domain:               "live",
			TableName:            table,
			Instrument:           "sz159206",
			PartitionKey:         "domain=live/table=" + table + "/instrument=sz159206/year=2025",
			StartDate:            "20250728",
			EndDate:              "20250822",
			ColdURI:              "tdx-cold://a-stock-market-tdx/cold/domain=live/table=" + table + "/instrument=sz159206/year=2025/part-test.parquet",
			Status:               SegmentPruning,
			SchemaVersion:        1,
			StorageScheme:        "local-v1",
			FinalizationStrategy: "atomic_rename_same_device",
			SourceDBPath:         source,
		}); err != nil {
			t.Fatalf("create segment %s: %v", table, err)
		}
	}

	result, err := RecoverInterruptedPruningSegments(store)
	if err != nil {
		t.Fatalf("recover pruning segments: %v", err)
	}
	if result.PruningSegments != 2 || result.RecoveredGroups != 1 || result.FailedSegments != 2 {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("tmp should be removed, stat err=%v", err)
	}
	segments, err := store.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	for _, segment := range segments {
		if segment.Status != SegmentFailed {
			t.Fatalf("segment %s status = %s, want failed", segment.SegmentID, segment.Status)
		}
	}
}

func TestRecoverInterruptedPruningSegmentsRollsBackBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sh600000.db")
	backup := source + ".pre_lifecycle.batch-1.bak"
	mustCreateSQLite(t, source, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424')`,
	})
	mustCreateSQLite(t, backup, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20250728')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424')`,
	})
	store, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer store.Close()
	if err := store.CreateSegment(ColdSegment{
		SegmentID:            "seg-1",
		DatasetID:            "a-stock-market-tdx",
		ArchiveBatchID:       "batch-1",
		Domain:               "trade",
		TableName:            "TradeHistory",
		Instrument:           "sh600000",
		PartitionKey:         "domain=trade/table=TradeHistory/instrument=sh600000/year=2025",
		StartDate:            "20250728",
		EndDate:              "20250822",
		ColdURI:              "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2025/part-test.parquet",
		Status:               SegmentPruning,
		SchemaVersion:        1,
		StorageScheme:        "local-v1",
		FinalizationStrategy: "atomic_rename_same_device",
		SourceDBPath:         source,
	}); err != nil {
		t.Fatalf("create segment: %v", err)
	}

	result, err := RecoverInterruptedPruningSegments(store)
	if err != nil {
		t.Fatalf("recover pruning segments: %v", err)
	}
	if result.RecoveredGroups != 1 || result.FailedSegments != 1 {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	rows, err := countRows(source, "TradeHistory")
	if err != nil {
		t.Fatalf("count source rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("source rows = %d, want restored backup rows", rows)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup should be consumed by rollback, stat err=%v", err)
	}
	segment, err := store.GetSegment("seg-1")
	if err != nil {
		t.Fatalf("get segment: %v", err)
	}
	if segment == nil || segment.Status != SegmentFailed {
		t.Fatalf("segment = %+v, want failed", segment)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
