package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/tdx/collector/lifecycle"
	systemgov "github.com/injoyai/tdx/governance"
)

func TestExecuteDataLifecycleRestoreTemporaryAndHotPath(t *testing.T) {
	originalDir := databaseDir
	defer func() { databaseDir = originalDir }()
	tmp := t.TempDir()
	databaseDir = tmp

	coldSource := filepath.Join(tmp, "source.db")
	mustCreateWebSQLite(t, coldSource, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
	})
	storage := lifecycle.NewLocalColdStorage(tmp)
	uri := lifecycle.MustFormatColdURI(lifecycle.ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2023/part-restore.parquet"})
	export, err := lifecycle.ExportSegment(lifecycle.ExportRequest{
		SourceDBPath: coldSource,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20230101",
		EndDate:      "20230131",
		Storage:      storage,
		ColdURI:      uri,
	})
	if err != nil {
		t.Fatalf("export cold source: %v", err)
	}
	createRestoreManifestSegment(t, tmp, lifecycle.ColdSegment{
		SegmentID:       "seg-restore",
		ArchiveBatchID:  "batch-restore",
		Domain:          "trade",
		TableName:       "TradeHistory",
		Instrument:      "sh600000",
		StartDate:       export.MinDate,
		EndDate:         export.MaxDate,
		ColdURI:         uri,
		Status:          lifecycle.SegmentActive,
		RowCount:        export.RowCount,
		ByteSize:        export.ByteSize,
		FileChecksum:    export.FileChecksum,
		LogicalChecksum: export.LogicalChecksum,
		SchemaVersion:   1,
	})

	tempRestore, err := executeDataLifecycleRestore(context.Background(), systemgov.DataLifecycleRestoreRequest{
		SegmentID: "seg-restore",
		Mode:      "temporary_query_restore",
	})
	if err != nil {
		t.Fatalf("temporary restore: %v", err)
	}
	if tempRestore.RowCount != 1 {
		t.Fatalf("temporary restore rows = %d, want 1", tempRestore.RowCount)
	}

	hotDB := filepath.Join(tmp, "trade", "sh600000.db")
	mustCreateWebSQLite(t, hotDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	hotRestore, err := executeDataLifecycleRestore(context.Background(), systemgov.DataLifecycleRestoreRequest{
		SegmentID: "seg-restore",
		Mode:      "hot_path_restore",
	})
	if err != nil {
		t.Fatalf("hot path restore: %v", err)
	}
	if hotRestore.TargetDBPath != hotDB || hotRestore.RowCount != 1 {
		t.Fatalf("unexpected hot restore: %+v", hotRestore)
	}
	rows, err := countWebRows(hotDB, "TradeHistory")
	if err != nil {
		t.Fatalf("count hot rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("hot rows after restore = %d, want 2", rows)
	}
}

func TestHotDBPathForColdSegmentSupportsAuctionSnapshot(t *testing.T) {
	originalDir := databaseDir
	defer func() { databaseDir = originalDir }()
	tmp := t.TempDir()
	databaseDir = tmp

	got, err := hotDBPathForColdSegment(lifecycle.ColdSegment{
		Domain:    "auction",
		TableName: "AuctionSnapshot",
	})
	if err != nil {
		t.Fatalf("hot db path: %v", err)
	}
	want := filepath.Join(tmp, "auction", "auction.db")
	if got != want {
		t.Fatalf("auction hot db path = %s, want %s", got, want)
	}
}

func TestExecuteDataLifecycleRestoreRejectsInactiveSegment(t *testing.T) {
	originalDir := databaseDir
	defer func() { databaseDir = originalDir }()
	tmp := t.TempDir()
	databaseDir = tmp

	export, uri := createRestoreTestSegment(t, tmp, "sh600001", "20230102", 1672623000)
	createRestoreManifestSegment(t, tmp, lifecycle.ColdSegment{
		SegmentID:       "seg-inactive",
		ArchiveBatchID:  "batch-restore",
		Domain:          "trade",
		TableName:       "TradeHistory",
		Instrument:      "sh600001",
		StartDate:       export.MinDate,
		EndDate:         export.MaxDate,
		ColdURI:         uri,
		Status:          lifecycle.SegmentFailed,
		RowCount:        export.RowCount,
		ByteSize:        export.ByteSize,
		FileChecksum:    export.FileChecksum,
		LogicalChecksum: export.LogicalChecksum,
		SchemaVersion:   1,
	})

	_, err := executeDataLifecycleRestore(context.Background(), systemgov.DataLifecycleRestoreRequest{
		SegmentID: "seg-inactive",
		Mode:      "hot_path_restore",
	})
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected inactive segment rejection, got %v", err)
	}
}

func TestExecuteDataLifecycleRestoreRejectsChecksumMismatch(t *testing.T) {
	originalDir := databaseDir
	defer func() { databaseDir = originalDir }()
	tmp := t.TempDir()
	databaseDir = tmp

	export, uri := createRestoreTestSegment(t, tmp, "sh600002", "20230102", 1672623000)
	createRestoreManifestSegment(t, tmp, lifecycle.ColdSegment{
		SegmentID:       "seg-corrupt",
		ArchiveBatchID:  "batch-restore",
		Domain:          "trade",
		TableName:       "TradeHistory",
		Instrument:      "sh600002",
		StartDate:       export.MinDate,
		EndDate:         export.MaxDate,
		ColdURI:         uri,
		Status:          lifecycle.SegmentActive,
		RowCount:        export.RowCount,
		ByteSize:        export.ByteSize,
		FileChecksum:    export.FileChecksum,
		LogicalChecksum: export.LogicalChecksum,
		SchemaVersion:   1,
	})

	corruptSource := filepath.Join(tmp, "corrupt-source.db")
	mustCreateWebSQLite(t, corruptSource, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600002','20230103',1672709400,1,99000,99,9,0,'S')`,
	})
	if _, err := lifecycle.ExportSegment(lifecycle.ExportRequest{
		SourceDBPath: corruptSource,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600002",
		StartDate:    "20230101",
		EndDate:      "20230131",
		Storage:      lifecycle.NewLocalColdStorage(tmp),
		ColdURI:      uri,
	}); err != nil {
		t.Fatalf("overwrite cold object: %v", err)
	}

	_, err := executeDataLifecycleRestore(context.Background(), systemgov.DataLifecycleRestoreRequest{
		SegmentID: "seg-corrupt",
		Mode:      "hot_path_restore",
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum mismatch rejection, got %v", err)
	}
}

func createRestoreTestSegment(t *testing.T, root, code, tradeDate string, tradeTime int64) (lifecycle.SegmentExportResult, string) {
	t.Helper()
	sourceDB := filepath.Join(root, code+"-source.db")
	mustCreateWebSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		fmt.Sprintf(`INSERT INTO TradeHistory VALUES('%s','%s',%d,1,11000,10,1,0,'B')`, code, tradeDate, tradeTime),
	})
	uri := lifecycle.MustFormatColdURI(lifecycle.ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=" + code + "/year=2023/part-restore.parquet"})
	export, err := lifecycle.ExportSegment(lifecycle.ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   code,
		StartDate:    "20230101",
		EndDate:      "20230131",
		Storage:      lifecycle.NewLocalColdStorage(root),
		ColdURI:      uri,
	})
	if err != nil {
		t.Fatalf("export test segment: %v", err)
	}
	return export, uri
}

func createRestoreManifestSegment(t *testing.T, root string, segment lifecycle.ColdSegment) {
	t.Helper()
	if segment.CreatedAt.IsZero() {
		segment.CreatedAt = time.Now()
	}
	if segment.UpdatedAt.IsZero() {
		segment.UpdatedAt = segment.CreatedAt
	}
	manifest, err := lifecycle.OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()
	if err := manifest.CreateSegment(segment); err != nil {
		t.Fatalf("create segment: %v", err)
	}
}
