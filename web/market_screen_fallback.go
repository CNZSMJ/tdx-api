package main

import (
	"database/sql"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type marketScreenRequest struct {
	sortBy         string
	order          string
	filter         string
	assetType      string
	limit          int
	tradingDate    string
	hasTradingDate bool
}

type marketScreenCodeRow struct {
	fullCode  string
	name      string
	exchange  string
	assetType string
}

type marketScreenCloseSnapshot struct {
	tick collectorpkg.StockTick
	date int64
}

type marketStatsRequest struct {
	assetType      string
	tradingDate    string
	hasTradingDate bool
}

type marketBlockRequest struct {
	key            blockProviderKey
	sortBy         string
	order          string
	limit          int
	tradingDate    string
	hasTradingDate bool
}

var marketScreenNow = time.Now

func parseMarketScreenRequest(r *http.Request) (marketScreenRequest, error) {
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortBy == "" {
		sortBy = "change_pct"
	}
	order := strings.TrimSpace(r.URL.Query().Get("order"))
	if order == "" {
		order = "desc"
	}
	assetType := strings.TrimSpace(r.URL.Query().Get("asset_type"))
	if assetType == "" {
		assetType = string(collectorpkg.AssetTypeStock)
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketScreenRequest{}, err
		}
		tradingDate = parsed
	}
	return marketScreenRequest{
		sortBy:         sortBy,
		order:          order,
		filter:         strings.TrimSpace(r.URL.Query().Get("filter")),
		assetType:      assetType,
		limit:          limit,
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func parseMarketScreenTradingDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"20060102", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed.Format("20060102"), nil
		}
	}
	return "", strconv.ErrSyntax
}

func buildMarketScreenResponse(ticks []collectorpkg.StockTick, filter, filterNote string, ts *collectorpkg.TickerService) map[string]interface{} {
	list := make([]map[string]interface{}, 0, len(ticks))
	for i := range ticks {
		item := stockTickToScreenMap(&ticks[i])
		if ts != nil {
			switch filter {
			case "limit_up":
				if p := ts.GetLimitUpPublic(ticks[i].Code); p != nil {
					mergeLimitPublic(item, p, true)
				}
			case "limit_down":
				if p := ts.GetLimitDownPublic(ticks[i].Code); p != nil {
					mergeLimitPublic(item, p, false)
				}
			}
		}
		list = append(list, item)
	}

	resp := map[string]interface{}{
		"count": len(list),
		"list":  list,
	}
	if filterNote != "" {
		resp["filter_note"] = filterNote
	}
	return resp
}

func buildMarketScreenTickerResponse(req marketScreenRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if !marketScreenShouldUseTicker(req, ts) {
		return nil, false
	}
	ticks, filterNote := ts.MarketScreen(req.sortBy, req.order, req.filter, req.assetType, req.limit)
	resp := buildMarketScreenResponse(ticks, req.filter, filterNote, ts)
	resp["data_source"] = "ticker"
	resp["trading_date"] = ts.UpdatedAt().In(time.Local).Format("20060102")
	addTickerMeta(resp, ts)
	return resp, true
}

func marketScreenShouldUseTicker(req marketScreenRequest, ts *collectorpkg.TickerService) bool {
	if ts == nil {
		return false
	}
	updatedAt := ts.UpdatedAt()
	if updatedAt.IsZero() {
		return false
	}
	updatedDate := updatedAt.In(time.Local).Format("20060102")
	if req.hasTradingDate {
		return updatedDate == req.tradingDate
	}
	now := marketScreenNow()
	if !marketScreenIsTodayTradingDay(now) {
		return false
	}
	return updatedDate == now.In(time.Local).Format("20060102")
}

func marketScreenIsTodayTradingDay(now time.Time) bool {
	if ok, err := resolveTradingDay(now); err == nil {
		return ok
	}
	wd := now.In(time.Local).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

func buildMarketScreenCloseSnapshotResponse(req marketScreenRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, filterNote, ok := loadMarketScreenCloseSnapshot(req)
	if !ok {
		if req.hasTradingDate {
			resp := buildMarketScreenResponse(nil, req.filter, filterNote, nil)
			addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
			resp["status"] = "empty"
			resp["status_hint"] = "指定 trading_date 无日K收盘快照"
			return resp, true
		}
		return nil, false
	}
	resp := buildMarketScreenResponse(ticks, req.filter, filterNote, nil)
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func loadMarketScreenCloseSnapshot(req marketScreenRequest) ([]collectorpkg.StockTick, string, string, bool) {
	if req.limit <= 0 {
		req.limit = 50
	}
	if req.limit > 200 {
		req.limit = 200
	}
	filterNote := ""
	if req.filter == "limit_up" || req.filter == "limit_down" {
		req.assetType = string(collectorpkg.AssetTypeStock)
		filterNote = "涨跌停筛选仅适用于股票"
	}

	ticks, tradingDate, ok := loadMarketScreenCloseTicks(req.assetType, req.tradingDate)
	if !ok {
		return nil, "", "", false
	}
	filtered := make([]collectorpkg.StockTick, 0, len(ticks))
	for _, tick := range ticks {
		switch req.filter {
		case "limit_up":
			if !tick.IsLimitUp {
				continue
			}
		case "limit_down":
			if !tick.IsLimitDown {
				continue
			}
		}
		filtered = append(filtered, tick)
	}
	if len(filtered) == 0 {
		return nil, "", "", false
	}
	sortMarketScreenTicks(filtered, req.sortBy, req.order)
	if len(filtered) > req.limit {
		filtered = filtered[:req.limit]
	}
	return filtered, tradingDate, filterNote, true
}

func loadMarketScreenLatestCloseTicks(assetType string) ([]collectorpkg.StockTick, string, bool) {
	return loadMarketScreenCloseTicks(assetType, "")
}

func loadMarketScreenCloseTicks(assetType, tradingDate string) ([]collectorpkg.StockTick, string, bool) {
	codes, err := loadMarketScreenCodeRows(assetType)
	if err != nil || len(codes) == 0 {
		return nil, "", false
	}

	items := make([]marketScreenCloseSnapshot, 0, len(codes))
	var latestDate int64
	for _, code := range codes {
		item, ok := loadMarketScreenCloseSnapshotForCode(code, tradingDate)
		if !ok {
			continue
		}
		if item.date > latestDate {
			latestDate = item.date
		}
		items = append(items, item)
	}
	if len(items) == 0 || latestDate == 0 {
		return nil, "", false
	}

	ticks := make([]collectorpkg.StockTick, 0, len(items))
	for _, item := range items {
		if item.date == latestDate {
			ticks = append(ticks, item.tick)
		}
	}
	if tradingDate != "" {
		return ticks, tradingDate, len(ticks) > 0
	}
	return ticks, time.Unix(latestDate, 0).In(time.Local).Format("20060102"), len(ticks) > 0
}

func loadMarketScreenCodeRows(assetType string) ([]marketScreenCodeRow, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(databaseDir, "codes.db")+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT Exchange, Code, Name FROM codes WHERE Exchange IS NOT NULL AND Code IS NOT NULL ORDER BY Exchange, Code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]marketScreenCodeRow, 0, 5120)
	for rows.Next() {
		var exchange, code, name string
		if err := rows.Scan(&exchange, &code, &name); err != nil {
			return nil, err
		}
		exchange = strings.TrimSpace(exchange)
		code = strings.TrimSpace(code)
		if exchange == "" || code == "" {
			continue
		}
		fullCode := code
		if !strings.HasPrefix(fullCode, exchange) {
			fullCode = exchange + fullCode
		}
		at := classifyAssetType(fullCode)
		switch assetType {
		case "all":
			if at != string(collectorpkg.AssetTypeStock) && at != string(collectorpkg.AssetTypeETF) {
				continue
			}
		case string(collectorpkg.AssetTypeETF):
			if at != string(collectorpkg.AssetTypeETF) {
				continue
			}
		case string(collectorpkg.AssetTypeStock):
			fallthrough
		default:
			if at != string(collectorpkg.AssetTypeStock) {
				continue
			}
		}
		out = append(out, marketScreenCodeRow{
			fullCode:  fullCode,
			name:      strings.TrimSpace(name),
			exchange:  exchange,
			assetType: at,
		})
	}
	return out, rows.Err()
}

func loadMarketScreenCloseSnapshotForCode(code marketScreenCodeRow, tradingDate string) (marketScreenCloseSnapshot, bool) {
	dbPath := filepath.Join(databaseDir, "kline", code.fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return marketScreenCloseSnapshot{}, false
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return marketScreenCloseSnapshot{}, false
	}
	defer db.Close()

	var latest, previous struct {
		date   int64
		open   collectorpkg.PriceMilli
		high   collectorpkg.PriceMilli
		low    collectorpkg.PriceMilli
		close  collectorpkg.PriceMilli
		volume int64
		amount collectorpkg.PriceMilli
	}

	if tradingDate == "" {
		rows, err := db.Query(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? ORDER BY Date DESC LIMIT 2`, code.fullCode)
		if err != nil {
			return marketScreenCloseSnapshot{}, false
		}
		defer rows.Close()
		if !rows.Next() {
			return marketScreenCloseSnapshot{}, false
		}
		if err := rows.Scan(&latest.date, &latest.open, &latest.high, &latest.low, &latest.close, &latest.volume, &latest.amount); err != nil {
			return marketScreenCloseSnapshot{}, false
		}
		hasPrevious := rows.Next()
		if hasPrevious {
			if err := rows.Scan(&previous.date, &previous.open, &previous.high, &previous.low, &previous.close, &previous.volume, &previous.amount); err != nil {
				return marketScreenCloseSnapshot{}, false
			}
		}
		return buildMarketScreenCloseSnapshot(code, latest, previous, hasPrevious), true
	}

	target, err := time.ParseInLocation("20060102", tradingDate, time.Local)
	if err != nil {
		return marketScreenCloseSnapshot{}, false
	}
	start := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, time.Local).Unix()
	end := time.Date(target.Year(), target.Month(), target.Day()+1, 0, 0, 0, 0, time.Local).Unix()
	row := db.QueryRow(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date >= ? AND Date < ? ORDER BY Date DESC LIMIT 1`, code.fullCode, start, end)
	if err := row.Scan(&latest.date, &latest.open, &latest.high, &latest.low, &latest.close, &latest.volume, &latest.amount); err != nil {
		return marketScreenCloseSnapshot{}, false
	}
	row = db.QueryRow(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date < ? ORDER BY Date DESC LIMIT 1`, code.fullCode, latest.date)
	hasPrevious := true
	if err := row.Scan(&previous.date, &previous.open, &previous.high, &previous.low, &previous.close, &previous.volume, &previous.amount); err != nil {
		hasPrevious = false
	}
	return buildMarketScreenCloseSnapshot(code, latest, previous, hasPrevious), true
}

func buildMarketScreenCloseSnapshot(code marketScreenCodeRow, latest, previous struct {
	date   int64
	open   collectorpkg.PriceMilli
	high   collectorpkg.PriceMilli
	low    collectorpkg.PriceMilli
	close  collectorpkg.PriceMilli
	volume int64
	amount collectorpkg.PriceMilli
}, hasPrevious bool) marketScreenCloseSnapshot {
	preClose := previous.close.Float64()
	price := latest.close.Float64()
	priceChange := 0.0
	pctChange := 0.0
	amplitude := 0.0
	if hasPrevious && preClose > 0 {
		priceChange = price - preClose
		pctChange = priceChange / preClose * 100
		amplitude = (latest.high.Float64() - latest.low.Float64()) / preClose * 100
	}

	tick := collectorpkg.StockTick{
		Code:        code.fullCode,
		Name:        code.name,
		Exchange:    code.exchange,
		AssetType:   code.assetType,
		Last:        price,
		PreClose:    preClose,
		Open:        latest.open.Float64(),
		High:        latest.high.Float64(),
		Low:         latest.low.Float64(),
		PctChange:   roundMarketScreen(pctChange, 2),
		PriceChange: roundMarketScreen(priceChange, 3),
		Volume:      latest.volume,
		Amount:      latest.amount.Float64(),
		Amplitude:   roundMarketScreen(amplitude, 2),
	}
	tick.IsLimitUp = marketScreenLimitUp(tick.PctChange, tick.Code, tick.Name)
	tick.IsLimitDown = marketScreenLimitDown(tick.PctChange, tick.Code, tick.Name)
	return marketScreenCloseSnapshot{tick: tick, date: latest.date}
}

func addMarketScreenCloseSnapshotMeta(resp map[string]interface{}, tradingDate string) {
	resp["status"] = "closed_snapshot"
	resp["status_hint"] = "Ticker 无盘中快照，已使用本地日K收盘快照"
	resp["data_source"] = "daily_kline"
	resp["trading_date"] = tradingDate
	if parsed, err := time.ParseInLocation("20060102", tradingDate, time.Local); err == nil {
		resp["updated_at"] = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 15, 0, 0, 0, time.Local).Format(time.RFC3339)
	}
}

func parseMarketStatsRequest(r *http.Request) (marketStatsRequest, error) {
	assetType, err := parseMarketStatsAssetType(r.URL.Query().Get("asset_type"))
	if err != nil {
		return marketStatsRequest{}, err
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketStatsRequest{}, err
		}
		tradingDate = parsed
	}
	return marketStatsRequest{
		assetType:      assetType,
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func buildMarketStatsTickerResponse(req marketStatsRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if !marketScreenShouldUseTicker(marketScreenRequest{
		tradingDate:    req.tradingDate,
		hasTradingDate: req.hasTradingDate,
	}, ts) {
		return nil, false
	}
	resp := buildMarketStatsData(ts.GetAllStocks(), req.assetType)
	resp["data_source"] = "ticker"
	resp["trading_date"] = ts.UpdatedAt().In(time.Local).Format("20060102")
	addTickerMeta(resp, ts)
	return resp, true
}

func buildMarketStatsCloseSnapshotResponse(req marketStatsRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks(req.assetType, req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	resp := buildMarketStatsData(ticks, req.assetType)
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func parseMarketBlockRequest(r *http.Request, requirement blockProviderKeyRequirement) (marketBlockRequest, error) {
	key, err := parseBlockProviderKey(r, requirement)
	if err != nil {
		return marketBlockRequest{}, err
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketBlockRequest{}, err
		}
		tradingDate = parsed
	}
	return marketBlockRequest{
		key:            key,
		sortBy:         strings.TrimSpace(r.URL.Query().Get("sort_by")),
		order:          strings.TrimSpace(r.URL.Query().Get("order")),
		limit:          parsePositiveInt(strings.TrimSpace(r.URL.Query().Get("limit"))),
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func buildBlockRankingTickerResponse(req marketBlockRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if !marketScreenShouldUseTicker(marketScreenRequest{
		tradingDate:    req.tradingDate,
		hasTradingDate: req.hasTradingDate,
	}, ts) {
		return nil, false
	}
	ranks := ts.GetBlockRanking(req.key.Source, req.key.BlockType, req.sortBy, req.order, req.limit)
	items := make([]map[string]interface{}, 0, len(ranks))
	for _, rank := range ranks {
		if req.key.Name != "" && rank.Name != req.key.Name {
			continue
		}
		items = append(items, blockRankToProviderMap(rank))
	}
	resp := map[string]interface{}{
		"count":        len(items),
		"items":        items,
		"data_source":  "ticker",
		"trading_date": ts.UpdatedAt().In(time.Local).Format("20060102"),
	}
	addTickerMeta(resp, ts)
	return resp, true
}

func buildBlockStocksTickerResponse(req marketBlockRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if !marketScreenShouldUseTicker(marketScreenRequest{
		tradingDate:    req.tradingDate,
		hasTradingDate: req.hasTradingDate,
	}, ts) {
		return nil, false
	}
	blockPct, ticks := ts.GetBlockStocks(req.key.Source, req.key.BlockType, req.key.Name, req.sortBy, req.order, req.limit)
	items := make([]map[string]interface{}, 0, len(ticks))
	for _, tick := range ticks {
		items = append(items, stockTickToProviderMap(tick))
	}
	resp := map[string]interface{}{
		"source":           req.key.Source,
		"block_type":       req.key.BlockType,
		"name":             req.key.Name,
		"block_pct_change": blockPct,
		"count":            len(items),
		"items":            items,
		"data_source":      "ticker",
		"trading_date":     ts.UpdatedAt().In(time.Local).Format("20060102"),
	}
	addTickerMeta(resp, ts)
	return resp, true
}

func buildBlockRankingCloseSnapshotResponse(req marketBlockRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks("all", req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	items := make([]map[string]interface{}, 0)
	if ok {
		items = buildBlockRankingCloseSnapshotItems(req, ticks)
	}
	resp := map[string]interface{}{
		"count": len(items),
		"items": items,
	}
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func buildBlockRankingCloseSnapshotItems(req marketBlockRequest, ticks []collectorpkg.StockTick) []map[string]interface{} {
	tickByCode := make(map[string]collectorpkg.StockTick, len(ticks)*2)
	for _, tick := range ticks {
		addMarketScreenTickLookup(tickByCode, tick)
	}
	groups, err := loadMarketScreenBlockGroups(req.key)
	if err != nil || len(groups) == 0 {
		return nil
	}
	ranks := make([]collectorpkg.BlockRank, 0, len(groups))
	for _, group := range groups {
		members, err := loadMarketScreenBlockMembers(group.Source, group.BlockType, group.Name)
		if err != nil || len(members) == 0 {
			continue
		}
		rank := collectorpkg.BlockRank{
			Name:        group.Name,
			Source:      group.Source,
			BlockType:   group.BlockType,
			MemberCount: len(members),
		}
		var totalPct float64
		var leading *collectorpkg.StockTick
		for _, code := range members {
			tick, ok := tickByCode[code]
			if !ok {
				continue
			}
			rank.AvailableCount++
			totalPct += tick.PctChange
			rank.Amount += tick.Amount
			if tick.PctChange > 0 {
				rank.RiseCount++
			} else if tick.PctChange < 0 {
				rank.FallCount++
			} else {
				rank.FlatCount++
			}
			if tick.IsLimitUp {
				rank.LimitUpCount++
			}
			if tick.IsLimitDown {
				rank.LimitDownCount++
			}
			if leading == nil || tick.PctChange > leading.PctChange {
				copyTick := tick
				leading = &copyTick
			}
		}
		if rank.AvailableCount == 0 {
			continue
		}
		rank.PctChange = roundMarketScreen(totalPct/float64(rank.AvailableCount), 2)
		if leading != nil {
			rank.LeadingCode = leading.Code
			rank.LeadingName = leading.Name
			rank.LeadingPct = leading.PctChange
		}
		ranks = append(ranks, rank)
	}
	sortMarketScreenBlockRanks(ranks, req.sortBy, req.order)
	if req.limit > 0 && len(ranks) > req.limit {
		ranks = ranks[:req.limit]
	}
	items := make([]map[string]interface{}, 0, len(ranks))
	for _, rank := range ranks {
		items = append(items, blockRankToProviderMap(rank))
	}
	return items
}

func buildBlockStocksCloseSnapshotResponse(req marketBlockRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks("all", req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	items := make([]map[string]interface{}, 0)
	blockPct := 0.0
	if ok {
		blockPct, items = buildBlockStocksCloseSnapshotItems(req, ticks)
	}
	resp := map[string]interface{}{
		"source":           req.key.Source,
		"block_type":       req.key.BlockType,
		"name":             req.key.Name,
		"block_pct_change": blockPct,
		"count":            len(items),
		"items":            items,
	}
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func buildBlockStocksCloseSnapshotItems(req marketBlockRequest, ticks []collectorpkg.StockTick) (float64, []map[string]interface{}) {
	tickByCode := make(map[string]collectorpkg.StockTick, len(ticks)*2)
	for _, tick := range ticks {
		addMarketScreenTickLookup(tickByCode, tick)
	}
	members, err := loadMarketScreenBlockMembers(req.key.Source, req.key.BlockType, req.key.Name)
	if err != nil || len(members) == 0 {
		return 0, nil
	}
	blockTicks := make([]collectorpkg.StockTick, 0, len(members))
	var totalPct float64
	for _, code := range members {
		tick, ok := tickByCode[code]
		if !ok {
			continue
		}
		totalPct += tick.PctChange
		blockTicks = append(blockTicks, tick)
	}
	if len(blockTicks) == 0 {
		return 0, nil
	}
	sortMarketScreenTicks(blockTicks, req.sortBy, req.order)
	if req.limit > 0 && len(blockTicks) > req.limit {
		blockTicks = blockTicks[:req.limit]
	}
	items := make([]map[string]interface{}, 0, len(blockTicks))
	for _, tick := range blockTicks {
		items = append(items, stockTickToProviderMap(tick))
	}
	return roundMarketScreen(totalPct/float64(len(blockTicks)), 2), items
}

func blockRankToProviderMap(rank collectorpkg.BlockRank) map[string]interface{} {
	return map[string]interface{}{
		"source":            rank.Source,
		"block_type":        rank.BlockType,
		"name":              rank.Name,
		"pct_change":        rank.PctChange,
		"amount":            rank.Amount,
		"member_count":      rank.MemberCount,
		"available_count":   rank.AvailableCount,
		"rise_count":        rank.RiseCount,
		"fall_count":        rank.FallCount,
		"flat_count":        rank.FlatCount,
		"limit_up_count":    rank.LimitUpCount,
		"limit_down_count":  rank.LimitDownCount,
		"leading_full_code": rank.LeadingCode,
		"leading_name":      rank.LeadingName,
		"leading_pct":       rank.LeadingPct,
		"leading_code":      bareCode(rank.LeadingCode),
	}
}

func addMarketScreenTickLookup(dst map[string]collectorpkg.StockTick, tick collectorpkg.StockTick) {
	dst[tick.Code] = tick
	dst[bareCode(tick.Code)] = tick
}

func loadMarketScreenBlockGroups(key blockProviderKey) ([]collectorpkg.BlockGroupRecord, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(databaseDir, "block", "blocks.db")+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `SELECT Name, BlockType, Source, StockCount, UpdatedAt FROM block_group WHERE Source = ?`
	args := []interface{}{key.Source}
	if key.BlockType != "" {
		query += ` AND BlockType = ?`
		args = append(args, key.BlockType)
	}
	if key.Name != "" {
		query += ` AND Name = ?`
		args = append(args, key.Name)
	}
	query += ` ORDER BY Name`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]collectorpkg.BlockGroupRecord, 0, 256)
	for rows.Next() {
		var group collectorpkg.BlockGroupRecord
		if err := rows.Scan(&group.Name, &group.BlockType, &group.Source, &group.StockCount, &group.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func loadMarketScreenBlockMembers(source, blockType, name string) ([]string, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(databaseDir, "block", "blocks.db")+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT Code FROM block_member WHERE Source = ? AND BlockType = ? AND BlockName = ? ORDER BY Code`, source, blockType, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	codes := make([]string, 0, 512)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, strings.TrimSpace(code))
	}
	return codes, rows.Err()
}

func sortMarketScreenBlockRanks(ranks []collectorpkg.BlockRank, sortBy, order string) {
	desc := !strings.EqualFold(order, "asc")
	sort.SliceStable(ranks, func(i, j int) bool {
		var a, b float64
		switch sortBy {
		case "amount":
			a, b = ranks[i].Amount, ranks[j].Amount
		case "limit_up":
			a, b = float64(ranks[i].LimitUpCount), float64(ranks[j].LimitUpCount)
		case "rise_count":
			a, b = float64(ranks[i].RiseCount), float64(ranks[j].RiseCount)
		default:
			a, b = ranks[i].PctChange, ranks[j].PctChange
		}
		if desc {
			return a > b
		}
		return a < b
	})
}

func sortMarketScreenTicks(ticks []collectorpkg.StockTick, sortBy, order string) {
	desc := !strings.EqualFold(order, "asc")
	if sortBy == "change_pct" || sortBy == "" {
		sortBy = "pct_change"
	}
	sort.SliceStable(ticks, func(i, j int) bool {
		var a, b float64
		switch sortBy {
		case "amount":
			a, b = ticks[i].Amount, ticks[j].Amount
		case "volume":
			a, b = float64(ticks[i].Volume), float64(ticks[j].Volume)
		case "amplitude":
			a, b = ticks[i].Amplitude, ticks[j].Amplitude
		case "pct_change":
			fallthrough
		default:
			a, b = ticks[i].PctChange, ticks[j].PctChange
		}
		if desc {
			return a > b
		}
		return a < b
	})
}

func marketScreenLimitUp(pct float64, code, name string) bool {
	return pct >= marketScreenLimitThreshold(code, name)-0.05
}

func marketScreenLimitDown(pct float64, code, name string) bool {
	return pct <= -(marketScreenLimitThreshold(code, name) - 0.05)
}

func marketScreenLimitThreshold(code, name string) float64 {
	bare := code
	if len(code) > 2 {
		bare = code[2:]
	}
	switch {
	case strings.HasPrefix(bare, "68"), strings.HasPrefix(bare, "30"):
		return 20
	case strings.HasPrefix(bare, "83"), strings.HasPrefix(bare, "87"),
		strings.HasPrefix(bare, "82"), strings.HasPrefix(bare, "43"):
		return 30
	default:
		if strings.Contains(strings.ToUpper(name), "ST") {
			return 5
		}
		return 10
	}
}

func roundMarketScreen(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}
