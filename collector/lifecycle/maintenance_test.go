package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testMaintenanceRunner(root string, manifest *ManifestStore, candidates []LifecycleCandidate) MaintenanceRunner {
	return MaintenanceRunner{
		MaintenanceResources: MaintenanceResources{
			Manifest:   manifest,
			Storage:    NewLocalColdStorage(filepath.Join(root, "cold")),
			Candidates: candidates,
		},
		MaintenancePaths: MaintenancePaths{
			DataDir: filepath.Join(root, "data"),
			Dataset: "a-stock-market-tdx",
		},
		MaintenancePolicy: MaintenancePolicy{
			Enable:              true,
			AllowPrune:          true,
			MinVerifiedSegments: 1,
		},
		MaintenanceRuntime: MaintenanceRuntime{
			RuntimeBudget:     time.Minute,
			FreeBytes:         10_000_000,
			SafetyMarginBytes: 1,
		},
		MaintenanceLimits: MaintenanceLimits{
			HotCutoffDate: "20260401",
		},
	}
}

func TestMaintenanceRunnerArchivesRestoreTestsAndPrunesCandidate(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`CREATE INDEX idx_trade_history_date ON TradeHistory(TradeDate, Seq)`,
		`CREATE TABLE collector_trade_history_staging(Code TEXT, TradeDate TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
		`INSERT INTO collector_trade_history_staging VALUES('sh600000','20230102')`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	result, err := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "trade",
		TableName:                 "TradeHistory",
		Instrument:                "sh600000",
		MinDate:                   "20230102",
		MaxDate:                   "20260424",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}}).RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "passed" || result.ProcessedSegments != 1 || result.PrunedSegments != 1 || result.RowsArchived != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	remaining, err := countRows(sourceDB, "TradeHistory")
	if err != nil {
		t.Fatalf("count remaining rows: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining rows = %d, want 1 hot row", remaining)
	}
	stagingRows, err := countRows(sourceDB, "collector_trade_history_staging")
	if err != nil {
		t.Fatalf("count preserved table: %v", err)
	}
	if stagingRows != 1 {
		t.Fatalf("preserved table rows = %d, want 1", stagingRows)
	}
	if matches, _ := filepath.Glob(sourceDB + ".pre_lifecycle.*.bak"); len(matches) != 0 {
		t.Fatalf("backup should be verified and removed after active segment, got %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "data", "cold_restore", "*.db")); len(matches) != 0 {
		t.Fatalf("restore-test db should be removed by default, got %v", matches)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 1 || segments[0].Status != SegmentActive || segments[0].RowCount != 1 {
		t.Fatalf("unexpected segments: %+v", segments)
	}
	coldRows, err := ReadTradeHistoryRows(NewLocalColdStorage(filepath.Join(root, "cold")), segments[0].ColdURI)
	if err != nil {
		t.Fatalf("read cold rows: %v", err)
	}
	if len(coldRows) != 1 || coldRows[0].TradeDate != "20230102" {
		t.Fatalf("unexpected cold rows: %+v", coldRows)
	}
}

func TestMaintenanceRunnerUsesCandidateHotCutoff(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260301',1772335800,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260501',1777599000,1,12000,20,2,0,'S')`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	runner := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "trade",
		TableName:                 "TradeHistory",
		Instrument:                "sh600000",
		MinDate:                   "20260301",
		MaxDate:                   "20260501",
		HotCutoffDate:             "20260401",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}})
	runner.HotCutoffDate = "20250101"

	result, err := runner.RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "passed" || result.RowsArchived != 1 || result.PrunedSegments != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	db, err := openLifecycleSQLiteReadOnly(sourceDB)
	if err != nil {
		t.Fatalf("open pruned db: %v", err)
	}
	defer db.Close()
	var tradeDate string
	if err := db.QueryRow(`SELECT TradeDate FROM TradeHistory`).Scan(&tradeDate); err != nil {
		t.Fatalf("query remaining row: %v", err)
	}
	if tradeDate != "20260501" {
		t.Fatalf("remaining trade date = %s, want 20260501", tradeDate)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 1 || segments[0].EndDate != "20260301" {
		t.Fatalf("unexpected segment: %+v", segments)
	}
}

func TestMaintenanceRunnerArchivesAuctionSnapshotAndPrunesCandidate(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "auction", "auction.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE AuctionSnapshot(TradeDate TEXT, SnapshotTime TEXT, InstrumentCode TEXT, Name TEXT, AuctionPrice REAL, AuctionAmount REAL, PrevClose REAL, AuctionPct REAL, Bid1Price REAL, Bid1Volume INTEGER, Ask1Price REAL, Ask1Volume INTEGER, IsLimitUpOpen INTEGER, IsLimitDownOpen INTEGER, CollectedAt INTEGER)`,
		`CREATE INDEX idx_auction_snapshot_date ON AuctionSnapshot(TradeDate, SnapshotTime)`,
		`INSERT INTO AuctionSnapshot VALUES('2023-01-02','09:20:00','sh600000','PF Bank',11.0,1000000,10.0,10.0,11.0,1000,0,0,1,0,1672622400)`,
		`INSERT INTO AuctionSnapshot VALUES('2026-04-24','09:25:00','sh600000','PF Bank',12.0,1200000,11.0,9.09,12.0,2000,0,0,0,0,1776993900)`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	result, err := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "auction",
		TableName:                 "AuctionSnapshot",
		Instrument:                "",
		MinDate:                   "20230102",
		MaxDate:                   "20260424",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}}).RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "passed" || result.ProcessedSegments != 1 || result.PrunedSegments != 1 || result.RowsArchived != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	rows, err := countRows(sourceDB, "AuctionSnapshot")
	if err != nil {
		t.Fatalf("count remaining rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("remaining auction rows = %d, want 1 hot row", rows)
	}
	db, err := openLifecycleSQLiteReadOnly(sourceDB)
	if err != nil {
		t.Fatalf("open pruned auction db: %v", err)
	}
	defer db.Close()
	var tradeDate string
	if err := db.QueryRow(`SELECT TradeDate FROM AuctionSnapshot`).Scan(&tradeDate); err != nil {
		t.Fatalf("query remaining auction row: %v", err)
	}
	if tradeDate != "2026-04-24" {
		t.Fatalf("remaining auction date = %s, want 2026-04-24", tradeDate)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 1 || segments[0].Domain != "auction" || segments[0].TableName != "AuctionSnapshot" || segments[0].Status != SegmentActive {
		t.Fatalf("unexpected auction segment: %+v", segments)
	}
}

func TestMaintenanceRunnerDoesNotPruneWhenPruneSwitchDisabled(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "order_history", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE OrderHistory(Code TEXT, TradeDate TEXT, Seq INTEGER, Price INTEGER, BuySellDelta INTEGER, Volume INTEGER)`,
		`INSERT INTO OrderHistory VALUES('sh600000','20230102',1,11000,-10,100)`,
		`INSERT INTO OrderHistory VALUES('sh600000','20260424',1,12000,10,200)`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	runner := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "order_history",
		TableName:                 "OrderHistory",
		Instrument:                "sh600000",
		MinDate:                   "20230102",
		MaxDate:                   "20260424",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}})
	runner.AllowPrune = false
	result, err := runner.RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "partial" || result.ProcessedSegments != 1 || result.PrunedSegments != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	rows, err := countRows(sourceDB, "OrderHistory")
	if err != nil {
		t.Fatalf("count source rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("source rows = %d, want unchanged 2", rows)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 1 || segments[0].Status != SegmentRestoreTested {
		t.Fatalf("unexpected segment status: %+v", segments)
	}
}

func TestMaintenanceRunnerPrunesMultipleTablesInOneDBReplacement(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "live", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE MinuteLive(Code TEXT, TradeDate TEXT, Clock TEXT, Price INTEGER, Number INTEGER)`,
		`CREATE TABLE TradeLive(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO MinuteLive VALUES('sh600000','20230102','09:31',11000,1)`,
		`INSERT INTO MinuteLive VALUES('sh600000','20260424','09:31',12000,2)`,
		`INSERT INTO TradeLive VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeLive VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	result, err := testMaintenanceRunner(root, manifest, []LifecycleCandidate{
		{
			DBPath:                    sourceDB,
			Domain:                    "live",
			TableName:                 "MinuteLive",
			Instrument:                "sh600000",
			MinDate:                   "20230102",
			MaxDate:                   "20260424",
			SourceDBBytes:             sourceStat.Size(),
			ColdRowShare:              0.5,
			EstimatedParquetRatio:     0.4,
			EstimatedReplacementRatio: 0.5,
		},
		{
			DBPath:                    sourceDB,
			Domain:                    "live",
			TableName:                 "TradeLive",
			Instrument:                "sh600000",
			MinDate:                   "20230102",
			MaxDate:                   "20260424",
			SourceDBBytes:             sourceStat.Size(),
			ColdRowShare:              0.5,
			EstimatedParquetRatio:     0.4,
			EstimatedReplacementRatio: 0.5,
		},
	}).RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.Status != "passed" || result.ProcessedSegments != 2 || result.PrunedSegments != 2 || result.RowsArchived != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	minuteRows, err := countRows(sourceDB, "MinuteLive")
	if err != nil {
		t.Fatalf("count minute rows: %v", err)
	}
	tradeRows, err := countRows(sourceDB, "TradeLive")
	if err != nil {
		t.Fatalf("count trade rows: %v", err)
	}
	if minuteRows != 1 || tradeRows != 1 {
		t.Fatalf("hot rows = MinuteLive:%d TradeLive:%d, want 1 each", minuteRows, tradeRows)
	}
	if matches, _ := filepath.Glob(sourceDB + ".pre_lifecycle.*.bak"); len(matches) != 0 {
		t.Fatalf("backup should be removed after grouped replacement, got %v", matches)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(segments))
	}
	for _, segment := range segments {
		if segment.Status != SegmentActive {
			t.Fatalf("segment not active: %+v", segment)
		}
	}
}

func TestMaintenanceRunnerCanRetainRestoreTestDBWhenRequested(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	runner := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "trade",
		TableName:                 "TradeHistory",
		Instrument:                "sh600000",
		MinDate:                   "20230102",
		MaxDate:                   "20260424",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}})
	runner.AllowPrune = false
	runner.KeepRestoreTestDB = true
	result, err := runner.RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.ProcessedSegments != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "data", "cold_restore", "*.db")); len(matches) != 1 {
		t.Fatalf("restore-test db should be retained, got %v", matches)
	}
}

func TestMaintenanceRunnerPrunesOnlyArchivedDateSpanWhenBatchIsCapped(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,11500,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	sourceStat, err := os.Stat(sourceDB)
	if err != nil {
		t.Fatalf("source stat: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	runner := testMaintenanceRunner(root, manifest, []LifecycleCandidate{{
		DBPath:                    sourceDB,
		Domain:                    "trade",
		TableName:                 "TradeHistory",
		Instrument:                "sh600000",
		MinDate:                   "20230102",
		MaxDate:                   "20260424",
		SourceDBBytes:             sourceStat.Size(),
		ColdRowShare:              0.5,
		EstimatedParquetRatio:     0.4,
		EstimatedReplacementRatio: 0.5,
	}})
	runner.MaxArchiveDays = 365
	result, err := runner.RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run: %v", err)
	}
	if result.PrunedSegments != 1 || result.RowsArchived != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	rows, err := countRows(sourceDB, "TradeHistory")
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows after capped prune = %d, want 2024 cold debt plus 2026 hot", rows)
	}
	segments, err := manifest.ListSegments()
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segments) != 1 || segments[0].StartDate != "20230102" || segments[0].EndDate != "20230102" {
		t.Fatalf("unexpected capped segment: %+v", segments)
	}
}

func TestMaintenanceRunnerDiscoversSmallCandidateWithoutScanningLargeInvalidDB(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "data", "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	largeInvalidDB := filepath.Join(root, "data", "trade", "sh999999.db")
	if err := os.WriteFile(largeInvalidDB, make([]byte, 1024*1024), 0o644); err != nil {
		t.Fatalf("write invalid db: %v", err)
	}
	manifest, err := OpenManifestStore(filepath.Join(root, "cold_manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifest.Close()

	runner := testMaintenanceRunner(root, manifest, nil)
	runner.MaxCandidates = 1
	runner.MaxInventoryFiles = 1
	result, err := runner.RunWithContext(context.Background())
	if err != nil {
		t.Fatalf("maintenance run should not scan large invalid db: %v", err)
	}
	if result.Status != "passed" || result.PrunedSegments != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDiscoverLifecycleCandidatesCanSortLargestFirst(t *testing.T) {
	root := t.TempDir()
	smallDB := filepath.Join(root, "data", "trade", "sh600001.db")
	largeDB := filepath.Join(root, "data", "trade", "sh600002.db")
	mustCreateSQLite(t, smallDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600001','20230102',1672623000,1,11000,10,1,0,'B')`,
	})
	mustCreateSQLite(t, largeDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`CREATE TABLE filler(x BLOB)`,
		`INSERT INTO filler VALUES(zeroblob(1048576))`,
		`INSERT INTO TradeHistory VALUES('sh600002','20230102',1672623000,1,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600002','20230103',1672709400,2,11000,10,1,0,'B')`,
		`INSERT INTO TradeHistory VALUES('sh600002','20230104',1672795800,3,11000,10,1,0,'B')`,
	})

	candidates, err := DiscoverLifecycleCandidates(context.Background(), filepath.Join(root, "data"), "20260401", CandidateDiscoveryOptions{
		MaxCandidates: 1,
		Sort:          "size_desc",
	})
	if err != nil {
		t.Fatalf("discover candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Instrument != "sh600002" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestDiscoverLifecycleCandidatesCanFilterDomains(t *testing.T) {
	root := t.TempDir()
	tradeDB := filepath.Join(root, "data", "trade", "sh600001.db")
	liveDB := filepath.Join(root, "data", "live", "sh600001.db")
	mustCreateSQLite(t, tradeDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600001','20230102',1672623000,1,11000,10,1,0,'B')`,
	})
	mustCreateSQLite(t, liveDB, []string{
		`CREATE TABLE TradeLive(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeLive VALUES('sh600001','20230102',1672623000,1,11000,10,1,0,'B')`,
	})

	candidates, err := DiscoverLifecycleCandidates(context.Background(), filepath.Join(root, "data"), "20260401", CandidateDiscoveryOptions{
		Domains: []string{"trade"},
	})
	if err != nil {
		t.Fatalf("discover candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Domain != "trade" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestRehydrateSegmentToHotPathRestoresPrunedRows(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "trade", "sh600000.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20260424',1776994200,1,12000,20,2,0,'S')`,
	})
	coldSource := filepath.Join(root, "cold-source.db")
	mustCreateSQLite(t, coldSource, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20230102',1672623000,1,11000,10,1,0,'B')`,
	})
	storage := NewLocalColdStorage(filepath.Join(root, "cold"))
	uri := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2023/part-rehydrate.parquet"})
	export, err := ExportSegment(ExportRequest{
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

	result, err := RehydrateSegmentToHotPath(export, sourceDB)
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	if result.RowCount != 1 || result.TargetDBPath != sourceDB {
		t.Fatalf("unexpected rehydrate result: %+v", result)
	}
	rows, err := countRows(sourceDB, "TradeHistory")
	if err != nil {
		t.Fatalf("count rehydrated rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows after rehydrate = %d, want 2", rows)
	}
}
