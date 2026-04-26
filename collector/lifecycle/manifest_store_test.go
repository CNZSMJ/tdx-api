package lifecycle

import (
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
	if policy == nil || policy.ColdRetentionYears != 7 || policy.HotRetentionTradingDays != 180 {
		t.Fatalf("unexpected default policy: %+v", policy)
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
