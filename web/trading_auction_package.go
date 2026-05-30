package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/protocol"
)

const (
	tradingAuctionPrecisionQuoteSnapshot  = "QUOTE_SNAPSHOT"
	tradingAuctionPrecisionAuctionHistory = "AUCTION_HISTORY"
	tradingAuctionAvailabilityAvailable   = "AVAILABLE"
	tradingAuctionAvailabilityPartial     = "PARTIAL"
	tradingAuctionAvailabilityMissing     = "MISSING"
)

var (
	tradingAuctionNow          = time.Now
	tradingAuctionQuoteFetcher = func(codes ...string) (protocol.QuotesResp, error) {
		if client == nil {
			return nil, errors.New("TDX client 未初始化")
		}
		return client.GetQuote(codes...)
	}
)

type tradingAuctionPackageRequest struct {
	Market                       string   `json:"market"`
	TradeDate                    string   `json:"trade_date"`
	AuctionPhase                 string   `json:"auction_phase"`
	SnapshotTime                 string   `json:"snapshot_time"`
	PlannedFullCodes             []string `json:"planned_full_codes"`
	YDLimitUpFullCodes           []string `json:"yd_limit_up_full_codes"`
	YDChainFullCodes             []string `json:"yd_chain_full_codes"`
	YDHighBoardFullCodes         []string `json:"yd_high_board_full_codes"`
	WatchedBlockIDs              []string `json:"watched_block_ids"`
	IncludeAuctionBornCandidates bool     `json:"include_auction_born_candidates"`
	CandidateBlockTypes          []string `json:"candidate_block_types"`
	CandidateLimit               int      `json:"candidate_limit"`
	LowOpenThresholdPct          float64  `json:"low_open_threshold_pct"`
}

type tradingAuctionPackageResponse struct {
	TradeDate             string                         `json:"trade_date"`
	AuctionPhase          string                         `json:"auction_phase"`
	SnapshotTime          string                         `json:"snapshot_time"`
	AuctionMkt            tradingAuctionMarket           `json:"auction_mkt"`
	PlannedNames          []tradingAuctionSnapshotItem   `json:"planned_names"`
	YDLimitUpNames        []tradingAuctionSnapshotItem   `json:"yd_limit_up_names"`
	YDChainNames          []tradingAuctionSnapshotItem   `json:"yd_chain_names"`
	YDHighBoardNames      []tradingAuctionSnapshotItem   `json:"yd_high_board_names"`
	BlockMembers          []tradingAuctionBlockMembers   `json:"block_members"`
	AuctionBornCandidates []tradingAuctionCandidateGroup `json:"auction_born_candidates"`
	Failures              []tradingAuctionFailure        `json:"failures"`
}

type tradingAuctionMarket struct {
	Availability            string                   `json:"availability"`
	Precision               string                   `json:"precision"`
	LimitUp                 any                      `json:"limit_up,omitempty"`
	LimitDown               any                      `json:"limit_down,omitempty"`
	ByStockClass            any                      `json:"by_stock_class,omitempty"`
	DataSource              string                   `json:"data_source,omitempty"`
	Status                  string                   `json:"status,omitempty"`
	StatusHint              string                   `json:"status_hint,omitempty"`
	TradingDate             string                   `json:"trading_date,omitempty"`
	UpdatedAt               string                   `json:"updated_at,omitempty"`
	MissingFields           []string                 `json:"missing_fields"`
	HighBoard               tradingAuctionGroupStats `json:"high_board"`
	YDLimitUpChainFeedback  tradingAuctionGroupStats `json:"yd_limit_up_chain_feedback"`
	WatchedBlockCoreAuction tradingAuctionGroupStats `json:"watched_block_core_auction"`
}

type tradingAuctionGroupStats struct {
	SampleCount                int                         `json:"sample_count"`
	AvailableCount             int                         `json:"available_count"`
	LowOpenThresholdPct        float64                     `json:"low_open_threshold_pct,omitempty"`
	LowOpenCount               int                         `json:"low_open_count,omitempty"`
	AvgAuctionPct              *float64                    `json:"avg_auction_pct"`
	AboveAvgAuctionAmountCount int                         `json:"above_avg_auction_amount_count,omitempty"`
	WeakSymbols                []string                    `json:"weak_symbols,omitempty"`
	Items                      []tradingAuctionCompactItem `json:"items,omitempty"`
}

type tradingAuctionCompactItem struct {
	Symbol        string  `json:"symbol"`
	Name          string  `json:"name"`
	AuctionPct    float64 `json:"auction_pct"`
	AuctionAmount float64 `json:"auction_amount"`
	IsLimitUpOpen bool    `json:"is_limit_up_open"`
}

type tradingAuctionSnapshotItem struct {
	FullCode                       string   `json:"full_code"`
	Symbol                         string   `json:"symbol"`
	Name                           string   `json:"name"`
	TradeDate                      string   `json:"trade_date"`
	SnapshotTime                   string   `json:"snapshot_time"`
	AuctionPhase                   string   `json:"auction_phase"`
	IsST                           bool     `json:"is_st"`
	AuctionPrice                   float64  `json:"auction_price"`
	PrevClose                      float64  `json:"prev_close"`
	AuctionPct                     float64  `json:"auction_pct"`
	AuctionAmount                  float64  `json:"auction_amount"`
	AvgAuctionAmount5D             float64  `json:"avg_auction_amount_5d,omitempty"`
	AvgAuctionAmount5DAvailability string   `json:"avg_auction_amount_5d_availability,omitempty"`
	AmountVsAvgAuction5D           float64  `json:"amount_vs_avg_auction_5d,omitempty"`
	IsLimitUpOpen                  bool     `json:"is_limit_up_open"`
	Bid1Price                      float64  `json:"bid1_price,omitempty"`
	Bid1Volume                     int      `json:"bid1_volume,omitempty"`
	QuoteTime                      string   `json:"quote_time"`
	Availability                   string   `json:"availability"`
	Precision                      string   `json:"precision"`
	MissingFields                  []string `json:"missing_fields"`
}

type tradingAuctionBlockMembers struct {
	BlockID   string   `json:"block_id"`
	Name      string   `json:"name"`
	BlockType string   `json:"block_type"`
	Source    string   `json:"source"`
	Members   []string `json:"members"`
}

type tradingAuctionCandidateBlock struct {
	Group   collectorpkg.BlockGroupRecord
	Members []string
}

type tradingAuctionCandidateGroup struct {
	BlockID           string   `json:"block_id"`
	Theme             string   `json:"theme"`
	BlockType         string   `json:"block_type"`
	SymbolCount       int      `json:"symbol_count"`
	AvgAuctionPct     float64  `json:"avg_auction_pct"`
	OneLineCount      int      `json:"one_line_count"`
	FrontlineSymbols  []string `json:"frontline_symbols"`
	GroupBehavior     string   `json:"group_behavior"`
	FrontlineBehavior string   `json:"frontline_behavior"`
}

type tradingAuctionFailure struct {
	FullCode string `json:"full_code,omitempty"`
	BlockID  string `json:"block_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Message  string `json:"message"`
}

type tradingAuctionOpeningFact struct {
	Price  float64
	Amount float64
}

func handleTradingAuctionPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}

	var req tradingAuctionPackageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}
	resp, err := buildTradingAuctionPackage(r.Context(), req)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	successResponse(w, resp)
}

func buildTradingAuctionPackage(ctx context.Context, req tradingAuctionPackageRequest) (tradingAuctionPackageResponse, error) {
	if strings.ToUpper(strings.TrimSpace(req.Market)) != "CN" {
		return tradingAuctionPackageResponse{}, errors.New("market only supports CN")
	}
	tradeDay, err := parseWorkdayDate(strings.TrimSpace(req.TradeDate))
	if err != nil {
		return tradingAuctionPackageResponse{}, errors.New("trade_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
	}
	phase := strings.ToUpper(strings.TrimSpace(req.AuctionPhase))
	switch phase {
	case "PRE_AUCTION", "CANCELLABLE", "FINAL":
	default:
		return tradingAuctionPackageResponse{}, errors.New("auction_phase 参数无效，仅支持 PRE_AUCTION/CANCELLABLE/FINAL")
	}
	snapshotTime := strings.TrimSpace(req.SnapshotTime)
	if _, err := time.ParseInLocation("15:04:05", snapshotTime, time.Local); err != nil {
		return tradingAuctionPackageResponse{}, errors.New("snapshot_time 参数格式错误，应为 HH:MM:SS")
	}
	if req.CandidateLimit <= 0 {
		req.CandidateLimit = 20
	}
	if req.LowOpenThresholdPct == 0 {
		req.LowOpenThresholdPct = -5
	}

	resp := tradingAuctionPackageResponse{
		TradeDate:    tradeDay.Format("2006-01-02"),
		AuctionPhase: phase,
		SnapshotTime: snapshotTime,
		AuctionMkt: tradingAuctionMarket{
			Availability:  tradingAuctionAvailabilityMissing,
			Precision:     tradingAuctionPrecisionQuoteSnapshot,
			MissingFields: []string{"seal_volume"},
		},
		PlannedNames:          []tradingAuctionSnapshotItem{},
		YDLimitUpNames:        []tradingAuctionSnapshotItem{},
		YDChainNames:          []tradingAuctionSnapshotItem{},
		YDHighBoardNames:      []tradingAuctionSnapshotItem{},
		BlockMembers:          []tradingAuctionBlockMembers{},
		AuctionBornCandidates: []tradingAuctionCandidateGroup{},
		Failures:              []tradingAuctionFailure{},
	}

	resp.AuctionMkt = applyTradingAuctionLimitStats(resp.AuctionMkt, tradeDay)
	resp.BlockMembers, resp.Failures = resolveTradingAuctionBlockMembers(req.WatchedBlockIDs, resp.Failures)
	candidateBlocks := collectTradingAuctionCandidateBlocks(req)
	explicitRows, err := tradingAuctionExplicitRows(req)
	if err != nil {
		return tradingAuctionPackageResponse{}, err
	}
	candidateRows := tradingAuctionRowsFromCandidateBlocks(candidateBlocks)
	allRows := mergeTradingAuctionRows(explicitRows, candidateRows)
	quotes := fetchMarketScreenQuotes(allRows, tradingAuctionQuoteFetcher)
	quoteTime := tradingAuctionQuoteTime(tradeDay, snapshotTime)
	now := tradingAuctionNow().In(time.Local)
	nowClock := now.Format("15:04:05")
	allowQuoteAuctionAmount := tradeDay.Format("20060102") == now.Format("20060102") && snapshotTime < "09:30:00" && nowClock >= "09:15:00" && nowClock < "09:30:00"
	explicitSet := tradingAuctionExplicitSet(req)

	itemsByCode := make(map[string]tradingAuctionSnapshotItem, len(allRows))
	for _, row := range allRows {
		quote := quotes[strings.ToLower(row.fullCode)]
		_, isExplicit := explicitSet[strings.ToLower(row.fullCode)]
		item := buildTradingAuctionSnapshotItem(ctx, row, quote, tradeDay, phase, snapshotTime, quoteTime, allowQuoteAuctionAmount, isExplicit, isExplicit && !allowQuoteAuctionAmount)
		itemsByCode[strings.ToLower(row.fullCode)] = item
	}

	resp.PlannedNames, resp.Failures = collectTradingAuctionExplicitItems("planned_names", req.PlannedFullCodes, itemsByCode, resp.Failures)
	resp.YDLimitUpNames, resp.Failures = collectTradingAuctionExplicitItems("yd_limit_up_names", req.YDLimitUpFullCodes, itemsByCode, resp.Failures)
	resp.YDChainNames, resp.Failures = collectTradingAuctionExplicitItems("yd_chain_names", req.YDChainFullCodes, itemsByCode, resp.Failures)
	resp.YDHighBoardNames, resp.Failures = collectTradingAuctionExplicitItems("yd_high_board_names", req.YDHighBoardFullCodes, itemsByCode, resp.Failures)

	resp.AuctionMkt.HighBoard = summarizeTradingAuctionItems(resp.YDHighBoardNames, req.LowOpenThresholdPct)
	resp.AuctionMkt.YDLimitUpChainFeedback = summarizeTradingAuctionItems(append(resp.YDLimitUpNames, resp.YDChainNames...), req.LowOpenThresholdPct)
	resp.AuctionMkt.WatchedBlockCoreAuction = summarizeTradingAuctionWatchedBlocks(resp.PlannedNames, resp.BlockMembers)

	if req.IncludeAuctionBornCandidates {
		resp.AuctionBornCandidates = buildTradingAuctionCandidateGroups(candidateBlocks, req.CandidateLimit, itemsByCode)
	}
	return resp, nil
}

func applyTradingAuctionLimitStats(mkt tradingAuctionMarket, tradeDay time.Time) tradingAuctionMarket {
	req := marketLimitStatsRequest{}
	if tradingDateStart(tradeDay).Before(tradingDateStart(tradingAuctionNow())) {
		req.tradingDate = tradeDay.Format("20060102")
		req.hasTradingDate = true
	}
	var data map[string]interface{}
	if resp, ok := buildMarketLimitStatsTickerResponse(req, getTickerService()); ok {
		data = resp
	} else if resp, ok := buildMarketLimitStatsCloseSnapshotResponse(req); ok {
		data = resp
	}
	if len(data) == 0 {
		mkt.Availability = tradingAuctionAvailabilityMissing
		return mkt
	}
	mkt.Availability = tradingAuctionAvailabilityAvailable
	mkt.LimitUp = data["limit_up"]
	mkt.LimitDown = data["limit_down"]
	mkt.ByStockClass = data["by_stock_class"]
	mkt.DataSource, _ = data["data_source"].(string)
	mkt.Status, _ = data["status"].(string)
	mkt.StatusHint, _ = data["status_hint"].(string)
	mkt.TradingDate, _ = data["trading_date"].(string)
	mkt.UpdatedAt, _ = data["updated_at"].(string)
	return mkt
}

func tradingAuctionExplicitSet(req tradingAuctionPackageRequest) map[string]struct{} {
	set := map[string]struct{}{}
	for _, group := range [][]string{req.PlannedFullCodes, req.YDLimitUpFullCodes, req.YDChainFullCodes, req.YDHighBoardFullCodes} {
		for _, raw := range group {
			fullCode := strings.ToLower(strings.TrimSpace(raw))
			if fullCode != "" {
				set[fullCode] = struct{}{}
			}
		}
	}
	return set
}

func tradingAuctionExplicitRows(req tradingAuctionPackageRequest) ([]marketScreenCodeRow, error) {
	all := append([]string{}, req.PlannedFullCodes...)
	all = append(all, req.YDLimitUpFullCodes...)
	all = append(all, req.YDChainFullCodes...)
	all = append(all, req.YDHighBoardFullCodes...)
	if len(all) == 0 {
		return nil, nil
	}
	known := tradingAuctionKnownCodeRows()
	rows := make([]marketScreenCodeRow, 0, len(all))
	for _, raw := range all {
		model, err := parseTradingInstrumentMetricModel(raw)
		if err != nil {
			return nil, err
		}
		row := marketScreenCodeRow{
			fullCode:  strings.ToLower(model.FullCode()),
			name:      model.Name,
			exchange:  strings.ToLower(model.Exchange),
			assetType: modelAssetType(model),
		}
		if cached, ok := known[row.fullCode]; ok {
			row.name = cached.name
			row.assetType = cached.assetType
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func tradingAuctionKnownCodeRows() map[string]marketScreenCodeRow {
	rows, err := loadMarketScreenCodeRows("all")
	if err != nil {
		return nil
	}
	out := make(map[string]marketScreenCodeRow, len(rows))
	for _, row := range rows {
		out[strings.ToLower(row.fullCode)] = row
	}
	return out
}

func mergeTradingAuctionRows(primary, extra []marketScreenCodeRow) []marketScreenCodeRow {
	seen := make(map[string]struct{}, len(primary)+len(extra))
	out := make([]marketScreenCodeRow, 0, len(primary)+len(extra))
	for _, rows := range [][]marketScreenCodeRow{primary, extra} {
		for _, row := range rows {
			row.fullCode = strings.ToLower(strings.TrimSpace(row.fullCode))
			if row.fullCode == "" {
				continue
			}
			if _, ok := seen[row.fullCode]; ok {
				continue
			}
			seen[row.fullCode] = struct{}{}
			if row.exchange == "" && len(row.fullCode) >= 2 {
				row.exchange = row.fullCode[:2]
			}
			if row.assetType == "" {
				row.assetType = classifyAssetType(row.fullCode)
			}
			out = append(out, row)
		}
	}
	return out
}

func buildTradingAuctionSnapshotItem(ctx context.Context, row marketScreenCodeRow, quote *protocol.Quote, tradeDay time.Time, phase, snapshotTime, quoteTime string, allowQuoteAuctionAmount, includeAvg, useLocalAuctionHistory bool) tradingAuctionSnapshotItem {
	fullCode := strings.ToLower(row.fullCode)
	item := tradingAuctionSnapshotItem{
		FullCode:      fullCode,
		Symbol:        tradingMetricSymbol(fullCode),
		Name:          row.name,
		TradeDate:     tradeDay.Format("2006-01-02"),
		SnapshotTime:  snapshotTime,
		AuctionPhase:  phase,
		IsST:          marketScreenIsSTStock(row.name),
		QuoteTime:     quoteTime,
		Availability:  tradingAuctionAvailabilityMissing,
		Precision:     tradingAuctionPrecisionQuoteSnapshot,
		MissingFields: []string{"seal_volume"},
	}

	quoteUsable := quote != nil && allowQuoteAuctionAmount
	if quoteUsable {
		if item.Name == "" {
			item.Name = row.name
		}
		item.PrevClose = roundMarketScreen(quote.K.Last.Float64(), 3)
		price := quote.K.Open.Float64()
		if price <= 0 {
			price = quote.K.Close.Float64()
		}
		item.AuctionPrice = roundMarketScreen(price, 3)
		if allowQuoteAuctionAmount {
			item.AuctionAmount = roundMarketScreen(quote.Amount, 2)
		}
		if quote.BuyLevel[0].Price > 0 {
			item.Bid1Price = roundMarketScreen(quote.BuyLevel[0].Price.Float64(), 3)
			item.Bid1Volume = quote.BuyLevel[0].Number
		}
	}
	if useLocalAuctionHistory {
		if prevClose, ok := loadTradingAuctionPrevClose(fullCode, tradeDay); ok {
			item.PrevClose = prevClose
		}
		if fact, ok, _ := loadTradingAuctionOpeningFact(fullCode, tradeDay); ok {
			item.AuctionPrice = fact.Price
			item.AuctionAmount = fact.Amount
			if quote == nil {
				item.Precision = tradingAuctionPrecisionAuctionHistory
			}
		}
	}
	if item.PrevClose > 0 && item.AuctionPrice > 0 {
		item.AuctionPct = roundMarketScreen((item.AuctionPrice/item.PrevClose-1)*100, 2)
		item.IsLimitUpOpen = marketScreenPriceTouchesLimitUp(item.AuctionPrice, item.PrevClose, item.FullCode, item.Name)
	}
	if includeAvg {
		avg, avgAvailability := loadTradingAuctionAvgAmount5D(ctx, fullCode, tradeDay)
		item.AvgAuctionAmount5DAvailability = avgAvailability
		if avg > 0 {
			item.AvgAuctionAmount5D = avg
			if item.AuctionAmount > 0 {
				item.AmountVsAvgAuction5D = roundMarketScreen(item.AuctionAmount/avg, 2)
			}
		}
	}

	item.MissingFields = tradingAuctionMissingFields(item, includeAvg)
	switch {
	case item.AuctionPrice > 0 && item.PrevClose > 0 && item.AuctionAmount > 0:
		item.Availability = tradingAuctionAvailabilityAvailable
	case quoteUsable || item.AuctionPrice > 0 || item.PrevClose > 0 || item.AuctionAmount > 0:
		item.Availability = tradingAuctionAvailabilityPartial
	default:
		item.Availability = tradingAuctionAvailabilityMissing
	}
	return item
}

func tradingAuctionMissingFields(item tradingAuctionSnapshotItem, includeAvg bool) []string {
	missing := []string{"seal_volume"}
	if item.AuctionPrice <= 0 {
		missing = append(missing, "auction_price")
	}
	if item.PrevClose <= 0 {
		missing = append(missing, "prev_close")
	}
	if item.AuctionAmount <= 0 {
		missing = append(missing, "auction_amount")
	}
	if item.Bid1Price <= 0 {
		missing = append(missing, "bid1_price")
	}
	if item.Bid1Volume <= 0 {
		missing = append(missing, "bid1_volume")
	}
	if includeAvg && item.AvgAuctionAmount5DAvailability == tradingAuctionAvailabilityMissing {
		missing = append(missing, "avg_auction_amount_5d")
	}
	return missing
}

func collectTradingAuctionExplicitItems(scope string, fullCodes []string, itemsByCode map[string]tradingAuctionSnapshotItem, failures []tradingAuctionFailure) ([]tradingAuctionSnapshotItem, []tradingAuctionFailure) {
	items := make([]tradingAuctionSnapshotItem, 0, len(fullCodes))
	seen := map[string]struct{}{}
	for _, raw := range fullCodes {
		fullCode := strings.ToLower(strings.TrimSpace(raw))
		if fullCode == "" {
			continue
		}
		if _, ok := seen[fullCode]; ok {
			continue
		}
		seen[fullCode] = struct{}{}
		item, ok := itemsByCode[fullCode]
		if !ok || item.Availability == tradingAuctionAvailabilityMissing {
			failures = append(failures, tradingAuctionFailure{FullCode: fullCode, Scope: scope, Message: "auction quote unavailable"})
			continue
		}
		items = append(items, item)
	}
	return items, failures
}

func loadTradingAuctionAvgAmount5D(ctx context.Context, fullCode string, tradeDay time.Time) (float64, string) {
	amounts, err := loadTradingInstrumentMetricLocalAuctionAmounts(ctx, fullCode, tradeDay, 5)
	if err != nil || len(amounts) == 0 {
		return 0, tradingAuctionAvailabilityMissing
	}
	sum := 0.0
	for _, item := range amounts {
		sum += item.Amount
	}
	availability := tradingAuctionAvailabilityAvailable
	if len(amounts) < 5 {
		availability = tradingAuctionAvailabilityPartial
	}
	return roundMarketScreen(sum/float64(len(amounts)), 2), availability
}

func loadTradingAuctionOpeningFact(fullCode string, tradeDay time.Time) (tradingAuctionOpeningFact, bool, error) {
	tradeDate := tradeDay.Format("20060102")
	for _, source := range []struct {
		path  string
		table string
	}{
		{filepath.Join(databaseDir, "trade", fullCode+".db"), "TradeHistory"},
		{filepath.Join(databaseDir, "live", fullCode+".db"), "TradeLive"},
	} {
		fact, ok, err := loadTradingAuctionOpeningFactFromDB(source.path, source.table, fullCode, tradeDate, tradeDay)
		if err != nil {
			return tradingAuctionOpeningFact{}, false, err
		}
		if ok {
			return fact, true, nil
		}
	}
	return tradingAuctionOpeningFact{}, false, nil
}

func loadTradingAuctionOpeningFactFromDB(dbPath, table, fullCode, tradeDate string, tradeDay time.Time) (tradingAuctionOpeningFact, bool, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return tradingAuctionOpeningFact{}, false, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return tradingAuctionOpeningFact{}, false, err
	}
	defer db.Close()

	start, end := tradingMetricAuctionWindow(tradeDay)
	query := fmt.Sprintf(`SELECT Price, VolumeHand FROM %s WHERE Code = ? AND TradeDate = ? AND TradeTime >= ? AND TradeTime < ? ORDER BY TradeTime, Seq`, table)
	rows, err := db.Query(query, fullCode, tradeDate, start.Unix(), end.Unix())
	if err != nil {
		return tradingAuctionOpeningFact{}, false, err
	}
	defer rows.Close()

	var amountMilli int64
	var priceMilli int64
	for rows.Next() {
		var price int64
		var volumeHand int64
		if err := rows.Scan(&price, &volumeHand); err != nil {
			return tradingAuctionOpeningFact{}, false, err
		}
		if price <= 0 || volumeHand <= 0 {
			continue
		}
		priceMilli = price
		amountMilli += price * volumeHand * 100
	}
	if err := rows.Err(); err != nil {
		return tradingAuctionOpeningFact{}, false, err
	}
	if priceMilli <= 0 || amountMilli <= 0 {
		return tradingAuctionOpeningFact{}, false, nil
	}
	return tradingAuctionOpeningFact{
		Price:  roundMarketScreen(float64(priceMilli)/1000, 3),
		Amount: roundMarketScreen(float64(amountMilli)/1000, 2),
	}, true, nil
}

func loadTradingAuctionPrevClose(fullCode string, tradeDay time.Time) (float64, bool) {
	dbPath := filepath.Join(databaseDir, "kline", fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return 0, false
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return 0, false
	}
	defer db.Close()

	cutoff := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 0, 0, 0, 0, time.Local).Unix()
	var close collectorpkg.PriceMilli
	if err := db.QueryRow(`SELECT Close FROM DayKline WHERE Code = ? AND Date < ? ORDER BY Date DESC LIMIT 1`, fullCode, cutoff).Scan(&close); err != nil {
		return 0, false
	}
	return roundMarketScreen(close.Float64(), 3), true
}

func tradingAuctionQuoteTime(tradeDay time.Time, snapshotTime string) string {
	at, err := time.ParseInLocation("2006-01-02 15:04:05", tradeDay.Format("2006-01-02")+" "+snapshotTime, time.Local)
	if err != nil {
		return tradeDay.Format("2006-01-02") + "T" + snapshotTime
	}
	return at.Format(time.RFC3339)
}

func resolveTradingAuctionBlockMembers(blockIDs []string, failures []tradingAuctionFailure) ([]tradingAuctionBlockMembers, []tradingAuctionFailure) {
	if len(blockIDs) == 0 {
		return []tradingAuctionBlockMembers{}, failures
	}
	bs := getBlockServiceForProvider()
	if bs == nil {
		for _, id := range blockIDs {
			failures = append(failures, tradingAuctionFailure{BlockID: id, Scope: "block_members", Message: "block service unavailable"})
		}
		return []tradingAuctionBlockMembers{}, failures
	}

	groups := bs.GetBlocks("")
	groupByID := make(map[string]collectorpkg.BlockGroupRecord, len(groups))
	for _, group := range groups {
		groupByID[tradingAuctionBlockID(group)] = group
	}
	out := make([]tradingAuctionBlockMembers, 0, len(blockIDs))
	for _, id := range blockIDs {
		group, ok := groupByID[strings.TrimSpace(id)]
		if !ok {
			failures = append(failures, tradingAuctionFailure{BlockID: id, Scope: "block_members", Message: "block_id not found"})
			continue
		}
		members := bs.GetBlockMembers(group.Source, group.BlockType, group.Name)
		out = append(out, tradingAuctionBlockMembers{
			BlockID:   tradingAuctionBlockID(group),
			Name:      group.Name,
			BlockType: group.BlockType,
			Source:    group.Source,
			Members:   members,
		})
	}
	return out, failures
}

func tradingAuctionBlockID(group collectorpkg.BlockGroupRecord) string {
	payload := group.Source + "\x1f" + group.BlockType + "\x1f" + group.Name
	return "blk_" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func summarizeTradingAuctionItems(items []tradingAuctionSnapshotItem, lowOpenThreshold float64) tradingAuctionGroupStats {
	stats := tradingAuctionGroupStats{
		SampleCount:         len(items),
		LowOpenThresholdPct: lowOpenThreshold,
		WeakSymbols:         []string{},
		Items:               []tradingAuctionCompactItem{},
	}
	sum := 0.0
	for _, item := range items {
		if item.Availability == tradingAuctionAvailabilityMissing {
			continue
		}
		stats.AvailableCount++
		sum += item.AuctionPct
		if item.AuctionPct <= lowOpenThreshold {
			stats.LowOpenCount++
			stats.WeakSymbols = append(stats.WeakSymbols, item.Symbol)
		}
		if item.AvgAuctionAmount5D > 0 && item.AuctionAmount > item.AvgAuctionAmount5D {
			stats.AboveAvgAuctionAmountCount++
		}
		stats.Items = append(stats.Items, tradingAuctionCompactItem{
			Symbol:        item.Symbol,
			Name:          item.Name,
			AuctionPct:    item.AuctionPct,
			AuctionAmount: item.AuctionAmount,
			IsLimitUpOpen: item.IsLimitUpOpen,
		})
	}
	if stats.AvailableCount > 0 {
		avg := roundMarketScreen(sum/float64(stats.AvailableCount), 2)
		stats.AvgAuctionPct = &avg
	}
	return stats
}

func summarizeTradingAuctionWatchedBlocks(planned []tradingAuctionSnapshotItem, blocks []tradingAuctionBlockMembers) tradingAuctionGroupStats {
	if len(planned) == 0 || len(blocks) == 0 {
		return summarizeTradingAuctionItems(nil, 0)
	}
	memberSet := make(map[string]struct{})
	for _, block := range blocks {
		for _, code := range block.Members {
			memberSet[strings.ToLower(code)] = struct{}{}
		}
	}
	filtered := make([]tradingAuctionSnapshotItem, 0, len(planned))
	for _, item := range planned {
		if _, ok := memberSet[strings.ToLower(item.FullCode)]; ok {
			filtered = append(filtered, item)
		}
	}
	return summarizeTradingAuctionItems(filtered, 0)
}

func collectTradingAuctionCandidateBlocks(req tradingAuctionPackageRequest) []tradingAuctionCandidateBlock {
	bs := getBlockServiceForProvider()
	ts := getTickerService()
	if bs == nil || ts == nil || !req.IncludeAuctionBornCandidates {
		return nil
	}
	typeSet := make(map[string]struct{}, len(req.CandidateBlockTypes))
	for _, blockType := range req.CandidateBlockTypes {
		text := strings.TrimSpace(blockType)
		if text != "" {
			typeSet[text] = struct{}{}
		}
	}
	if len(typeSet) == 0 {
		typeSet[string(collectorpkg.BlockTypeConcept)] = struct{}{}
		typeSet[string(collectorpkg.BlockTypeStyle)] = struct{}{}
	}

	groups := bs.GetBlocks("")
	groupByKey := make(map[string]collectorpkg.BlockGroupRecord, len(groups))
	for _, group := range groups {
		groupByKey[marketScreenBlockMemberKey(group.Source, group.BlockType, group.Name)] = group
	}
	seen := map[string]struct{}{}
	out := []tradingAuctionCandidateBlock{}
	for blockType := range typeSet {
		ranks := ts.GetBlockRanking("", blockType, "pct_change", "desc", req.CandidateLimit)
		for _, rank := range ranks {
			key := marketScreenBlockMemberKey(rank.Source, rank.BlockType, rank.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			group, ok := groupByKey[key]
			if !ok {
				continue
			}
			members := bs.GetBlockMembers(group.Source, group.BlockType, group.Name)
			if len(members) == 0 {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, tradingAuctionCandidateBlock{Group: group, Members: members})
		}
	}
	return out
}

func tradingAuctionRowsFromCandidateBlocks(blocks []tradingAuctionCandidateBlock) []marketScreenCodeRow {
	known := tradingAuctionKnownCodeRows()
	rows := make([]marketScreenCodeRow, 0)
	seen := map[string]struct{}{}
	for _, block := range blocks {
		for _, rawCode := range block.Members {
			fullCode := strings.ToLower(strings.TrimSpace(rawCode))
			if fullCode == "" {
				continue
			}
			if _, ok := seen[fullCode]; ok {
				continue
			}
			seen[fullCode] = struct{}{}
			if row, ok := known[fullCode]; ok {
				rows = append(rows, row)
				continue
			}
			model, err := parseTradingInstrumentMetricModel(fullCode)
			if err != nil {
				continue
			}
			rows = append(rows, marketScreenCodeRow{
				fullCode:  fullCode,
				name:      model.Name,
				exchange:  strings.ToLower(model.Exchange),
				assetType: modelAssetType(model),
			})
		}
	}
	return rows
}

func buildTradingAuctionCandidateGroups(blocks []tradingAuctionCandidateBlock, limit int, itemsByCode map[string]tradingAuctionSnapshotItem) []tradingAuctionCandidateGroup {
	candidates := []tradingAuctionCandidateGroup{}
	for _, block := range blocks {
		frontline := []tradingAuctionSnapshotItem{}
		sum := 0.0
		oneLine := 0
		for _, code := range block.Members {
			item, ok := itemsByCode[strings.ToLower(code)]
			if !ok || item.Availability == tradingAuctionAvailabilityMissing {
				continue
			}
			if item.IsLimitUpOpen || item.AuctionPct >= 2 {
				frontline = append(frontline, item)
				sum += item.AuctionPct
				if item.IsLimitUpOpen {
					oneLine++
				}
			}
		}
		if len(frontline) == 0 || (len(frontline) < 2 && oneLine == 0) {
			continue
		}
		sort.Slice(frontline, func(i, j int) bool {
			if frontline[i].IsLimitUpOpen != frontline[j].IsLimitUpOpen {
				return frontline[i].IsLimitUpOpen
			}
			if frontline[i].AuctionPct != frontline[j].AuctionPct {
				return frontline[i].AuctionPct > frontline[j].AuctionPct
			}
			return frontline[i].AuctionAmount > frontline[j].AuctionAmount
		})
		symbols := make([]string, 0, tradingAuctionMinInt(len(frontline), 3))
		for i := 0; i < len(frontline) && i < 3; i++ {
			symbols = append(symbols, frontline[i].Symbol)
		}
		candidates = append(candidates, tradingAuctionCandidateGroup{
			BlockID:           tradingAuctionBlockID(block.Group),
			Theme:             block.Group.Name,
			BlockType:         block.Group.BlockType,
			SymbolCount:       len(frontline),
			AvgAuctionPct:     roundMarketScreen(sum/float64(len(frontline)), 2),
			OneLineCount:      oneLine,
			FrontlineSymbols:  symbols,
			GroupBehavior:     tradingAuctionGroupBehavior(len(frontline)),
			FrontlineBehavior: tradingAuctionFrontlineBehavior(oneLine),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].OneLineCount != candidates[j].OneLineCount {
			return candidates[i].OneLineCount > candidates[j].OneLineCount
		}
		if candidates[i].AvgAuctionPct != candidates[j].AvgAuctionPct {
			return candidates[i].AvgAuctionPct > candidates[j].AvgAuctionPct
		}
		return candidates[i].SymbolCount > candidates[j].SymbolCount
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func tradingAuctionGroupBehavior(count int) string {
	if count >= 2 {
		return "多个标的竞价高开"
	}
	return "单个标的竞价高开"
}

func tradingAuctionFrontlineBehavior(oneLineCount int) string {
	if oneLineCount > 0 {
		return "前排出现一字或大幅高开"
	}
	return "前排竞价大幅高开"
}

func tradingAuctionMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
