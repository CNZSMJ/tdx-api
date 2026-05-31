package main

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/protocol"
)

type marketScreenRequest struct {
	sortBy          string
	order           string
	filter          string
	assetType       string
	limit           int
	page            int
	excludeST       bool
	minChangePct    float64
	maxChangePct    float64
	hasMinChangePct bool
	hasMaxChangePct bool
	tradingDate     string
	hasTradingDate  bool
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

type marketScreenQuoteFetcher func(...string) (protocol.QuotesResp, error)

type marketScreenPagination struct {
	page       int
	pageSize   int
	total      int
	totalPages int
	hasNext    bool
	hasPrev    bool
}

type marketStatsRequest struct {
	assetType      string
	tradingDate    string
	hasTradingDate bool
}

type marketLimitStatsRequest struct {
	tradingDate    string
	hasTradingDate bool
}

type marketLimitUpTiersRequest struct {
	tradingDate    string
	hasTradingDate bool
	stockClass     string
	minStreak      int
}

type marketLimitUpTierKlineRow struct {
	date   int64
	open   collectorpkg.PriceMilli
	high   collectorpkg.PriceMilli
	low    collectorpkg.PriceMilli
	close  collectorpkg.PriceMilli
	volume int64
	amount collectorpkg.PriceMilli
}

type marketLimitUpTierStock struct {
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	Exchange        string  `json:"exchange"`
	IsST            bool    `json:"is_st"`
	Price           float64 `json:"price"`
	ChangePct       float64 `json:"change_pct"`
	Amount          float64 `json:"amount"`
	Volume          int64   `json:"volume"`
	Streak          int     `json:"streak"`
	FirstLimitDate  string  `json:"first_limit_date"`
	LastLimitDate   string  `json:"last_limit_date"`
	BoardType       string  `json:"board_type"`
	LimitFirstSeen  *string `json:"limit_first_seen"`
	LimitBreakCount *int    `json:"limit_break_count"`
}

type marketLimitUpTier struct {
	Streak int                      `json:"streak"`
	Label  string                   `json:"label"`
	Count  int                      `json:"count"`
	Stocks []marketLimitUpTierStock `json:"stocks"`
}

type marketBlockRequest struct {
	key            blockProviderKey
	sortBy         string
	order          string
	limit          int
	tradingDate    string
	hasTradingDate bool
}

type marketSignalRequest struct {
	typeFilter     string
	tradingDate    string
	hasTradingDate bool
}

type marketSignalCheckRequest struct {
	fullCodes      []string
	signalTypes    []string
	mode           string
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
	page := 1
	if v := strings.TrimSpace(r.URL.Query().Get("page")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	minChangePct, hasMinChangePct, err := parseMarketScreenFloatParam(r.URL.Query().Get("min_change_pct"))
	if err != nil {
		return marketScreenRequest{}, errors.New("min_change_pct 参数格式错误，应为数字")
	}
	maxChangePct, hasMaxChangePct, err := parseMarketScreenFloatParam(r.URL.Query().Get("max_change_pct"))
	if err != nil {
		return marketScreenRequest{}, errors.New("max_change_pct 参数格式错误，应为数字")
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketScreenRequest{}, errors.New("trading_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		}
		tradingDate = parsed
	}
	return marketScreenRequest{
		sortBy:          sortBy,
		order:           order,
		filter:          strings.TrimSpace(r.URL.Query().Get("filter")),
		assetType:       assetType,
		limit:           limit,
		page:            page,
		excludeST:       parseBool(strings.TrimSpace(r.URL.Query().Get("exclude_st"))),
		minChangePct:    minChangePct,
		maxChangePct:    maxChangePct,
		hasMinChangePct: hasMinChangePct,
		hasMaxChangePct: hasMaxChangePct,
		tradingDate:     tradingDate,
		hasTradingDate:  tradingDate != "",
	}, nil
}

func parseMarketScreenFloatParam(raw string) (float64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
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
	pageSize := normalizeMarketScreenPageSize(req.limit)
	result := ts.MarketScreenWithOptions(collectorpkg.MarketScreenOptions{
		SortBy:          req.sortBy,
		Order:           req.order,
		Filter:          req.filter,
		AssetType:       req.assetType,
		Limit:           pageSize,
		Offset:          marketScreenPageOffset(req.page, pageSize),
		ExcludeST:       req.excludeST,
		MinChangePct:    req.minChangePct,
		MaxChangePct:    req.maxChangePct,
		HasMinChangePct: req.hasMinChangePct,
		HasMaxChangePct: req.hasMaxChangePct,
	})
	resp := buildMarketScreenResponse(result.Ticks, req.filter, result.FilterNote, ts)
	addMarketScreenPaginationMeta(resp, newMarketScreenPagination(req.page, pageSize, result.Total))
	resp["data_source"] = "ticker"
	resp["trading_date"] = ts.UpdatedAt().In(time.Local).Format("20060102")
	addTickerMeta(resp, ts)
	return resp, true
}

func buildMarketScreenQuoteSnapshotResponse(req marketScreenRequest) (map[string]interface{}, bool) {
	if req.hasTradingDate || client == nil {
		return nil, false
	}
	ticks, filterNote, pagination, ok := loadMarketScreenQuoteSnapshot(req, func(codes ...string) (protocol.QuotesResp, error) {
		return client.GetQuote(codes...)
	})
	if !ok {
		return nil, false
	}
	resp := buildMarketScreenResponse(ticks, req.filter, filterNote, nil)
	addMarketScreenPaginationMeta(resp, pagination)
	tradingDate := inferMarketScreenQuoteTradingDate(marketScreenNow())
	resp["status"] = "quote_snapshot"
	resp["status_hint"] = "Ticker 无盘中快照，已使用 TDX quote 的昨收口径"
	resp["data_source"] = "quote"
	resp["trading_date"] = tradingDate
	if parsed, err := time.ParseInLocation("20060102", tradingDate, time.Local); err == nil {
		resp["updated_at"] = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 15, 0, 0, 0, time.Local).Format(time.RFC3339)
	}
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
	ticks, tradingDate, filterNote, pagination, ok := loadMarketScreenCloseSnapshot(req)
	if !ok {
		if req.hasTradingDate {
			resp := buildMarketScreenResponse(nil, req.filter, filterNote, nil)
			addMarketScreenPaginationMeta(resp, newMarketScreenPagination(req.page, normalizeMarketScreenPageSize(req.limit), 0))
			addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
			resp["status"] = "empty"
			resp["status_hint"] = "指定 trading_date 无日K收盘快照"
			return resp, true
		}
		return nil, false
	}
	resp := buildMarketScreenResponse(ticks, req.filter, filterNote, nil)
	addMarketScreenPaginationMeta(resp, pagination)
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func loadMarketScreenCloseSnapshot(req marketScreenRequest) ([]collectorpkg.StockTick, string, string, marketScreenPagination, bool) {
	pageSize := normalizeMarketScreenPageSize(req.limit)
	filterNote := ""
	if req.filter == "limit_up" || req.filter == "limit_down" {
		req.assetType = string(collectorpkg.AssetTypeStock)
		filterNote = "涨跌停筛选仅适用于股票"
	}

	ticks, tradingDate, ok := loadMarketScreenCloseTicks(req.assetType, req.tradingDate)
	if !ok {
		return nil, "", "", marketScreenPagination{}, false
	}
	filtered := make([]collectorpkg.StockTick, 0, len(ticks))
	for _, tick := range ticks {
		if req.excludeST && marketScreenIsSTStock(tick.Name) {
			continue
		}
		if !marketScreenRequestMatchesChangePct(tick.PctChange, req) {
			continue
		}
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
		return nil, "", "", marketScreenPagination{}, false
	}
	sortMarketScreenTicks(filtered, req.sortBy, req.order)
	paged, pagination := paginateMarketScreenTicks(filtered, req.page, pageSize)
	return paged, tradingDate, filterNote, pagination, true
}

func loadMarketScreenQuoteSnapshot(req marketScreenRequest, quoteFetcher marketScreenQuoteFetcher) ([]collectorpkg.StockTick, string, marketScreenPagination, bool) {
	pageSize := normalizeMarketScreenPageSize(req.limit)
	filterNote := ""
	if req.filter == "limit_up" || req.filter == "limit_down" {
		req.assetType = string(collectorpkg.AssetTypeStock)
		filterNote = "涨跌停筛选仅适用于股票"
	}

	codes, err := loadMarketScreenCodeRows(req.assetType)
	if err != nil || len(codes) == 0 {
		return nil, "", marketScreenPagination{}, false
	}
	quotes := fetchMarketScreenQuotes(codes, quoteFetcher)
	if len(quotes) == 0 {
		return nil, "", marketScreenPagination{}, false
	}

	filtered := make([]collectorpkg.StockTick, 0, len(quotes))
	for _, code := range codes {
		quote := quotes[strings.ToLower(code.fullCode)]
		tick, ok := marketScreenQuoteToTick(code, quote)
		if !ok {
			continue
		}
		if req.excludeST && marketScreenIsSTStock(tick.Name) {
			continue
		}
		if !marketScreenRequestMatchesChangePct(tick.PctChange, req) {
			continue
		}
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
		return nil, "", marketScreenPagination{}, false
	}
	sortMarketScreenTicks(filtered, req.sortBy, req.order)
	paged, pagination := paginateMarketScreenTicks(filtered, req.page, pageSize)
	return paged, filterNote, pagination, true
}

func fetchMarketScreenQuotes(codes []marketScreenCodeRow, quoteFetcher marketScreenQuoteFetcher) map[string]*protocol.Quote {
	const batchSize = 80
	quotes := make(map[string]*protocol.Quote, len(codes))
	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}
		queryCodes := make([]string, 0, end-i)
		for _, code := range codes[i:end] {
			queryCodes = append(queryCodes, code.fullCode)
		}
		resp, err := quoteFetcher(queryCodes...)
		if err != nil {
			continue
		}
		for _, quote := range resp {
			if quote == nil {
				continue
			}
			quotes[strings.ToLower(protocol.AddPrefix(quote.Code))] = quote
		}
	}
	return quotes
}

func marketScreenQuoteToTick(code marketScreenCodeRow, quote *protocol.Quote) (collectorpkg.StockTick, bool) {
	if quote == nil {
		return collectorpkg.StockTick{}, false
	}
	preClose := quote.K.Last.Float64()
	price := quote.K.Close.Float64()
	if price <= 0 {
		return collectorpkg.StockTick{}, false
	}
	priceChange := 0.0
	pctChange := 0.0
	amplitude := 0.0
	if preClose > 0 {
		priceChange = price - preClose
		pctChange = priceChange / preClose * 100
		amplitude = (quote.K.High.Float64() - quote.K.Low.Float64()) / preClose * 100
	}
	tick := collectorpkg.StockTick{
		Code:        code.fullCode,
		Name:        code.name,
		Exchange:    code.exchange,
		AssetType:   code.assetType,
		Last:        price,
		PreClose:    preClose,
		Open:        quote.K.Open.Float64(),
		High:        quote.K.High.Float64(),
		Low:         quote.K.Low.Float64(),
		PctChange:   roundMarketScreen(pctChange, 2),
		PriceChange: roundMarketScreen(priceChange, 3),
		Volume:      int64(quote.TotalHand),
		Amount:      quote.Amount,
		Amplitude:   roundMarketScreen(amplitude, 2),
	}
	tick.IsLimitUp = marketScreenLimitUp(tick.PctChange, tick.Code, tick.Name)
	tick.IsLimitDown = marketScreenLimitDown(tick.PctChange, tick.Code, tick.Name)
	return tick, true
}

func inferMarketScreenQuoteTradingDate(now time.Time) string {
	day := normalizeCalendarDay(now)
	minutes := now.In(time.Local).Hour()*60 + now.In(time.Local).Minute()
	if isMarketScreenTradingDay(day) && minutes >= 9*60+15 {
		return day.Format("20060102")
	}
	for i := 0; i < 10; i++ {
		day = day.AddDate(0, 0, -1)
		if isMarketScreenTradingDay(day) {
			return day.Format("20060102")
		}
	}
	return normalizeCalendarDay(now).Format("20060102")
}

func isMarketScreenTradingDay(day time.Time) bool {
	if ok, err := resolveTradingDay(day); err == nil {
		return ok
	}
	if projected, ok := projectedTradingDay(day); ok {
		return projected
	}
	switch day.In(time.Local).Weekday() {
	case time.Saturday, time.Sunday:
		return false
	default:
		return true
	}
}

func loadMarketScreenLatestCloseTicks(assetType string) ([]collectorpkg.StockTick, string, bool) {
	return loadMarketScreenCloseTicks(assetType, "")
}

func loadMarketScreenCloseTicks(assetType, tradingDate string) ([]collectorpkg.StockTick, string, bool) {
	if ticks, date, ok := loadMarketScreenMaterializedCloseTicks(assetType, tradingDate); ok {
		return ticks, date, true
	}
	return buildAndStoreMarketScreenCloseTicks(assetType, tradingDate)
}

func buildMarketScreenCloseTicksFromKline(assetType, tradingDate string) ([]collectorpkg.StockTick, string, bool) {
	codes, err := loadMarketScreenCodeRows(assetType)
	if err != nil || len(codes) == 0 {
		return nil, "", false
	}

	items := loadMarketScreenCloseSnapshotsForCodes(codes, tradingDate)
	var latestDate int64
	for _, item := range items {
		if item.date > latestDate {
			latestDate = item.date
		}
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

func loadMarketLimitUpTierRows(code marketScreenCodeRow, tradingDate string) ([]marketLimitUpTierKlineRow, bool) {
	dbPath := filepath.Join(databaseDir, "kline", code.fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, false
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, false
	}
	defer db.Close()

	start, end, ok := marketScreenTradingDateUnixRange(tradingDate)
	if !ok {
		return nil, false
	}
	var target marketLimitUpTierKlineRow
	row := db.QueryRow(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date >= ? AND Date < ? ORDER BY Date DESC LIMIT 1`, code.fullCode, start, end)
	if err := row.Scan(&target.date, &target.open, &target.high, &target.low, &target.close, &target.volume, &target.amount); err != nil {
		return nil, false
	}
	rows := []marketLimitUpTierKlineRow{target}
	previousRows, err := queryMarketLimitUpTierRowsBefore(db, code.fullCode, target.date)
	if err != nil {
		return rows, true
	}
	rows = append(rows, previousRows...)
	return rows, true
}

func loadMarketLimitUpTierPreviousRows(code marketScreenCodeRow, tradingDate string) []marketLimitUpTierKlineRow {
	dbPath := filepath.Join(databaseDir, "kline", code.fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	start, _, ok := marketScreenTradingDateUnixRange(tradingDate)
	if !ok {
		return nil
	}
	rows, err := queryMarketLimitUpTierRowsBefore(db, code.fullCode, start)
	if err != nil {
		return nil
	}
	return rows
}

func queryMarketLimitUpTierRowsBefore(db *sql.DB, code string, before int64) ([]marketLimitUpTierKlineRow, error) {
	const lookback = 80
	rows, err := db.Query(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date < ? ORDER BY Date DESC LIMIT ?`, code, before, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]marketLimitUpTierKlineRow, 0, lookback)
	for rows.Next() {
		var row marketLimitUpTierKlineRow
		if err := rows.Scan(&row.date, &row.open, &row.high, &row.low, &row.close, &row.volume, &row.amount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func marketScreenTradingDateUnixRange(tradingDate string) (int64, int64, bool) {
	parsed, err := time.ParseInLocation("20060102", tradingDate, time.Local)
	if err != nil {
		return 0, 0, false
	}
	start := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.Local)
	return start.Unix(), start.AddDate(0, 0, 1).Unix(), true
}

func marketLimitUpTierStockFromRows(code marketScreenCodeRow, rows []marketLimitUpTierKlineRow, tradingDate string) (marketLimitUpTierStock, bool) {
	streak, firstDate, preClose := marketLimitUpDailyStreak(rows, code.fullCode, code.name)
	if streak == 0 {
		return marketLimitUpTierStock{}, false
	}
	current := rows[0]
	closePrice := current.close.Float64()
	changePct := 0.0
	if preClose > 0 {
		changePct = (closePrice - preClose) / preClose * 100
	}
	return marketLimitUpTierStock{
		Code:           code.fullCode,
		Name:           code.name,
		Exchange:       code.exchange,
		IsST:           marketScreenIsSTStock(code.name),
		Price:          closePrice,
		ChangePct:      roundMarketScreen(changePct, 2),
		Amount:         current.amount.Float64(),
		Volume:         current.volume,
		Streak:         streak,
		FirstLimitDate: firstDate,
		LastLimitDate:  tradingDate,
		BoardType:      marketLimitUpBoardType(current.open.Float64(), current.high.Float64(), current.low.Float64(), preClose, code.fullCode, code.name),
	}, true
}

func marketLimitUpDailyStreak(rows []marketLimitUpTierKlineRow, code, name string) (int, string, float64) {
	streak := 0
	firstDate := ""
	currentPreClose := 0.0
	for i := 0; i+1 < len(rows); i++ {
		row := rows[i]
		preClose := rows[i+1].close.Float64()
		if i == 0 {
			currentPreClose = preClose
		}
		if !marketScreenPriceTouchesLimitUp(row.close.Float64(), preClose, code, name) {
			break
		}
		streak++
		firstDate = time.Unix(row.date, 0).In(time.Local).Format("20060102")
	}
	return streak, firstDate, currentPreClose
}

func marketLimitUpTierStockFromTick(tick collectorpkg.StockTick, streak int, firstDate, tradingDate string, limitPublic *collectorpkg.LimitSidePublic) marketLimitUpTierStock {
	stock := marketLimitUpTierStock{
		Code:           tick.Code,
		Name:           tick.Name,
		Exchange:       tick.Exchange,
		IsST:           marketScreenIsSTStock(tick.Name),
		Price:          tick.Last,
		ChangePct:      tick.PctChange,
		Amount:         tick.Amount,
		Volume:         tick.Volume,
		Streak:         streak,
		FirstLimitDate: firstDate,
		LastLimitDate:  tradingDate,
		BoardType:      marketLimitUpBoardType(tick.Open, tick.High, tick.Low, tick.PreClose, tick.Code, tick.Name),
	}
	applyMarketLimitUpTierLimitPublic(&stock, limitPublic)
	return stock
}

func applyMarketLimitUpTierLimitPublic(stock *marketLimitUpTierStock, p *collectorpkg.LimitSidePublic) {
	if p == nil {
		return
	}
	if p.FirstSeen != "" {
		firstSeen := marketLimitObservedTimeOfDay(p.FirstSeen)
		stock.LimitFirstSeen = &firstSeen
	}
	breakCount := p.BreakCount
	stock.LimitBreakCount = &breakCount
}

func marketLimitObservedTimeOfDay(raw string) string {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.In(time.Local).Format("15:04:05")
	}
	return raw
}

func marketLimitUpTickerStreak(tick collectorpkg.StockTick, tradingDate string, previousRows []marketLimitUpTierKlineRow) (int, string) {
	streak := 1
	firstDate := tradingDate
	for i := 0; i+1 < len(previousRows); i++ {
		row := previousRows[i]
		preClose := previousRows[i+1].close.Float64()
		if !marketScreenPriceTouchesLimitUp(row.close.Float64(), preClose, tick.Code, tick.Name) {
			break
		}
		streak++
		firstDate = time.Unix(row.date, 0).In(time.Local).Format("20060102")
	}
	return streak, firstDate
}

func marketLimitUpBoardType(open, high, low, preClose float64, code, name string) string {
	if marketScreenPriceTouchesLimitDown(low, preClose, code, name) {
		return "floor_sky"
	}
	if marketScreenPriceTouchesLimitUp(open, preClose, code, name) &&
		marketScreenPriceTouchesLimitUp(high, preClose, code, name) &&
		marketScreenPriceTouchesLimitUp(low, preClose, code, name) {
		return "one_line"
	}
	if marketScreenPriceTouchesLimitUp(open, preClose, code, name) &&
		marketScreenPriceTouchesLimitUp(high, preClose, code, name) &&
		!marketScreenPriceTouchesLimitUp(low, preClose, code, name) {
		return "t_board"
	}
	return "turnover_board"
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
	tick.IsLimitUp = marketScreenPriceTouchesLimitUp(price, preClose, tick.Code, tick.Name)
	tick.IsLimitDown = marketScreenPriceTouchesLimitDown(price, preClose, tick.Code, tick.Name)
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

func parseMarketLimitStatsRequest(r *http.Request) (marketLimitStatsRequest, error) {
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketLimitStatsRequest{}, errors.New("trading_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		}
		tradingDate = parsed
	}
	return marketLimitStatsRequest{
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func buildMarketLimitStatsTickerResponse(req marketLimitStatsRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if !marketLimitStatsShouldUseTicker(req, ts) {
		return nil, false
	}
	resp := buildMarketLimitStatsBreakdownData(ts.GetLimitStatsBreakdown())
	resp["data_source"] = "ticker"
	resp["trading_date"] = ts.UpdatedAt().In(time.Local).Format("20060102")
	addTickerMeta(resp, ts)
	return resp, true
}

func marketLimitStatsShouldUseTicker(req marketLimitStatsRequest, ts *collectorpkg.TickerService) bool {
	if req.hasTradingDate || ts == nil {
		return false
	}
	updatedAt := ts.UpdatedAt()
	if updatedAt.IsZero() {
		return false
	}
	now := marketScreenNow()
	if !marketScreenInTickerSession(now) {
		return false
	}
	if updatedAt.In(time.Local).Format("20060102") != now.In(time.Local).Format("20060102") {
		return false
	}
	if now.Before(updatedAt) {
		return true
	}
	return now.Sub(updatedAt) < 10*time.Second
}

func marketScreenInTickerSession(now time.Time) bool {
	if !marketScreenIsTodayTradingDay(now) {
		return false
	}
	local := now.In(time.Local)
	minutes := local.Hour()*60 + local.Minute()
	return minutes >= 9*60+15 && minutes <= 15*60+5
}

func buildMarketLimitStatsCloseSnapshotResponse(req marketLimitStatsRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	resp := buildMarketLimitStatsBreakdownData(collectorpkg.ComputeLimitStatsBreakdown(ticks))
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func parseMarketLimitUpTiersRequest(r *http.Request) (marketLimitUpTiersRequest, error) {
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketLimitUpTiersRequest{}, errors.New("trading_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		}
		tradingDate = parsed
	}

	stockClass := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("stock_class")))
	if stockClass == "" {
		stockClass = "non_st"
	}
	switch stockClass {
	case "non_st", "st", "all":
	default:
		return marketLimitUpTiersRequest{}, errors.New("stock_class 参数无效，应为 non_st、st 或 all")
	}

	minStreak := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("min_streak")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return marketLimitUpTiersRequest{}, errors.New("min_streak 参数无效，应为正整数")
		}
		minStreak = n
	}

	return marketLimitUpTiersRequest{
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
		stockClass:     stockClass,
		minStreak:      minStreak,
	}, nil
}

func buildMarketLimitUpTiersResponse(req marketLimitUpTiersRequest, ts *collectorpkg.TickerService) (map[string]interface{}, bool) {
	if tickerByCode, limitUpByCode, tradingDate, ok := marketLimitUpTiersTickerSnapshot(req, ts); ok {
		resp, hasTarget := buildMarketLimitUpTiersData(req, tradingDate, tickerByCode, limitUpByCode)
		if hasTarget {
			addTickerMeta(resp, ts)
			return resp, true
		}
	}

	tradingDate := req.tradingDate
	if tradingDate == "" {
		if _, latestDate, ok := loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), ""); ok {
			tradingDate = latestDate
		}
	}
	if tradingDate == "" {
		return nil, false
	}

	resp, hasTarget := buildMarketLimitUpTiersData(req, tradingDate, nil, nil)
	if hasTarget {
		addMarketLimitUpTiersCloseMeta(resp, tradingDate)
		return resp, true
	}
	if req.hasTradingDate {
		resp := buildMarketLimitUpTiersPayload(req, tradingDate, nil)
		resp["updated_at"] = nil
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	return nil, false
}

func marketLimitUpTiersTickerSnapshot(req marketLimitUpTiersRequest, ts *collectorpkg.TickerService) (map[string]collectorpkg.StockTick, map[string]*collectorpkg.LimitSidePublic, string, bool) {
	if !marketScreenShouldUseTicker(marketScreenRequest{
		tradingDate:    req.tradingDate,
		hasTradingDate: req.hasTradingDate,
	}, ts) {
		return nil, nil, "", false
	}
	ticks := ts.GetAllStocks()
	if len(ticks) == 0 {
		return nil, nil, "", false
	}
	byCode := make(map[string]collectorpkg.StockTick, len(ticks))
	limitUpByCode := make(map[string]*collectorpkg.LimitSidePublic, len(ticks))
	for _, tick := range ticks {
		if tick.AssetType == string(collectorpkg.AssetTypeStock) {
			key := strings.ToLower(tick.Code)
			byCode[key] = tick
			if p := ts.GetLimitUpPublic(tick.Code); p != nil {
				limitUpByCode[key] = p
			}
		}
	}
	return byCode, limitUpByCode, ts.UpdatedAt().In(time.Local).Format("20060102"), len(byCode) > 0
}

func buildMarketLimitUpTiersData(req marketLimitUpTiersRequest, tradingDate string, tickerByCode map[string]collectorpkg.StockTick, limitUpByCode map[string]*collectorpkg.LimitSidePublic) (map[string]interface{}, bool) {
	if tickerByCode == nil && limitUpByCode == nil {
		if stocks, ok := loadMarketLimitUpTiersMaterialized(req, tradingDate); ok {
			return buildMarketLimitUpTiersPayload(req, tradingDate, stocks), true
		}
		stocks, hasTarget := buildMarketLimitUpTierStocksFromKline(tradingDate)
		if hasTarget {
			saveMarketLimitUpTiersMaterialized(tradingDate, stocks)
			return buildMarketLimitUpTiersPayload(req, tradingDate, filterMarketLimitUpTierStocks(stocks, req)), true
		}
		return nil, false
	}

	codes, err := loadMarketScreenCodeRows(string(collectorpkg.AssetTypeStock))
	if err != nil || len(codes) == 0 {
		return nil, false
	}

	stocks := make([]marketLimitUpTierStock, 0, 64)
	hasTarget := false
	for _, code := range codes {
		key := strings.ToLower(code.fullCode)
		if tick, ok := tickerByCode[key]; ok {
			hasTarget = true
			if !marketLimitUpTiersStockClassMatches(code.name, req.stockClass) || !tick.IsLimitUp {
				continue
			}
			previousRows := loadMarketLimitUpTierPreviousRows(code, tradingDate)
			streak, firstDate := marketLimitUpTickerStreak(tick, tradingDate, previousRows)
			if streak < req.minStreak {
				continue
			}
			stocks = append(stocks, marketLimitUpTierStockFromTick(tick, streak, firstDate, tradingDate, limitUpByCode[key]))
			continue
		}

		rows, ok := loadMarketLimitUpTierRows(code, tradingDate)
		if ok {
			hasTarget = true
		}
		if !ok || !marketLimitUpTiersStockClassMatches(code.name, req.stockClass) {
			continue
		}
		stock, ok := marketLimitUpTierStockFromRows(code, rows, tradingDate)
		if !ok || stock.Streak < req.minStreak {
			continue
		}
		stocks = append(stocks, stock)
	}

	return buildMarketLimitUpTiersPayload(req, tradingDate, stocks), hasTarget
}

func marketLimitUpTiersStockClassMatches(name, stockClass string) bool {
	isST := marketScreenIsSTStock(name)
	switch stockClass {
	case "st":
		return isST
	case "all":
		return true
	default:
		return !isST
	}
}

func buildMarketLimitUpTiersPayload(req marketLimitUpTiersRequest, tradingDate string, stocks []marketLimitUpTierStock) map[string]interface{} {
	sort.Slice(stocks, func(i, j int) bool {
		if stocks[i].Streak != stocks[j].Streak {
			return stocks[i].Streak > stocks[j].Streak
		}
		if stocks[i].Amount != stocks[j].Amount {
			return stocks[i].Amount > stocks[j].Amount
		}
		return stocks[i].Code < stocks[j].Code
	})

	grouped := make(map[int][]marketLimitUpTierStock)
	streaks := make([]int, 0)
	seen := make(map[int]bool)
	highest := 0
	for _, stock := range stocks {
		grouped[stock.Streak] = append(grouped[stock.Streak], stock)
		if !seen[stock.Streak] {
			seen[stock.Streak] = true
			streaks = append(streaks, stock.Streak)
		}
		if stock.Streak > highest {
			highest = stock.Streak
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(streaks)))

	tiers := make([]marketLimitUpTier, 0, len(streaks))
	for _, streak := range streaks {
		items := grouped[streak]
		tiers = append(tiers, marketLimitUpTier{
			Streak: streak,
			Label:  strconv.Itoa(streak) + "板",
			Count:  len(items),
			Stocks: items,
		})
	}

	return map[string]interface{}{
		"trading_date":   tradingDate,
		"stock_class":    req.stockClass,
		"min_streak":     req.minStreak,
		"total":          len(stocks),
		"highest_streak": highest,
		"tiers":          tiers,
	}
}

func addMarketLimitUpTiersCloseMeta(resp map[string]interface{}, tradingDate string) {
	resp["status"] = "closed_snapshot"
	resp["status_hint"] = "已使用本地日K收盘快照计算连板梯队"
	if parsed, err := time.ParseInLocation("20060102", tradingDate, time.Local); err == nil {
		resp["updated_at"] = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 15, 0, 0, 0, time.Local).Format(time.RFC3339)
	}
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
	membersByGroup, err := loadMarketScreenBlockMembersByGroup(req.key)
	if err != nil {
		return nil
	}
	ranks := make([]collectorpkg.BlockRank, 0, len(groups))
	for _, group := range groups {
		members := membersByGroup[marketScreenBlockMemberKey(group.Source, group.BlockType, group.Name)]
		if len(members) == 0 {
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

func parseMarketSignalRequest(r *http.Request) (marketSignalRequest, error) {
	typeFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("type")))
	if typeFilter == "" {
		typeFilter = "all"
	}
	switch typeFilter {
	case "all", "new_high", "new_low", "volume_spike":
	default:
		return marketSignalRequest{}, errors.New("type 参数无效，支持 all|new_high|new_low|volume_spike")
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketSignalRequest{}, err
		}
		tradingDate = parsed
	}
	return marketSignalRequest{
		typeFilter:     typeFilter,
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func buildMarketSignalSnapshotResponse(req marketSignalRequest, snap collectorpkg.SignalSnapshot, apiStatus string) (map[string]interface{}, bool) {
	if !marketSignalShouldUseSnapshot(req, snap) {
		return nil, false
	}
	resp := buildMarketSignalResponse(req.typeFilter, snap.NewHigh, snap.NewLow, snap.VolumeSpike)
	resp["status"] = apiStatus
	resp["updated_at"] = snap.UpdatedAt.Format(time.RFC3339)
	resp["scan_duration_ms"] = snap.ScanDurationMs
	resp["data_source"] = "signal_service"
	resp["trading_date"] = snap.UpdatedAt.In(time.Local).Format("20060102")
	if apiStatus == "not_ready" {
		resp["status_hint"] = "首轮 K 线扫描尚未完成，请稍后重试"
	}
	if apiStatus == "scanning" {
		resp["status_hint"] = "正在扫描中，以下为上一轮完整结果"
	}
	if apiStatus == "stale" {
		resp["status_hint"] = "结果已超过新鲜度阈值，可能过期"
	}
	return resp, true
}

func marketSignalShouldUseSnapshot(req marketSignalRequest, snap collectorpkg.SignalSnapshot) bool {
	if snap.UpdatedAt.IsZero() {
		return false
	}
	updatedDate := snap.UpdatedAt.In(time.Local).Format("20060102")
	if req.hasTradingDate {
		return updatedDate == req.tradingDate
	}
	now := marketScreenNow()
	if !marketScreenIsTodayTradingDay(now) {
		return false
	}
	return updatedDate == now.In(time.Local).Format("20060102")
}

func buildMarketSignalCloseSnapshotResponse(req marketSignalRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks("stock", req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	var items []collectorpkg.SignalItem
	if ok {
		items = buildMarketScreenSignalItems(ticks, tradingDate, marketSignalTypesForFilter(req.typeFilter))
	}
	newHigh, newLow, volumeSpike := splitMarketSignalItems(items)
	resp := buildMarketSignalResponse(req.typeFilter, newHigh, newLow, volumeSpike)
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func buildMarketSignalResponse(typeFilter string, newHigh, newLow, volumeSpike []collectorpkg.SignalItem) map[string]interface{} {
	resp := map[string]interface{}{
		"updated_at":       nil,
		"scan_duration_ms": int64(0),
		"new_high":         newHigh,
		"new_low":          newLow,
		"volume_spike":     volumeSpike,
	}
	switch typeFilter {
	case "new_high":
		resp["list"] = newHigh
		resp["count"] = len(newHigh)
	case "new_low":
		resp["list"] = newLow
		resp["count"] = len(newLow)
	case "volume_spike":
		resp["list"] = volumeSpike
		resp["count"] = len(volumeSpike)
	default:
		resp["count"] = len(newHigh) + len(newLow) + len(volumeSpike)
	}
	return resp
}

func parseMarketSignalCheckRequest(r *http.Request) (marketSignalCheckRequest, error) {
	fullCodes := normalizeSignalCheckFullCodes(splitCodes(strings.TrimSpace(r.URL.Query().Get("full_codes"))))
	if len(fullCodes) == 0 {
		return marketSignalCheckRequest{}, errors.New("full_codes 为必填参数")
	}
	signalTypes, err := normalizeMarketSignalTypes(splitCodes(strings.TrimSpace(r.URL.Query().Get("signal_types"))))
	if err != nil {
		return marketSignalCheckRequest{}, err
	}
	mode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "hits_only"
	}
	if mode != "hits_only" && mode != "full" {
		return marketSignalCheckRequest{}, errors.New("mode 仅支持 hits_only 或 full")
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	tradingDate := ""
	if tradingDateRaw != "" {
		parsed, err := parseMarketScreenTradingDate(tradingDateRaw)
		if err != nil {
			return marketSignalCheckRequest{}, err
		}
		tradingDate = parsed
	}
	return marketSignalCheckRequest{
		fullCodes:      fullCodes,
		signalTypes:    signalTypes,
		mode:           mode,
		tradingDate:    tradingDate,
		hasTradingDate: tradingDate != "",
	}, nil
}

func buildMarketSignalCheckTickerResponse(req marketSignalCheckRequest, ss *collectorpkg.SignalService, ts *collectorpkg.TickerService) (map[string]interface{}, bool, error) {
	if ss == nil || !marketScreenShouldUseTicker(marketScreenRequest{
		tradingDate:    req.tradingDate,
		hasTradingDate: req.hasTradingDate,
	}, ts) {
		return nil, false, nil
	}
	hits, err := ss.CheckCodes(req.fullCodes, req.signalTypes)
	if err != nil {
		return nil, true, err
	}
	resp := buildSignalCheckPayload(req.fullCodes, req.signalTypes, req.mode, hits, time.Now())
	resp["data_source"] = "signal_service"
	resp["trading_date"] = ts.UpdatedAt().In(time.Local).Format("20060102")
	addTickerMeta(resp, ts)
	return resp, true, nil
}

func buildMarketSignalCheckCloseSnapshotResponse(req marketSignalCheckRequest) (map[string]interface{}, bool) {
	ticks, tradingDate, ok := loadMarketScreenCloseTicks("stock", req.tradingDate)
	if !ok && !req.hasTradingDate {
		return nil, false
	}
	hits := make([]collectorpkg.SignalItem, 0)
	if ok {
		tickByCode := make(map[string]collectorpkg.StockTick, len(ticks)*2)
		for _, tick := range ticks {
			addMarketScreenTickLookup(tickByCode, tick)
		}
		for _, fullCode := range req.fullCodes {
			tick, ok := tickByCode[fullCode]
			if !ok {
				continue
			}
			hits = append(hits, buildMarketScreenSignalItems([]collectorpkg.StockTick{tick}, tradingDate, req.signalTypes)...)
		}
	}
	resp := buildSignalCheckPayload(req.fullCodes, req.signalTypes, req.mode, hits, time.Now())
	if req.hasTradingDate && !ok {
		addMarketScreenCloseSnapshotMeta(resp, req.tradingDate)
		resp["status"] = "empty"
		resp["status_hint"] = "指定 trading_date 无日K收盘快照"
		return resp, true
	}
	addMarketScreenCloseSnapshotMeta(resp, tradingDate)
	return resp, true
}

func marketSignalTypesForFilter(typeFilter string) []string {
	switch typeFilter {
	case "new_high", "new_low", "volume_spike":
		return []string{typeFilter}
	default:
		return []string{"new_high", "new_low", "volume_spike"}
	}
}

func normalizeMarketSignalTypes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("signal_types 为必填参数")
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		typ := strings.TrimSpace(strings.ToLower(item))
		switch typ {
		case "new_high", "new_low", "volume_spike":
		default:
			return nil, errors.New("unsupported signal type: " + typ)
		}
		if _, ok := seen[typ]; ok {
			continue
		}
		seen[typ] = struct{}{}
		out = append(out, typ)
	}
	if len(out) == 0 {
		return nil, errors.New("signal_types 为必填参数")
	}
	return out, nil
}

func normalizeSignalCheckFullCodes(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		fullCode := strings.ToLower(strings.TrimSpace(item))
		if fullCode == "" || bareCode(fullCode) == fullCode {
			continue
		}
		if _, ok := seen[fullCode]; ok {
			continue
		}
		seen[fullCode] = struct{}{}
		out = append(out, fullCode)
	}
	return out
}

func buildMarketScreenSignalItems(ticks []collectorpkg.StockTick, tradingDate string, signalTypes []string) []collectorpkg.SignalItem {
	filter := make(map[string]bool, len(signalTypes))
	for _, typ := range signalTypes {
		filter[typ] = true
	}
	items := make([]collectorpkg.SignalItem, 0)
	for i := range ticks {
		rows, ok := loadMarketScreenSignalRows(ticks[i].Code, tradingDate, 26)
		if !ok {
			continue
		}
		for _, item := range evaluateMarketScreenSignalItems(&ticks[i], rows, 20, 5, 2.0) {
			if filter[item.SignalType] {
				items = append(items, item)
			}
		}
	}
	sortMarketScreenSignalItems(items)
	return items
}

func loadMarketScreenSignalRows(fullCode, tradingDate string, limit int) ([]collectorpkg.KlinePublishRow, bool) {
	if tradingDate == "" {
		return nil, false
	}
	target, err := time.ParseInLocation("20060102", tradingDate, time.Local)
	if err != nil {
		return nil, false
	}
	end := time.Date(target.Year(), target.Month(), target.Day()+1, 0, 0, 0, 0, time.Local).Unix()
	dbPath := filepath.Join(databaseDir, "kline", fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, false
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, false
	}
	defer db.Close()

	rows, err := db.Query(`SELECT Code, Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date < ? ORDER BY Date DESC LIMIT ?`, fullCode, end, limit)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	out := make([]collectorpkg.KlinePublishRow, 0, limit)
	for rows.Next() {
		var row collectorpkg.KlinePublishRow
		if err := rows.Scan(&row.Code, &row.Date, &row.Open, &row.High, &row.Low, &row.Close, &row.Volume, &row.Amount); err != nil {
			return nil, false
		}
		out = append(out, row)
	}
	if rows.Err() != nil || len(out) == 0 {
		return nil, false
	}
	if time.Unix(out[0].Date, 0).In(time.Local).Format("20060102") != tradingDate {
		return nil, false
	}
	return out, true
}

func evaluateMarketScreenSignalItems(tick *collectorpkg.StockTick, rows []collectorpkg.KlinePublishRow, win, vlb int, volumeRatioMin float64) []collectorpkg.SignalItem {
	if tick == nil || len(rows) < win+1 {
		return nil
	}
	if 1+win > len(rows) {
		return nil
	}
	hist := rows[1 : 1+win]
	var maxHigh, minLow collectorpkg.PriceMilli
	for i := range hist {
		if i == 0 {
			maxHigh = hist[i].High
			minLow = hist[i].Low
			continue
		}
		if hist[i].High > maxHigh {
			maxHigh = hist[i].High
		}
		if hist[i].Low < minLow {
			minLow = hist[i].Low
		}
	}

	items := make([]collectorpkg.SignalItem, 0, 3)
	refHigh := maxHigh.Float64()
	refLow := minLow.Float64()
	if tick.High >= refHigh-1e-9 {
		items = append(items, collectorpkg.SignalItem{
			Code:       tick.Code,
			Name:       tick.Name,
			SignalType: "new_high",
			Window:     win,
			Price:      tick.Last,
			High:       refHigh,
			Low:        tick.Low,
			Volume:     tick.Volume,
			ChangePct:  tick.PctChange,
		})
	}
	if tick.Low <= refLow+1e-9 {
		items = append(items, collectorpkg.SignalItem{
			Code:       tick.Code,
			Name:       tick.Name,
			SignalType: "new_low",
			Window:     win,
			Price:      tick.Last,
			Low:        refLow,
			High:       tick.High,
			Volume:     tick.Volume,
			ChangePct:  tick.PctChange,
		})
	}
	if 1+win+vlb <= len(rows) {
		volSlice := rows[1+win : 1+win+vlb]
		var sum int64
		for _, row := range volSlice {
			sum += row.Volume
		}
		avg := float64(sum) / float64(len(volSlice))
		if avg > 0 && float64(tick.Volume)/avg >= volumeRatioMin {
			items = append(items, collectorpkg.SignalItem{
				Code:        tick.Code,
				Name:        tick.Name,
				SignalType:  "volume_spike",
				Window:      vlb,
				Price:       tick.Last,
				Volume:      tick.Volume,
				AvgVolume:   avg,
				VolumeRatio: float64(tick.Volume) / avg,
				ChangePct:   tick.PctChange,
			})
		}
	}
	return items
}

func splitMarketSignalItems(items []collectorpkg.SignalItem) ([]collectorpkg.SignalItem, []collectorpkg.SignalItem, []collectorpkg.SignalItem) {
	newHigh := make([]collectorpkg.SignalItem, 0)
	newLow := make([]collectorpkg.SignalItem, 0)
	volumeSpike := make([]collectorpkg.SignalItem, 0)
	for _, item := range items {
		switch item.SignalType {
		case "new_high":
			newHigh = append(newHigh, item)
		case "new_low":
			newLow = append(newLow, item)
		case "volume_spike":
			volumeSpike = append(volumeSpike, item)
		}
	}
	return newHigh, newLow, volumeSpike
}

func sortMarketScreenSignalItems(items []collectorpkg.SignalItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].SignalType < items[j].SignalType
	})
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

func paginateMarketScreenTicks(ticks []collectorpkg.StockTick, page, pageSize int) ([]collectorpkg.StockTick, marketScreenPagination) {
	pagination := newMarketScreenPagination(page, pageSize, len(ticks))
	offset := marketScreenPageOffset(pagination.page, pagination.pageSize)
	if offset >= len(ticks) {
		return []collectorpkg.StockTick{}, pagination
	}
	end := offset + pagination.pageSize
	if end > len(ticks) {
		end = len(ticks)
	}
	return ticks[offset:end], pagination
}

func normalizeMarketScreenPageSize(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizeMarketScreenPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func marketScreenPageOffset(page, pageSize int) int {
	return (normalizeMarketScreenPage(page) - 1) * normalizeMarketScreenPageSize(pageSize)
}

func newMarketScreenPagination(page, pageSize, total int) marketScreenPagination {
	page = normalizeMarketScreenPage(page)
	pageSize = normalizeMarketScreenPageSize(pageSize)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return marketScreenPagination{
		page:       page,
		pageSize:   pageSize,
		total:      total,
		totalPages: totalPages,
		hasNext:    page < totalPages,
		hasPrev:    page > 1 && total > 0,
	}
}

func addMarketScreenPaginationMeta(resp map[string]interface{}, pagination marketScreenPagination) {
	resp["total"] = pagination.total
	resp["page"] = pagination.page
	resp["page_size"] = pagination.pageSize
	resp["total_pages"] = pagination.totalPages
	resp["has_next"] = pagination.hasNext
	resp["has_prev"] = pagination.hasPrev
}

func marketScreenRequestMatchesChangePct(pct float64, req marketScreenRequest) bool {
	if req.hasMinChangePct && pct < req.minChangePct {
		return false
	}
	if req.hasMaxChangePct && pct > req.maxChangePct {
		return false
	}
	return true
}

func marketScreenLimitUp(pct float64, code, name string) bool {
	return pct >= marketScreenLimitThreshold(code, name)-0.05
}

func marketScreenLimitDown(pct float64, code, name string) bool {
	return pct <= -(marketScreenLimitThreshold(code, name) - 0.05)
}

func marketScreenPriceTouchesLimitUp(price, preClose float64, code, name string) bool {
	if price <= 0 || preClose <= 0 {
		return false
	}
	return (price-preClose)/preClose*100 >= marketScreenLimitThreshold(code, name)-0.05
}

func marketScreenPriceTouchesLimitDown(price, preClose float64, code, name string) bool {
	if price <= 0 || preClose <= 0 {
		return false
	}
	return (price-preClose)/preClose*100 <= -(marketScreenLimitThreshold(code, name) - 0.05)
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
		strings.HasPrefix(bare, "82"), strings.HasPrefix(bare, "43"),
		strings.HasPrefix(bare, "92"):
		return 30
	default:
		if marketScreenIsSTStock(name) {
			return 5
		}
		return 10
	}
}

func marketScreenIsSTStock(name string) bool {
	return strings.Contains(strings.ToUpper(name), "ST")
}

func roundMarketScreen(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}
