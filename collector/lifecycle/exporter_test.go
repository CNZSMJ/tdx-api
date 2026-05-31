package lifecycle

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestExportVerifyAndRestoreStageOneSegments(t *testing.T) {
	loc := mustShanghai(t)
	root := t.TempDir()
	cold := NewLocalColdStorage(filepath.Join(root, "cold-root"))

	cases := []struct {
		name       string
		domain     string
		table      string
		sourceDB   string
		setupSQL   []string
		startDate  string
		endDate    string
		wantRows   int64
		verifyRows func(t *testing.T, restoredDB string)
	}{
		{
			name:     "trade history",
			domain:   "trade",
			table:    "TradeHistory",
			sourceDB: filepath.Join(root, "trade.db"),
			setupSQL: []string{
				`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
				`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
				`INSERT INTO TradeHistory VALUES('sh600000','20240103',1704249000,1,12100,20,2,1,'S')`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  2,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "TradeHistory")
				if err != nil || got != 2 {
					t.Fatalf("restored trade rows=%d err=%v", got, err)
				}
			},
		},
		{
			name:     "trade live",
			domain:   "live",
			table:    "TradeLive",
			sourceDB: filepath.Join(root, "live_trade.db"),
			setupSQL: []string{
				`CREATE TABLE TradeLive(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
				`INSERT INTO TradeLive VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  1,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "TradeLive")
				if err != nil || got != 1 {
					t.Fatalf("restored live rows=%d err=%v", got, err)
				}
			},
		},
		{
			name:     "trade minute bar",
			domain:   "trade",
			table:    "TradeMinute5Bar",
			sourceDB: filepath.Join(root, "trade_bar.db"),
			setupSQL: []string{
				`CREATE TABLE TradeMinute5Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
				`INSERT INTO TradeMinute5Bar VALUES('sh600000','20240102',1704162600,12000,12100,11900,12050,100,1205000)`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  1,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "TradeMinute5Bar")
				if err != nil || got != 1 {
					t.Fatalf("restored trade bar rows=%d err=%v", got, err)
				}
			},
		},
		{
			name:     "quote snapshot",
			domain:   "live",
			table:    "QuoteSnapshot",
			sourceDB: filepath.Join(root, "quotes.db"),
			setupSQL: []string{
				`CREATE TABLE QuoteSnapshot(Code TEXT, CaptureTime INTEGER, Last INTEGER, PreClose INTEGER, Open INTEGER, High INTEGER, Low INTEGER, VolumeHand INTEGER, AmountYuan REAL)`,
				`INSERT INTO QuoteSnapshot VALUES('sh600000',` + itoa(time.Date(2024, 1, 2, 9, 30, 0, 0, loc).Unix()) + `,12000,11900,11950,12100,11900,1000,120000.5)`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  1,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "QuoteSnapshot")
				if err != nil || got != 1 {
					t.Fatalf("restored quote rows=%d err=%v", got, err)
				}
			},
		},
		{
			name:     "order history",
			domain:   "order_history",
			table:    "OrderHistory",
			sourceDB: filepath.Join(root, "order.db"),
			setupSQL: []string{
				`CREATE TABLE OrderHistory(Code TEXT, TradeDate TEXT, Seq INTEGER, Price INTEGER, BuySellDelta INTEGER, Volume INTEGER)`,
				`INSERT INTO OrderHistory VALUES('sh600000','20240102',1,12000,-10,100)`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  1,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "OrderHistory")
				if err != nil || got != 1 {
					t.Fatalf("restored order rows=%d err=%v", got, err)
				}
			},
		},
		{
			name:     "auction snapshot",
			domain:   "auction",
			table:    "AuctionSnapshot",
			sourceDB: filepath.Join(root, "auction.db"),
			setupSQL: []string{
				`CREATE TABLE AuctionSnapshot(TradeDate TEXT, SnapshotTime TEXT, InstrumentCode TEXT, Name TEXT, AuctionPrice REAL, AuctionAmount REAL, PrevClose REAL, AuctionPct REAL, Bid1Price REAL, Bid1Volume INTEGER, Ask1Price REAL, Ask1Volume INTEGER, IsLimitUpOpen INTEGER, IsLimitDownOpen INTEGER, CollectedAt INTEGER)`,
				`INSERT INTO AuctionSnapshot VALUES('2024-01-02','09:20:00','sh600000','浦发银行',12.3,1230000.5,12.0,2.5,12.3,1000,12.31,2000,0,0,1704162600)`,
			},
			startDate: "20240101",
			endDate:   "20240131",
			wantRows:  1,
			verifyRows: func(t *testing.T, restoredDB string) {
				got, err := countRows(restoredDB, "AuctionSnapshot")
				if err != nil || got != 1 {
					t.Fatalf("restored auction rows=%d err=%v", got, err)
				}
				db, err := openLifecycleSQLiteReadOnly(restoredDB)
				if err != nil {
					t.Fatalf("open restored auction db: %v", err)
				}
				defer db.Close()
				var tradeDate string
				var auctionPct float64
				if err := db.QueryRow(`SELECT TradeDate, AuctionPct FROM AuctionSnapshot WHERE InstrumentCode='sh600000'`).Scan(&tradeDate, &auctionPct); err != nil {
					t.Fatalf("query restored auction row: %v", err)
				}
				if tradeDate != "2024-01-02" || auctionPct != 2.5 {
					t.Fatalf("unexpected restored auction row date=%s pct=%v", tradeDate, auctionPct)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustCreateSQLite(t, tc.sourceDB, tc.setupSQL)
			before, err := countRows(tc.sourceDB, tc.table)
			if err != nil {
				t.Fatalf("source count before: %v", err)
			}
			uri := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=" + tc.domain + "/table=" + tc.table + "/instrument=sh600000/year=2024/part-test-000.parquet"})
			result, err := ExportSegment(ExportRequest{
				SourceDBPath: tc.sourceDB,
				TableName:    tc.table,
				Domain:       tc.domain,
				Instrument:   "sh600000",
				StartDate:    tc.startDate,
				EndDate:      tc.endDate,
				Storage:      cold,
				ColdURI:      uri,
			})
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			if result.RowCount != tc.wantRows || result.FileChecksum == "" || result.LogicalChecksum == "" {
				t.Fatalf("unexpected export result: %+v", result)
			}
			if err := VerifySegment(result); err != nil {
				t.Fatalf("verify: %v", err)
			}
			after, err := countRows(tc.sourceDB, tc.table)
			if err != nil {
				t.Fatalf("source count after: %v", err)
			}
			if after != before {
				t.Fatalf("export mutated source count from %d to %d", before, after)
			}

			restoredDB := filepath.Join(root, tc.name+"-restore.db")
			restore, err := RestoreSegment(result, restoredDB)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if restore.RowCount != tc.wantRows {
				t.Fatalf("restore row count = %d, want %d", restore.RowCount, tc.wantRows)
			}
			tc.verifyRows(t, restoredDB)
		})
	}
}

type readerOnlyColdStorage struct {
	object []byte
}

func (s *readerOnlyColdStorage) Put(uri string, data []byte) error {
	return errors.New("buffered Put must not be used for segment export")
}

func (s *readerOnlyColdStorage) PutReader(uri string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.object = data
	return nil
}

func (s *readerOnlyColdStorage) Get(uri string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.object)), nil
}

func (s *readerOnlyColdStorage) Exists(uri string) (bool, error) {
	return len(s.object) > 0, nil
}

func (s *readerOnlyColdStorage) Stat(uri string) (ColdObjectInfo, error) {
	return ColdObjectInfo{URI: uri, Size: int64(len(s.object))}, nil
}

func (s *readerOnlyColdStorage) Delete(uri string) error {
	s.object = nil
	return nil
}

func (s *readerOnlyColdStorage) List(prefix string) ([]string, error) {
	if len(s.object) == 0 {
		return nil, nil
	}
	return []string{"memory-object"}, nil
}

func (s *readerOnlyColdStorage) Rename(src, dst string) error {
	return nil
}

type stagingAwareColdStorage struct {
	readerOnlyColdStorage
	stagingDir       string
	stagingDirCalled bool
}

func (s *stagingAwareColdStorage) StagingDir(uri string) (string, error) {
	s.stagingDirCalled = true
	return s.stagingDir, nil
}

func TestExportSegmentUsesStreamingColdStoragePut(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "trade.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	storage := &readerOnlyColdStorage{}
	result, err := ExportSegment(ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      storage,
		ColdURI:      MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-stream.parquet"}),
	})
	if err != nil {
		t.Fatalf("export should use streaming storage path: %v", err)
	}
	if result.RowCount != 1 || len(storage.object) == 0 {
		t.Fatalf("unexpected streaming export result=%+v object_bytes=%d", result, len(storage.object))
	}
}

func TestExportSegmentUsesColdStorageStagingDir(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "trade.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	storage := &stagingAwareColdStorage{stagingDir: filepath.Join(root, "cold-root", ".staging")}
	result, err := ExportSegment(ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      storage,
		ColdURI:      MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-staging.parquet"}),
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.RowCount != 1 || !storage.stagingDirCalled {
		t.Fatalf("export did not use cold storage staging dir: result=%+v called=%v", result, storage.stagingDirCalled)
	}
}

func TestExportedParquetUsesVersionedPlanColumnNames(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "trade.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	storage := &readerOnlyColdStorage{}
	if _, err := ExportSegment(ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      storage,
		ColdURI:      MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-schema.parquet"}),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	file, err := parquet.OpenFile(bytes.NewReader(storage.object), int64(len(storage.object)))
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	got := parquetLeafColumns(file.Schema())
	if !reflect.DeepEqual(got, []string{"schema_version", "code", "trade_date", "trade_time", "seq", "price_milli", "volume_hand", "number", "status_code", "side"}) {
		t.Fatalf("parquet columns = %v", got)
	}
}

func TestVerifySegmentFailureInjection(t *testing.T) {
	root := t.TempDir()
	cold := NewLocalColdStorage(filepath.Join(root, "cold-root"))
	sourceDB := filepath.Join(root, "trade.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	uri := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-test-000.parquet"})
	result, err := ExportSegment(ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      cold,
		ColdURI:      uri,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := cold.Delete(uri); err != nil {
		t.Fatalf("delete exported file: %v", err)
	}
	if err := VerifySegment(result); !errors.Is(err, ErrColdFileMissing) {
		t.Fatalf("missing file verify err=%v", err)
	}

	result, err = ExportSegment(ExportRequest{
		SourceDBPath: sourceDB,
		TableName:    "TradeHistory",
		Domain:       "trade",
		Instrument:   "sh600000",
		StartDate:    "20240101",
		EndDate:      "20240131",
		Storage:      cold,
		ColdURI:      uri,
	})
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if err := cold.Put(uri, []byte("corrupt")); err != nil {
		t.Fatalf("corrupt cold file: %v", err)
	}
	if err := VerifySegment(result); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("checksum verify err=%v", err)
	}
}

func parquetLeafColumns(schema *parquet.Schema) []string {
	columns := schema.Columns()
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if len(column) == 0 {
			continue
		}
		out = append(out, column[len(column)-1])
	}
	return out
}

func TestExportInterruptedBeforeFinalizeDoesNotPublishFinalURI(t *testing.T) {
	root := t.TempDir()
	cold := NewLocalColdStorage(filepath.Join(root, "cold-root"))
	sourceDB := filepath.Join(root, "trade.db")
	mustCreateSQLite(t, sourceDB, []string{
		`CREATE TABLE TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		`INSERT INTO TradeHistory VALUES('sh600000','20240102',1704162600,1,12000,10,1,0,'B')`,
	})
	uri := MustFormatColdURI(ColdURI{Dataset: "a-stock-market-tdx", Path: "cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-test-000.parquet"})
	_, err := ExportSegment(ExportRequest{
		SourceDBPath:                sourceDB,
		TableName:                   "TradeHistory",
		Domain:                      "trade",
		Instrument:                  "sh600000",
		StartDate:                   "20240101",
		EndDate:                     "20240131",
		Storage:                     cold,
		ColdURI:                     uri,
		InjectFailureBeforeFinalize: true,
	})
	if err == nil {
		t.Fatalf("expected injected export failure")
	}
	if ok, existsErr := cold.Exists(uri); existsErr != nil || ok {
		t.Fatalf("final uri exists=%v err=%v after interrupted export", ok, existsErr)
	}
}
