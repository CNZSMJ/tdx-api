package lifecycle

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestStoreCreatesSchemaIndexesAndDefaultRetention(t *testing.T) {
	store, err := OpenManifestStore(filepath.Join(t.TempDir(), "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer store.Close()

	for _, table := range []string{"cold_segment", "lifecycle_batch", "hot_retention_policy", "lifecycle_watermark", "lifecycle_restore"} {
		if ok, err := store.HasTable(table); err != nil || !ok {
			t.Fatalf("table %s exists=%v err=%v", table, ok, err)
		}
	}
	requiredSegmentColumns := []string{
		"segment_id", "dataset_id", "domain", "table_name", "instrument", "partition_key",
		"start_date", "end_date", "trading_day_count", "row_count", "byte_size",
		"file_checksum", "logical_checksum", "schema_version", "storage_scheme", "finalization_strategy",
		"cold_uri", "archive_batch_id", "source_db_path", "source_db_size", "source_selection_hash",
		"status", "error_code", "error_message", "created_at", "exporting_at", "exported_at",
		"verified_at", "restore_tested_at", "pruning_at", "hot_pruned_at", "active_at", "updated_at",
	}
	segmentColumns, err := store.tableColumns("cold_segment")
	if err != nil {
		t.Fatalf("list cold_segment columns: %v", err)
	}
	for _, column := range requiredSegmentColumns {
		if !segmentColumns[column] {
			t.Fatalf("cold_segment missing required column %s; columns=%v", column, segmentColumns)
		}
	}
	for _, index := range RequiredManifestIndexes() {
		if ok, err := store.HasIndex(index); err != nil || !ok {
			t.Fatalf("index %s exists=%v err=%v", index, ok, err)
		}
	}
	policy, err := store.GetRetentionPolicy("trade", "TradeHistory")
	if err != nil {
		t.Fatalf("get retention policy: %v", err)
	}
	if policy == nil || policy.ColdRetentionYears != 7 || policy.HotRetentionTradingDays != TradeHotRetentionTradingDays {
		t.Fatalf("unexpected default policy: %+v", policy)
	}
	auctionPolicy, err := store.GetRetentionPolicy("auction", "AuctionSnapshot")
	if err != nil {
		t.Fatalf("get auction retention policy: %v", err)
	}
	if auctionPolicy == nil || auctionPolicy.ColdRetentionYears != 7 || auctionPolicy.HotRetentionTradingDays != DefaultHotRetentionTradingDays {
		t.Fatalf("unexpected auction default policy: %+v", auctionPolicy)
	}
}

func TestManifestStoreUpdatesDefaultRetentionPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cold_manifest.db")
	store, err := OpenManifestStore(dbPath)
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE hot_retention_policy SET hot_retention_trading_days=180 WHERE domain='trade' AND table_name='TradeHistory'`); err != nil {
		t.Fatalf("seed old retention policy: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close manifest: %v", err)
	}

	reopened, err := OpenManifestStore(dbPath)
	if err != nil {
		t.Fatalf("reopen manifest: %v", err)
	}
	defer reopened.Close()

	policy, err := reopened.GetRetentionPolicy("trade", "TradeHistory")
	if err != nil {
		t.Fatalf("get retention policy: %v", err)
	}
	if policy == nil || policy.HotRetentionTradingDays != TradeHotRetentionTradingDays {
		t.Fatalf("retention policy was not updated: %+v", policy)
	}
}

func TestManifestStoreEnforcesSegmentStateMachine(t *testing.T) {
	store, err := OpenManifestStore(filepath.Join(t.TempDir(), "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer store.Close()

	segment := ColdSegment{
		SegmentID:            "seg-1",
		DatasetID:            "a-stock-market-tdx",
		ArchiveBatchID:       "batch-1",
		Domain:               "trade",
		TableName:            "TradeHistory",
		Instrument:           "sh600000",
		PartitionKey:         "domain=trade/table=TradeHistory/instrument=sh600000/year=2024",
		StartDate:            "20240101",
		EndDate:              "20240131",
		TradingDayCount:      23,
		ColdURI:              "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-abcd-000.parquet",
		Status:               SegmentPlanned,
		ByteSize:             4096,
		SchemaVersion:        1,
		StorageScheme:        "local-v1",
		FinalizationStrategy: "atomic_rename_same_device",
		SourceDBPath:         "/tmp/source.db",
		SourceDBSize:         8192,
		SourceSelectionHash:  "selection-hash",
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	if err := store.CreateSegment(segment); err != nil {
		t.Fatalf("create segment: %v", err)
	}
	if err := store.UpdateSegmentStatus("seg-1", SegmentVerified, "unsafe skip"); err == nil {
		t.Fatalf("planned -> verified should fail")
	}
	if err := store.UpdateSegmentStatus("seg-1", SegmentExporting, "start export"); err != nil {
		t.Fatalf("planned -> exporting: %v", err)
	}
	got, err := store.GetSegment("seg-1")
	if err != nil {
		t.Fatalf("get segment: %v", err)
	}
	if got == nil || got.Status != SegmentExporting {
		t.Fatalf("unexpected segment after update: %+v", got)
	}
	if got.DatasetID != segment.DatasetID || got.PartitionKey != segment.PartitionKey || got.StorageScheme != segment.StorageScheme || got.FinalizationStrategy != segment.FinalizationStrategy {
		t.Fatalf("segment metadata was not preserved: got=%+v want=%+v", got, segment)
	}
}

func TestManifestStoreMigratesLegacyBatchIDColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cold_manifest.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE cold_segment (
		segment_id TEXT PRIMARY KEY,
		batch_id TEXT NOT NULL,
		domain TEXT NOT NULL,
		table_name TEXT NOT NULL,
		instrument TEXT,
		start_date TEXT NOT NULL,
		end_date TEXT NOT NULL,
		cold_uri TEXT NOT NULL,
		status TEXT NOT NULL,
		row_count INTEGER NOT NULL DEFAULT 0,
		file_checksum TEXT,
		logical_checksum TEXT,
		schema_version INTEGER NOT NULL,
		status_reason TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		archive_batch_id TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy cold_segment: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cold_segment(segment_id, batch_id, domain, table_name, instrument, start_date, end_date, cold_uri, status, schema_version, created_at, updated_at, archive_batch_id)
		VALUES('legacy-seg', 'legacy-batch', 'trade', 'TradeHistory', 'sh600000', '20240101', '20240131', 'tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-legacy.parquet', 'active', 1, '2024-02-01T00:00:00Z', '2024-02-01T00:00:00Z', 'legacy-batch')`); err != nil {
		t.Fatalf("seed legacy segment: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	store, err := OpenManifestStore(dbPath)
	if err != nil {
		t.Fatalf("open migrated manifest: %v", err)
	}
	defer store.Close()

	columns, err := store.tableColumns("cold_segment")
	if err != nil {
		t.Fatalf("list migrated columns: %v", err)
	}
	if columns["batch_id"] {
		t.Fatalf("legacy batch_id column should be removed after migration")
	}
	if !columns["archive_batch_id"] {
		t.Fatalf("archive_batch_id column should exist after migration")
	}

	var archiveBatchID string
	if err := store.db.QueryRow(`SELECT archive_batch_id FROM cold_segment WHERE segment_id='legacy-seg'`).Scan(&archiveBatchID); err != nil {
		t.Fatalf("read migrated segment: %v", err)
	}
	if archiveBatchID != "legacy-batch" {
		t.Fatalf("archive_batch_id = %q, want legacy-batch", archiveBatchID)
	}

	segment := ColdSegment{
		SegmentID:       "new-seg",
		ArchiveBatchID:  "new-batch",
		Domain:          "trade",
		TableName:       "TradeHistory",
		Instrument:      "sh600001",
		StartDate:       "20240201",
		EndDate:         "20240229",
		TradingDayCount: 20,
		ColdURI:         "tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600001/year=2024/part-new.parquet",
		Status:          SegmentPlanned,
		SchemaVersion:   1,
	}
	if err := store.CreateSegment(segment); err != nil {
		t.Fatalf("create segment after legacy migration: %v", err)
	}
	if err := store.db.QueryRow(`SELECT archive_batch_id FROM cold_segment WHERE segment_id='new-seg'`).Scan(&archiveBatchID); err != nil {
		t.Fatalf("read new segment: %v", err)
	}
	if archiveBatchID != "new-batch" {
		t.Fatalf("new archive_batch_id = %q, want new-batch", archiveBatchID)
	}
}
