package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	_ "github.com/glebarez/go-sqlite"
	"github.com/parquet-go/parquet-go"
)

var ErrColdFileMissing = errors.New("cold file missing")
var ErrChecksumMismatch = errors.New("cold segment checksum mismatch")

type ExportRequest struct {
	SourceDBPath                string
	TableName                   string
	Domain                      string
	Instrument                  string
	StartDate                   string
	EndDate                     string
	Storage                     ColdStorage
	ColdURI                     string
	InjectFailureBeforeFinalize bool
}

type SegmentExportResult struct {
	SourceDBPath    string
	TableName       string
	Domain          string
	Instrument      string
	StartDate       string
	EndDate         string
	ColdURI         string
	Storage         ColdStorage
	RowCount        int64
	ByteSize        int64
	MinDate         string
	MaxDate         string
	FileChecksum    string
	LogicalChecksum string
	SchemaVersion   int
}

type RestoreResult struct {
	TargetDBPath string
	TableName    string
	RowCount     int64
}

type coldStorageStager interface {
	StagingDir(uri string) (string, error)
}

type ColdTradeHistoryRow struct {
	Code       string `json:"code"`
	TradeDate  string `json:"trade_date"`
	TradeTime  int64  `json:"trade_time"`
	Seq        int64  `json:"seq"`
	Price      int64  `json:"price"`
	VolumeHand int64  `json:"volume_hand"`
	Number     int64  `json:"number"`
	StatusCode int64  `json:"status_code"`
	Side       string `json:"side"`
}

type tradeHistoryArchiveRow struct {
	SchemaVersion int32  `parquet:"schema_version,zstd"`
	Code          string `parquet:"code,zstd"`
	TradeDate     string `parquet:"trade_date,zstd"`
	TradeTime     int64  `parquet:"trade_time,zstd"`
	Seq           int64  `parquet:"seq,zstd"`
	Price         int64  `parquet:"price_milli,zstd"`
	VolumeHand    int64  `parquet:"volume_hand,zstd"`
	Number        int64  `parquet:"number,zstd"`
	StatusCode    int64  `parquet:"status_code,zstd"`
	Side          string `parquet:"side,zstd"`
}

type tradeBarArchiveRow struct {
	SchemaVersion int32  `parquet:"schema_version,zstd"`
	Code          string `parquet:"code,zstd"`
	TradeDate     string `parquet:"trade_date,zstd"`
	BucketTime    int64  `parquet:"bucket_time,zstd"`
	Open          int64  `parquet:"open_milli,zstd"`
	High          int64  `parquet:"high_milli,zstd"`
	Low           int64  `parquet:"low_milli,zstd"`
	Close         int64  `parquet:"close_milli,zstd"`
	VolumeHand    int64  `parquet:"volume_hand,zstd"`
	Amount        int64  `parquet:"amount_milli,zstd"`
}

type minuteLiveArchiveRow struct {
	SchemaVersion int32  `parquet:"schema_version,zstd"`
	Code          string `parquet:"code,zstd"`
	TradeDate     string `parquet:"trade_date,zstd"`
	Clock         string `parquet:"clock,zstd"`
	Price         int64  `parquet:"price_milli,zstd"`
	Number        int64  `parquet:"number,zstd"`
}

type quoteSnapshotArchiveRow struct {
	SchemaVersion int32   `parquet:"schema_version,zstd"`
	Code          string  `parquet:"code,zstd"`
	CaptureTime   int64   `parquet:"capture_time,zstd"`
	CaptureDate   string  `parquet:"capture_date,zstd"`
	Last          int64   `parquet:"last_milli,zstd"`
	PreClose      int64   `parquet:"pre_close_milli,zstd"`
	Open          int64   `parquet:"open_milli,zstd"`
	High          int64   `parquet:"high_milli,zstd"`
	Low           int64   `parquet:"low_milli,zstd"`
	VolumeHand    int64   `parquet:"volume_hand,zstd"`
	AmountYuan    float64 `parquet:"amount_yuan,zstd"`
}

type orderHistoryArchiveRow struct {
	SchemaVersion int32  `parquet:"schema_version,zstd"`
	Code          string `parquet:"code,zstd"`
	TradeDate     string `parquet:"trade_date,zstd"`
	Seq           int64  `parquet:"seq,zstd"`
	Price         int64  `parquet:"price_milli,zstd"`
	BuySellDelta  int64  `parquet:"buy_sell_delta,zstd"`
	Volume        int64  `parquet:"volume,zstd"`
}

type auctionSnapshotArchiveRow struct {
	SchemaVersion   int32   `parquet:"schema_version,zstd"`
	TradeDate       string  `parquet:"trade_date,zstd"`
	SnapshotTime    string  `parquet:"snapshot_time,zstd"`
	InstrumentCode  string  `parquet:"instrument_code,zstd"`
	Name            string  `parquet:"name,zstd"`
	AuctionPrice    float64 `parquet:"auction_price,zstd"`
	AuctionAmount   float64 `parquet:"auction_amount,zstd"`
	PrevClose       float64 `parquet:"prev_close,zstd"`
	AuctionPct      float64 `parquet:"auction_pct,zstd"`
	Bid1Price       float64 `parquet:"bid1_price,zstd"`
	Bid1Volume      int64   `parquet:"bid1_volume,zstd"`
	Ask1Price       float64 `parquet:"ask1_price,zstd"`
	Ask1Volume      int64   `parquet:"ask1_volume,zstd"`
	IsLimitUpOpen   bool    `parquet:"is_limit_up_open,zstd"`
	IsLimitDownOpen bool    `parquet:"is_limit_down_open,zstd"`
	CollectedAt     int64   `parquet:"collected_at,zstd"`
}

func ExportSegment(req ExportRequest) (SegmentExportResult, error) {
	if req.Storage == nil {
		return SegmentExportResult{}, errors.New("cold storage is required")
	}
	if req.ColdURI == "" {
		return SegmentExportResult{}, errors.New("cold uri is required")
	}
	stagingDir, err := exportStagingDir(req.Storage, req.ColdURI)
	if err != nil {
		return SegmentExportResult{}, err
	}
	tmp, err := os.CreateTemp(stagingDir, "tdx-lifecycle-export-*.parquet")
	if err != nil {
		return SegmentExportResult{}, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	result := SegmentExportResult{
		SourceDBPath:  req.SourceDBPath,
		TableName:     req.TableName,
		Domain:        req.Domain,
		Instrument:    req.Instrument,
		StartDate:     req.StartDate,
		EndDate:       req.EndDate,
		ColdURI:       req.ColdURI,
		Storage:       req.Storage,
		SchemaVersion: 1,
	}
	rowsForChecksum, err := writeTableParquet(req, tmpPath, &result)
	if err != nil {
		return SegmentExportResult{}, err
	}
	schema := StageOneParquetSchemas()[schemaNameForTable(req.TableName)]
	result.LogicalChecksum, err = LogicalChecksum(rowsForChecksum, schema.StableKeys, parquetFieldOrder(schema))
	if err != nil {
		return SegmentExportResult{}, err
	}
	result.FileChecksum, err = FileChecksum(tmpPath)
	if err != nil {
		return SegmentExportResult{}, err
	}
	if stat, err := os.Stat(tmpPath); err == nil {
		result.ByteSize = stat.Size()
	}
	if req.InjectFailureBeforeFinalize {
		return SegmentExportResult{}, errors.New("injected export failure before finalize")
	}
	file, err := os.Open(tmpPath)
	if err != nil {
		return SegmentExportResult{}, err
	}
	defer file.Close()
	if err := req.Storage.PutReader(req.ColdURI, file); err != nil {
		return SegmentExportResult{}, err
	}
	return result, nil
}

func exportStagingDir(storage ColdStorage, uri string) (string, error) {
	stager, ok := storage.(coldStorageStager)
	if !ok {
		return "", nil
	}
	dir, err := stager.StagingDir(uri)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func VerifySegment(result SegmentExportResult) error {
	data, err := readColdObject(result.Storage, result.ColdURI)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != result.FileChecksum {
		return fmt.Errorf("%w: file checksum", ErrChecksumMismatch)
	}
	rows, err := readSegmentRows(result.TableName, data)
	if err != nil {
		return err
	}
	if int64(len(rows)) != result.RowCount {
		return fmt.Errorf("%w: row count got %d want %d", ErrChecksumMismatch, len(rows), result.RowCount)
	}
	schema := StageOneParquetSchemas()[schemaNameForTable(result.TableName)]
	logical, err := LogicalChecksum(rows, schema.StableKeys, parquetFieldOrder(schema))
	if err != nil {
		return err
	}
	if logical != result.LogicalChecksum {
		return fmt.Errorf("%w: logical checksum", ErrChecksumMismatch)
	}
	return nil
}

func RestoreSegment(result SegmentExportResult, targetDBPath string) (RestoreResult, error) {
	data, err := readColdObject(result.Storage, result.ColdURI)
	if err != nil {
		return RestoreResult{}, err
	}
	rows, err := readSegmentRows(result.TableName, data)
	if err != nil {
		return RestoreResult{}, err
	}
	db, err := openLifecycleSQLite(targetDBPath)
	if err != nil {
		return RestoreResult{}, err
	}
	defer db.Close()
	if err := createRestoreTable(db, result.TableName); err != nil {
		return RestoreResult{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return RestoreResult{}, err
	}
	for _, row := range rows {
		if err := insertRestoreRow(tx, result.TableName, row); err != nil {
			_ = tx.Rollback()
			return RestoreResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{TargetDBPath: targetDBPath, TableName: result.TableName, RowCount: int64(len(rows))}, nil
}

func ReadTradeHistoryRows(storage ColdStorage, uri string) ([]ColdTradeHistoryRow, error) {
	data, err := readColdObject(storage, uri)
	if err != nil {
		return nil, err
	}
	rows, err := parquet.Read[tradeHistoryArchiveRow](bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]ColdTradeHistoryRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ColdTradeHistoryRow{
			Code:       row.Code,
			TradeDate:  row.TradeDate,
			TradeTime:  row.TradeTime,
			Seq:        row.Seq,
			Price:      row.Price,
			VolumeHand: row.VolumeHand,
			Number:     row.Number,
			StatusCode: row.StatusCode,
			Side:       row.Side,
		})
	}
	return out, nil
}

func writeTableParquet(req ExportRequest, path string, result *SegmentExportResult) ([]map[string]any, error) {
	db, err := openLifecycleSQLiteReadOnly(req.SourceDBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	switch req.TableName {
	case "TradeHistory", "TradeLive":
		rows, maps, err := loadTradeLikeRows(db, req.TableName, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	case "TradeMinute1Bar", "TradeMinute5Bar", "TradeMinute15Bar", "TradeMinute30Bar", "TradeMinute60Bar":
		rows, maps, err := loadTradeBarRows(db, req.TableName, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	case "MinuteLive":
		rows, maps, err := loadMinuteLiveRows(db, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	case "QuoteSnapshot":
		rows, maps, err := loadQuoteSnapshotRows(db, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	case "OrderHistory":
		rows, maps, err := loadOrderHistoryRows(db, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	case "AuctionSnapshot":
		rows, maps, err := loadAuctionSnapshotRows(db, req.StartDate, req.EndDate)
		if err != nil {
			return nil, err
		}
		fillExportRange(result, maps)
		result.RowCount = int64(len(rows))
		return maps, parquet.WriteFile(path, rows, parquet.Compression(&parquet.Zstd))
	default:
		return nil, fmt.Errorf("unsupported export table %s", req.TableName)
	}
}

func readSegmentRows(table string, data []byte) ([]map[string]any, error) {
	reader := bytes.NewReader(data)
	switch table {
	case "TradeHistory", "TradeLive":
		rows, err := parquet.Read[tradeHistoryArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, tradeLikeRowMap(row))
		}
		return out, nil
	case "TradeMinute1Bar", "TradeMinute5Bar", "TradeMinute15Bar", "TradeMinute30Bar", "TradeMinute60Bar":
		rows, err := parquet.Read[tradeBarArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, tradeBarRowMap(row))
		}
		return out, nil
	case "MinuteLive":
		rows, err := parquet.Read[minuteLiveArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, map[string]any{"schema_version": int(row.SchemaVersion), "code": row.Code, "trade_date": row.TradeDate, "clock": row.Clock, "price_milli": row.Price, "number": row.Number})
		}
		return out, nil
	case "QuoteSnapshot":
		rows, err := parquet.Read[quoteSnapshotArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, quoteRowMap(row))
		}
		return out, nil
	case "OrderHistory":
		rows, err := parquet.Read[orderHistoryArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, orderRowMap(row))
		}
		return out, nil
	case "AuctionSnapshot":
		rows, err := parquet.Read[auctionSnapshotArchiveRow](reader, int64(len(data)))
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, auctionSnapshotRowMap(row))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported segment table %s", table)
	}
}

func readColdObject(storage ColdStorage, uri string) ([]byte, error) {
	if storage == nil {
		return nil, errors.New("cold storage is required")
	}
	r, err := storage.Get(uri)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrColdFileMissing
		}
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
