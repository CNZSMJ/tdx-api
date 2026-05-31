package lifecycle

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func openLifecycleSQLiteReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro")
}

func openLifecycleSQLite(path string) (*sql.DB, error) {
	return openLifecycleSQLiteWithConfig(path, configureLifecycleWriteSQLite)
}

func openHotPathRestoreSQLite(path string) (*sql.DB, error) {
	return openLifecycleSQLiteWithConfig(path, configureHotPathRestoreSQLite)
}

func openLifecycleSQLiteWithConfig(path string, configure func(*sql.DB) error) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if configure != nil {
		if err := configure(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

func configureHotPathRestoreSQLite(db *sql.DB) error {
	for _, stmt := range []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA synchronous=FULL`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func configureLifecycleWriteSQLite(db *sql.DB) error {
	for _, stmt := range []string{
		`PRAGMA journal_mode=OFF`,
		`PRAGMA synchronous=OFF`,
		`PRAGMA temp_store=MEMORY`,
		`PRAGMA cache_size=-100000`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func loadTradeLikeRows(db *sql.DB, table, startDate, endDate string) ([]tradeHistoryArchiveRow, []map[string]any, error) {
	query := fmt.Sprintf(`SELECT Code, TradeDate, TradeTime, Seq, Price, VolumeHand, Number, StatusCode, Side FROM %s WHERE TradeDate >= ? AND TradeDate <= ? ORDER BY Code, TradeDate, TradeTime, Seq`, quoteIdent(table))
	rows, err := db.Query(query, startDate, endDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]tradeHistoryArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row tradeHistoryArchiveRow
		row.SchemaVersion = 1
		if err := rows.Scan(&row.Code, &row.TradeDate, &row.TradeTime, &row.Seq, &row.Price, &row.VolumeHand, &row.Number, &row.StatusCode, &row.Side); err != nil {
			return nil, nil, err
		}
		out = append(out, row)
		maps = append(maps, tradeLikeRowMap(row))
	}
	return out, maps, rows.Err()
}

func loadMinuteLiveRows(db *sql.DB, startDate, endDate string) ([]minuteLiveArchiveRow, []map[string]any, error) {
	rows, err := db.Query(`SELECT Code, TradeDate, Clock, Price, Number FROM MinuteLive WHERE TradeDate >= ? AND TradeDate <= ? ORDER BY Code, TradeDate, Clock`, startDate, endDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]minuteLiveArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row minuteLiveArchiveRow
		row.SchemaVersion = 1
		if err := rows.Scan(&row.Code, &row.TradeDate, &row.Clock, &row.Price, &row.Number); err != nil {
			return nil, nil, err
		}
		out = append(out, row)
		maps = append(maps, map[string]any{"schema_version": int(row.SchemaVersion), "code": row.Code, "trade_date": row.TradeDate, "clock": row.Clock, "price_milli": row.Price, "number": row.Number})
	}
	return out, maps, rows.Err()
}

func loadTradeBarRows(db *sql.DB, table, startDate, endDate string) ([]tradeBarArchiveRow, []map[string]any, error) {
	query := fmt.Sprintf(`SELECT Code, TradeDate, BucketTime, Open, High, Low, Close, VolumeHand, Amount FROM %s WHERE TradeDate >= ? AND TradeDate <= ? ORDER BY Code, TradeDate, BucketTime`, quoteIdent(table))
	rows, err := db.Query(query, startDate, endDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]tradeBarArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row tradeBarArchiveRow
		row.SchemaVersion = 1
		if err := rows.Scan(&row.Code, &row.TradeDate, &row.BucketTime, &row.Open, &row.High, &row.Low, &row.Close, &row.VolumeHand, &row.Amount); err != nil {
			return nil, nil, err
		}
		out = append(out, row)
		maps = append(maps, tradeBarRowMap(row))
	}
	return out, maps, rows.Err()
}

func loadQuoteSnapshotRows(db *sql.DB, startDate, endDate string) ([]quoteSnapshotArchiveRow, []map[string]any, error) {
	loc, err := shanghaiLocation()
	if err != nil {
		return nil, nil, err
	}
	start, err := time.ParseInLocation("20060102", startDate, loc)
	if err != nil {
		return nil, nil, err
	}
	end, err := time.ParseInLocation("20060102", endDate, loc)
	if err != nil {
		return nil, nil, err
	}
	startUnix := dayStart(start, loc).Unix()
	endUnix := dayStart(end, loc).AddDate(0, 0, 1).Unix()
	rows, err := db.Query(`SELECT Code, CaptureTime, Last, PreClose, Open, High, Low, VolumeHand, AmountYuan FROM QuoteSnapshot WHERE CaptureTime >= ? AND CaptureTime < ? ORDER BY Code, CaptureTime`, startUnix, endUnix)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]quoteSnapshotArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row quoteSnapshotArchiveRow
		row.SchemaVersion = 1
		if err := rows.Scan(&row.Code, &row.CaptureTime, &row.Last, &row.PreClose, &row.Open, &row.High, &row.Low, &row.VolumeHand, &row.AmountYuan); err != nil {
			return nil, nil, err
		}
		row.CaptureDate = time.Unix(row.CaptureTime, 0).In(loc).Format("20060102")
		out = append(out, row)
		maps = append(maps, quoteRowMap(row))
	}
	return out, maps, rows.Err()
}

func loadOrderHistoryRows(db *sql.DB, startDate, endDate string) ([]orderHistoryArchiveRow, []map[string]any, error) {
	rows, err := db.Query(`SELECT Code, TradeDate, Seq, Price, BuySellDelta, Volume FROM OrderHistory WHERE TradeDate >= ? AND TradeDate <= ? ORDER BY Code, TradeDate, Seq`, startDate, endDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]orderHistoryArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row orderHistoryArchiveRow
		row.SchemaVersion = 1
		if err := rows.Scan(&row.Code, &row.TradeDate, &row.Seq, &row.Price, &row.BuySellDelta, &row.Volume); err != nil {
			return nil, nil, err
		}
		out = append(out, row)
		maps = append(maps, orderRowMap(row))
	}
	return out, maps, rows.Err()
}

func loadAuctionSnapshotRows(db *sql.DB, startDate, endDate string) ([]auctionSnapshotArchiveRow, []map[string]any, error) {
	dateExpr := normalizedSQLiteDateExpr("TradeDate")
	query := fmt.Sprintf(`SELECT %s, SnapshotTime, InstrumentCode, COALESCE(Name, ''), AuctionPrice, AuctionAmount, PrevClose, AuctionPct, Bid1Price, Bid1Volume, Ask1Price, Ask1Volume, CASE WHEN IsLimitUpOpen THEN 1 ELSE 0 END, CASE WHEN IsLimitDownOpen THEN 1 ELSE 0 END, CollectedAt FROM AuctionSnapshot WHERE %s >= ? AND %s <= ? ORDER BY %s, SnapshotTime, InstrumentCode`, dateExpr, dateExpr, dateExpr, dateExpr)
	rows, err := db.Query(query, startDate, endDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]auctionSnapshotArchiveRow, 0)
	maps := make([]map[string]any, 0)
	for rows.Next() {
		var row auctionSnapshotArchiveRow
		var isLimitUpOpen, isLimitDownOpen int64
		row.SchemaVersion = 1
		if err := rows.Scan(
			&row.TradeDate,
			&row.SnapshotTime,
			&row.InstrumentCode,
			&row.Name,
			&row.AuctionPrice,
			&row.AuctionAmount,
			&row.PrevClose,
			&row.AuctionPct,
			&row.Bid1Price,
			&row.Bid1Volume,
			&row.Ask1Price,
			&row.Ask1Volume,
			&isLimitUpOpen,
			&isLimitDownOpen,
			&row.CollectedAt,
		); err != nil {
			return nil, nil, err
		}
		row.IsLimitUpOpen = isLimitUpOpen != 0
		row.IsLimitDownOpen = isLimitDownOpen != 0
		out = append(out, row)
		maps = append(maps, auctionSnapshotRowMap(row))
	}
	return out, maps, rows.Err()
}

func createRestoreTable(db *sql.DB, table string) error {
	stmts := map[string]string{
		"TradeHistory":     `CREATE TABLE IF NOT EXISTS TradeHistory(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		"TradeMinute1Bar":  `CREATE TABLE IF NOT EXISTS TradeMinute1Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
		"TradeMinute5Bar":  `CREATE TABLE IF NOT EXISTS TradeMinute5Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
		"TradeMinute15Bar": `CREATE TABLE IF NOT EXISTS TradeMinute15Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
		"TradeMinute30Bar": `CREATE TABLE IF NOT EXISTS TradeMinute30Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
		"TradeMinute60Bar": `CREATE TABLE IF NOT EXISTS TradeMinute60Bar(Code TEXT, TradeDate TEXT, BucketTime INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close INTEGER, VolumeHand INTEGER, Amount INTEGER)`,
		"TradeLive":        `CREATE TABLE IF NOT EXISTS TradeLive(Code TEXT, TradeDate TEXT, TradeTime INTEGER, Seq INTEGER, Price INTEGER, VolumeHand INTEGER, Number INTEGER, StatusCode INTEGER, Side TEXT)`,
		"MinuteLive":       `CREATE TABLE IF NOT EXISTS MinuteLive(Code TEXT, TradeDate TEXT, Clock TEXT, Price INTEGER, Number INTEGER)`,
		"QuoteSnapshot":    `CREATE TABLE IF NOT EXISTS QuoteSnapshot(Code TEXT, CaptureTime INTEGER, CaptureDate TEXT, Last INTEGER, PreClose INTEGER, Open INTEGER, High INTEGER, Low INTEGER, VolumeHand INTEGER, AmountYuan REAL)`,
		"OrderHistory":     `CREATE TABLE IF NOT EXISTS OrderHistory(Code TEXT, TradeDate TEXT, Seq INTEGER, Price INTEGER, BuySellDelta INTEGER, Volume INTEGER)`,
		"AuctionSnapshot":  `CREATE TABLE IF NOT EXISTS AuctionSnapshot(TradeDate TEXT, SnapshotTime TEXT, InstrumentCode TEXT, Name TEXT, AuctionPrice REAL, AuctionAmount REAL, PrevClose REAL, AuctionPct REAL, Bid1Price REAL, Bid1Volume INTEGER, Ask1Price REAL, Ask1Volume INTEGER, IsLimitUpOpen INTEGER, IsLimitDownOpen INTEGER, CollectedAt INTEGER)`,
	}
	stmt, ok := stmts[table]
	if !ok {
		return fmt.Errorf("unsupported restore table %s", table)
	}
	_, err := db.Exec(stmt)
	return err
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertRestoreRow(db sqlExecer, table string, row map[string]any) error {
	switch table {
	case "TradeHistory", "TradeLive":
		_, err := db.Exec(`INSERT INTO `+quoteIdent(table)+`(Code, TradeDate, TradeTime, Seq, Price, VolumeHand, Number, StatusCode, Side) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row["code"], row["trade_date"], row["trade_time"], row["seq"], row["price_milli"], row["volume_hand"], row["number"], row["status_code"], row["side"])
		return err
	case "MinuteLive":
		_, err := db.Exec(`INSERT INTO MinuteLive(Code, TradeDate, Clock, Price, Number) VALUES(?, ?, ?, ?, ?)`, row["code"], row["trade_date"], row["clock"], row["price_milli"], row["number"])
		return err
	case "TradeMinute1Bar", "TradeMinute5Bar", "TradeMinute15Bar", "TradeMinute30Bar", "TradeMinute60Bar":
		_, err := db.Exec(`INSERT INTO `+quoteIdent(table)+`(Code, TradeDate, BucketTime, Open, High, Low, Close, VolumeHand, Amount) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row["code"], row["trade_date"], row["bucket_time"], row["open_milli"], row["high_milli"], row["low_milli"], row["close_milli"], row["volume_hand"], row["amount_milli"])
		return err
	case "QuoteSnapshot":
		_, err := db.Exec(`INSERT INTO QuoteSnapshot(Code, CaptureTime, CaptureDate, Last, PreClose, Open, High, Low, VolumeHand, AmountYuan) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row["code"], row["capture_time"], row["capture_date"], row["last_milli"], row["pre_close_milli"], row["open_milli"], row["high_milli"], row["low_milli"], row["volume_hand"], row["amount_yuan"])
		return err
	case "OrderHistory":
		_, err := db.Exec(`INSERT INTO OrderHistory(Code, TradeDate, Seq, Price, BuySellDelta, Volume) VALUES(?, ?, ?, ?, ?, ?)`, row["code"], row["trade_date"], row["seq"], row["price_milli"], row["buy_sell_delta"], row["volume"])
		return err
	case "AuctionSnapshot":
		tradeDate, err := auctionSnapshotHotTradeDate(row["trade_date"])
		if err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO AuctionSnapshot(TradeDate, SnapshotTime, InstrumentCode, Name, AuctionPrice, AuctionAmount, PrevClose, AuctionPct, Bid1Price, Bid1Volume, Ask1Price, Ask1Volume, IsLimitUpOpen, IsLimitDownOpen, CollectedAt) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tradeDate, row["snapshot_time"], row["instrument_code"], row["name"], row["auction_price"], row["auction_amount"], row["prev_close"], row["auction_pct"], row["bid1_price"], row["bid1_volume"], row["ask1_price"], row["ask1_volume"], row["is_limit_up_open"], row["is_limit_down_open"], row["collected_at"])
		return err
	default:
		return fmt.Errorf("unsupported restore table %s", table)
	}
}

func auctionSnapshotHotTradeDate(value any) (string, error) {
	text, _ := value.(string)
	text = strings.TrimSpace(text)
	if _, err := time.Parse("2006-01-02", text); err == nil {
		return text, nil
	}
	t, err := time.Parse("20060102", text)
	if err != nil {
		return "", fmt.Errorf("invalid auction snapshot trade_date %q", text)
	}
	return t.Format("2006-01-02"), nil
}

func fillExportRange(result *SegmentExportResult, rows []map[string]any) {
	if len(rows) == 0 {
		return
	}
	dates := make([]string, 0, len(rows))
	for _, row := range rows {
		if value, ok := row["trade_date"].(string); ok {
			dates = append(dates, value)
			continue
		}
		if value, ok := row["capture_date"].(string); ok {
			dates = append(dates, value)
		}
	}
	sort.Strings(dates)
	if len(dates) > 0 {
		result.MinDate = dates[0]
		result.MaxDate = dates[len(dates)-1]
	}
}

func schemaNameForTable(table string) string {
	if table == "TradeLive" {
		return "TradeLive"
	}
	return table
}

func parquetFieldOrder(schema ParquetSchema) []string {
	out := make([]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		out = append(out, column.Name)
	}
	return out
}

func tradeLikeRowMap(row tradeHistoryArchiveRow) map[string]any {
	return map[string]any{
		"schema_version": int(row.SchemaVersion),
		"code":           row.Code,
		"trade_date":     row.TradeDate,
		"trade_time":     row.TradeTime,
		"seq":            row.Seq,
		"price_milli":    row.Price,
		"volume_hand":    row.VolumeHand,
		"number":         row.Number,
		"status_code":    row.StatusCode,
		"side":           row.Side,
	}
}

func tradeBarRowMap(row tradeBarArchiveRow) map[string]any {
	return map[string]any{
		"schema_version": int(row.SchemaVersion),
		"code":           row.Code,
		"trade_date":     row.TradeDate,
		"bucket_time":    row.BucketTime,
		"open_milli":     row.Open,
		"high_milli":     row.High,
		"low_milli":      row.Low,
		"close_milli":    row.Close,
		"volume_hand":    row.VolumeHand,
		"amount_milli":   row.Amount,
	}
}

func quoteRowMap(row quoteSnapshotArchiveRow) map[string]any {
	return map[string]any{
		"schema_version":  int(row.SchemaVersion),
		"code":            row.Code,
		"capture_time":    row.CaptureTime,
		"capture_date":    row.CaptureDate,
		"last_milli":      row.Last,
		"pre_close_milli": row.PreClose,
		"open_milli":      row.Open,
		"high_milli":      row.High,
		"low_milli":       row.Low,
		"volume_hand":     row.VolumeHand,
		"amount_yuan":     row.AmountYuan,
	}
}

func orderRowMap(row orderHistoryArchiveRow) map[string]any {
	return map[string]any{
		"schema_version": int(row.SchemaVersion),
		"code":           row.Code,
		"trade_date":     row.TradeDate,
		"seq":            row.Seq,
		"price_milli":    row.Price,
		"buy_sell_delta": row.BuySellDelta,
		"volume":         row.Volume,
	}
}

func auctionSnapshotRowMap(row auctionSnapshotArchiveRow) map[string]any {
	return map[string]any{
		"schema_version":     int(row.SchemaVersion),
		"trade_date":         row.TradeDate,
		"snapshot_time":      row.SnapshotTime,
		"instrument_code":    row.InstrumentCode,
		"name":               row.Name,
		"auction_price":      row.AuctionPrice,
		"auction_amount":     row.AuctionAmount,
		"prev_close":         row.PrevClose,
		"auction_pct":        row.AuctionPct,
		"bid1_price":         row.Bid1Price,
		"bid1_volume":        row.Bid1Volume,
		"ask1_price":         row.Ask1Price,
		"ask1_volume":        row.Ask1Volume,
		"is_limit_up_open":   row.IsLimitUpOpen,
		"is_limit_down_open": row.IsLimitDownOpen,
		"collected_at":       row.CollectedAt,
	}
}
