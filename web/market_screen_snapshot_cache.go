package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

var marketScreenSnapshotBuildMu sync.Mutex

func buildAndStoreMarketScreenCloseTicks(assetType, tradingDate string) ([]collectorpkg.StockTick, string, bool) {
	marketScreenSnapshotBuildMu.Lock()
	defer marketScreenSnapshotBuildMu.Unlock()

	if ticks, date, ok := loadMarketScreenMaterializedCloseTicks(assetType, tradingDate); ok {
		return ticks, date, true
	}

	started := time.Now()
	ticks, date, ok := buildMarketScreenCloseTicksFromKline("all", tradingDate)
	if !ok {
		return nil, "", false
	}
	if !marketScreenCloseTicksUsable(ticks) {
		log.Printf("market_snapshot build discarded: trading_date=%s rows=%d reason=zero_volume_amount", date, len(ticks))
		return nil, "", false
	}
	if err := saveMarketScreenMaterializedCloseTicks(date, ticks); err != nil {
		log.Printf("market_snapshot save failed: trading_date=%s rows=%d err=%v", date, len(ticks), err)
	} else {
		log.Printf("market_snapshot built: trading_date=%s rows=%d duration=%s", date, len(ticks), time.Since(started).Truncate(time.Millisecond))
	}

	filtered := filterMarketScreenCloseTicksByAssetType(ticks, assetType)
	return filtered, date, len(filtered) > 0
}

func loadMarketScreenCloseSnapshotsForCodes(codes []marketScreenCodeRow, tradingDate string) []marketScreenCloseSnapshot {
	workerCount := marketScreenSnapshotWorkerCount(len(codes))
	if workerCount == 0 {
		return nil
	}

	jobs := make(chan marketScreenCodeRow)
	results := make(chan marketScreenCloseSnapshot, len(codes))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range jobs {
				if item, ok := loadMarketScreenCloseSnapshotForCode(code, tradingDate); ok {
					results <- item
				}
			}
		}()
	}

	for _, code := range codes {
		jobs <- code
	}
	close(jobs)
	wg.Wait()
	close(results)

	items := make([]marketScreenCloseSnapshot, 0, len(results))
	for item := range results {
		items = append(items, item)
	}
	return items
}

func buildMarketLimitUpTierStocksFromKline(tradingDate string) ([]marketLimitUpTierStock, bool) {
	codes, err := loadMarketScreenCodeRows(string(collectorpkg.AssetTypeStock))
	if err != nil || len(codes) == 0 {
		return nil, false
	}

	workerCount := marketScreenSnapshotWorkerCount(len(codes))
	jobs := make(chan marketScreenCodeRow)
	results := make(chan marketLimitUpTierBuildResult, len(codes))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range jobs {
				rows, ok := loadMarketLimitUpTierRows(code, tradingDate)
				if !ok {
					continue
				}
				result := marketLimitUpTierBuildResult{hasTarget: true}
				if stock, ok := marketLimitUpTierStockFromRows(code, rows, tradingDate); ok {
					result.stock = stock
					result.hasStock = true
				}
				results <- result
			}
		}()
	}

	for _, code := range codes {
		jobs <- code
	}
	close(jobs)
	wg.Wait()
	close(results)

	stocks := make([]marketLimitUpTierStock, 0, 64)
	hasTarget := false
	for result := range results {
		hasTarget = hasTarget || result.hasTarget
		if result.hasStock {
			stocks = append(stocks, result.stock)
		}
	}
	return stocks, hasTarget
}

type marketLimitUpTierBuildResult struct {
	stock     marketLimitUpTierStock
	hasStock  bool
	hasTarget bool
}

func marketScreenSnapshotWorkerCount(total int) int {
	if total <= 0 {
		return 0
	}
	workers := runtime.GOMAXPROCS(0) * 4
	if workers < 8 {
		workers = 8
	}
	if workers > 32 {
		workers = 32
	}
	if workers > total {
		workers = total
	}
	return workers
}

func loadMarketScreenMaterializedCloseTicks(assetType, tradingDate string) ([]collectorpkg.StockTick, string, bool) {
	db, err := openMarketScreenSnapshotDB(true)
	if err != nil {
		return nil, "", false
	}
	defer db.Close()

	date := tradingDate
	if date == "" {
		var ok bool
		date, ok = latestMarketScreenSnapshotDate(db, assetType)
		if !ok {
			return nil, "", false
		}
	}

	where, args := marketScreenSnapshotAssetWhere(assetType)
	args = append([]interface{}{date}, args...)
	rows, err := db.Query(`SELECT full_code, name, exchange, asset_type, last_price, pre_close, open_price, high_price, low_price, change_pct, price_change, volume, amount, amplitude, is_limit_up, is_limit_down FROM daily_market_snapshot WHERE trading_date = ?`+where+` ORDER BY full_code`, args...)
	if err != nil {
		return nil, "", false
	}
	defer rows.Close()

	ticks := make([]collectorpkg.StockTick, 0, 5120)
	for rows.Next() {
		var tick collectorpkg.StockTick
		var isLimitUp, isLimitDown int
		if err := rows.Scan(&tick.Code, &tick.Name, &tick.Exchange, &tick.AssetType, &tick.Last, &tick.PreClose, &tick.Open, &tick.High, &tick.Low, &tick.PctChange, &tick.PriceChange, &tick.Volume, &tick.Amount, &tick.Amplitude, &isLimitUp, &isLimitDown); err != nil {
			return nil, "", false
		}
		tick.IsLimitUp = isLimitUp == 1
		tick.IsLimitDown = isLimitDown == 1
		if !marketScreenCloseTickLimitFlagsValid(tick) {
			// 跳过单条限价校验失败的行（如 ETF/指数限价规则不匹配），不影响整批数据
			continue
		}
		ticks = append(ticks, tick)
	}
	if err := rows.Err(); err != nil || len(ticks) == 0 {
		return nil, "", false
	}
	if !marketScreenCloseTicksUsable(ticks) {
		return nil, "", false
	}
	return ticks, date, true
}

func saveMarketScreenMaterializedCloseTicks(tradingDate string, ticks []collectorpkg.StockTick) error {
	if !marketScreenCloseTicksUsable(ticks) {
		return nil
	}
	db, err := openMarketScreenSnapshotDB(false)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureMarketScreenSnapshotSchema(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM daily_market_snapshot WHERE trading_date = ?`, tradingDate); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO daily_market_snapshot(trading_date, full_code, name, exchange, asset_type, open_price, high_price, low_price, last_price, pre_close, change_pct, price_change, volume, amount, amplitude, is_limit_up, is_limit_down, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	updatedAt := time.Now().Format(time.RFC3339)
	for _, tick := range ticks {
		if _, err := stmt.Exec(tradingDate, tick.Code, tick.Name, tick.Exchange, tick.AssetType, tick.Open, tick.High, tick.Low, tick.Last, tick.PreClose, tick.PctChange, tick.PriceChange, tick.Volume, tick.Amount, tick.Amplitude, boolToInt(tick.IsLimitUp), boolToInt(tick.IsLimitDown), updatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func marketScreenCloseTicksUsable(ticks []collectorpkg.StockTick) bool {
	if len(ticks) == 0 {
		return false
	}
	for _, tick := range ticks {
		if tick.Amount > 0 || tick.Volume > 0 {
			return true
		}
	}
	return false
}

func marketScreenCloseTickLimitFlagsValid(tick collectorpkg.StockTick) bool {
	return tick.IsLimitUp == marketScreenPriceTouchesLimitUp(tick.Last, tick.PreClose, tick.Code, tick.Name) &&
		tick.IsLimitDown == marketScreenPriceTouchesLimitDown(tick.Last, tick.PreClose, tick.Code, tick.Name)
}

func loadMarketLimitUpTiersMaterialized(req marketLimitUpTiersRequest, tradingDate string) ([]marketLimitUpTierStock, bool) {
	db, err := openMarketScreenSnapshotDB(true)
	if err != nil {
		return nil, false
	}
	defer db.Close()

	where := ` WHERE trading_date = ? AND streak >= ?`
	args := []interface{}{tradingDate, req.minStreak}
	switch req.stockClass {
	case "st":
		where += ` AND is_st = 1`
	case "non_st":
		where += ` AND is_st = 0`
	}

	rows, err := db.Query(`SELECT full_code, name, exchange, is_st, price, change_pct, amount, volume, streak, first_limit_date, last_limit_date, board_type FROM daily_limit_up_tier_snapshot`+where+` ORDER BY streak DESC, amount DESC, full_code`, args...)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	stocks := make([]marketLimitUpTierStock, 0, 64)
	for rows.Next() {
		var stock marketLimitUpTierStock
		var isST int
		if err := rows.Scan(&stock.Code, &stock.Name, &stock.Exchange, &isST, &stock.Price, &stock.ChangePct, &stock.Amount, &stock.Volume, &stock.Streak, &stock.FirstLimitDate, &stock.LastLimitDate, &stock.BoardType); err != nil {
			return nil, false
		}
		stock.IsST = isST == 1
		stocks = append(stocks, stock)
	}
	if err := rows.Err(); err != nil {
		return nil, false
	}
	return stocks, len(stocks) > 0
}

func saveMarketLimitUpTiersMaterialized(tradingDate string, stocks []marketLimitUpTierStock) {
	db, err := openMarketScreenSnapshotDB(false)
	if err != nil {
		log.Printf("market_limit_up_tiers save failed: trading_date=%s err=%v", tradingDate, err)
		return
	}
	defer db.Close()
	if err := ensureMarketScreenSnapshotSchema(db); err != nil {
		log.Printf("market_limit_up_tiers schema failed: trading_date=%s err=%v", tradingDate, err)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		log.Printf("market_limit_up_tiers save failed: trading_date=%s err=%v", tradingDate, err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM daily_limit_up_tier_snapshot WHERE trading_date = ?`, tradingDate); err != nil {
		log.Printf("market_limit_up_tiers delete failed: trading_date=%s err=%v", tradingDate, err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO daily_limit_up_tier_snapshot(trading_date, full_code, name, exchange, is_st, price, change_pct, amount, volume, streak, first_limit_date, last_limit_date, board_type, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		log.Printf("market_limit_up_tiers prepare failed: trading_date=%s err=%v", tradingDate, err)
		return
	}
	defer stmt.Close()

	updatedAt := time.Now().Format(time.RFC3339)
	for _, stock := range stocks {
		if _, err := stmt.Exec(tradingDate, stock.Code, stock.Name, stock.Exchange, boolToInt(stock.IsST), stock.Price, stock.ChangePct, stock.Amount, stock.Volume, stock.Streak, stock.FirstLimitDate, stock.LastLimitDate, stock.BoardType, updatedAt); err != nil {
			log.Printf("market_limit_up_tiers insert failed: trading_date=%s code=%s err=%v", tradingDate, stock.Code, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("market_limit_up_tiers commit failed: trading_date=%s err=%v", tradingDate, err)
		return
	}
	log.Printf("market_limit_up_tiers built: trading_date=%s rows=%d", tradingDate, len(stocks))
}

func filterMarketLimitUpTierStocks(stocks []marketLimitUpTierStock, req marketLimitUpTiersRequest) []marketLimitUpTierStock {
	filtered := make([]marketLimitUpTierStock, 0, len(stocks))
	for _, stock := range stocks {
		if stock.Streak < req.minStreak || !marketLimitUpTiersStockClassMatches(stock.Name, req.stockClass) {
			continue
		}
		filtered = append(filtered, stock)
	}
	return filtered
}

func filterMarketScreenCloseTicksByAssetType(ticks []collectorpkg.StockTick, assetType string) []collectorpkg.StockTick {
	switch assetType {
	case "all":
		return ticks
	case string(collectorpkg.AssetTypeETF):
	default:
		assetType = string(collectorpkg.AssetTypeStock)
	}
	filtered := make([]collectorpkg.StockTick, 0, len(ticks))
	for _, tick := range ticks {
		if tick.AssetType == assetType {
			filtered = append(filtered, tick)
		}
	}
	return filtered
}

func latestMarketScreenSnapshotDate(db *sql.DB, assetType string) (string, bool) {
	where, args := marketScreenSnapshotAssetWhere(assetType)
	var date sql.NullString
	if err := db.QueryRow(`SELECT MAX(trading_date) FROM daily_market_snapshot WHERE 1=1`+where, args...).Scan(&date); err != nil || !date.Valid || date.String == "" {
		return "", false
	}
	return date.String, true
}

func marketScreenSnapshotAssetWhere(assetType string) (string, []interface{}) {
	switch assetType {
	case "all":
		return "", nil
	case string(collectorpkg.AssetTypeETF):
		return " AND asset_type = ?", []interface{}{string(collectorpkg.AssetTypeETF)}
	default:
		return " AND asset_type = ?", []interface{}{string(collectorpkg.AssetTypeStock)}
	}
}

func openMarketScreenSnapshotDB(readOnly bool) (*sql.DB, error) {
	dbPath := filepath.Join(databaseDir, "market_snapshot.db")
	if readOnly {
		if _, err := os.Stat(dbPath); err != nil {
			return nil, err
		}
		return sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	}
	if err := os.MkdirAll(databaseDir, 0o755); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", dbPath)
}

func ensureMarketScreenSnapshotSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS daily_market_snapshot (
			trading_date TEXT NOT NULL,
			full_code TEXT NOT NULL,
			name TEXT NOT NULL,
			exchange TEXT NOT NULL,
			asset_type TEXT NOT NULL,
			open_price REAL NOT NULL,
			high_price REAL NOT NULL,
			low_price REAL NOT NULL,
			last_price REAL NOT NULL,
			pre_close REAL NOT NULL,
			change_pct REAL NOT NULL,
			price_change REAL NOT NULL,
			volume INTEGER NOT NULL,
			amount REAL NOT NULL,
			amplitude REAL NOT NULL,
			is_limit_up INTEGER NOT NULL,
			is_limit_down INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (trading_date, full_code)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_market_snapshot_asset_change ON daily_market_snapshot(trading_date, asset_type, change_pct)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_market_snapshot_asset_amount ON daily_market_snapshot(trading_date, asset_type, amount)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_market_snapshot_limit_up ON daily_market_snapshot(trading_date, asset_type, is_limit_up)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_market_snapshot_limit_down ON daily_market_snapshot(trading_date, asset_type, is_limit_down)`,
		`CREATE TABLE IF NOT EXISTS daily_limit_up_tier_snapshot (
			trading_date TEXT NOT NULL,
			full_code TEXT NOT NULL,
			name TEXT NOT NULL,
			exchange TEXT NOT NULL,
			is_st INTEGER NOT NULL,
			price REAL NOT NULL,
			change_pct REAL NOT NULL,
			amount REAL NOT NULL,
			volume INTEGER NOT NULL,
			streak INTEGER NOT NULL,
			first_limit_date TEXT NOT NULL,
			last_limit_date TEXT NOT NULL,
			board_type TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (trading_date, full_code)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_limit_up_tier_snapshot_lookup ON daily_limit_up_tier_snapshot(trading_date, is_st, streak)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func loadMarketScreenBlockMembersByGroup(key blockProviderKey) (map[string][]string, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(databaseDir, "block", "blocks.db")+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT Source, BlockType, BlockName, Code FROM block_member WHERE Source = ?`
	args := []interface{}{key.Source}
	if key.BlockType != "" {
		query += ` AND BlockType = ?`
		args = append(args, key.BlockType)
	}
	if key.Name != "" {
		query += ` AND BlockName = ?`
		args = append(args, key.Name)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string, 512)
	for rows.Next() {
		var source, blockType, name, code string
		if err := rows.Scan(&source, &blockType, &name, &code); err != nil {
			return nil, err
		}
		key := marketScreenBlockMemberKey(source, blockType, name)
		out[key] = append(out[key], strings.TrimSpace(code))
	}
	return out, rows.Err()
}

func marketScreenBlockMemberKey(source, blockType, name string) string {
	return source + "\x00" + blockType + "\x00" + name
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func warmMarketScreenLatestSnapshot() {
	started := time.Now()
	if _, date, ok := loadMarketScreenCloseTicks("all", ""); ok {
		req := marketLimitUpTiersRequest{stockClass: "all", minStreak: 1}
		if _, ok := loadMarketLimitUpTiersMaterialized(req, date); !ok {
			if stocks, hasTarget := buildMarketLimitUpTierStocksFromKline(date); hasTarget {
				saveMarketLimitUpTiersMaterialized(date, stocks)
			}
		}
		log.Printf("market_snapshot warm completed: trading_date=%s duration=%s", date, time.Since(started).Truncate(time.Millisecond))
	}
}
