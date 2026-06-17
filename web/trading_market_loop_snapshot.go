package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	collectorpkg "github.com/injoyai/tdx/collector"
)

const (
	tradingMarketLoopMaxExplicitCodes           = 200
	tradingMarketLoopMaxWatchedBlocks           = 50
	tradingMarketLoopMaxRankingLimit            = 50
	tradingMarketLoopMaxScreenLimit             = 100
	tradingMarketLoopCandidateMembers           = 5
	tradingMarketLoopDefaultMaxStalenessSeconds = 180
)

type tradingMarketLoopSnapshotRequest struct {
	Market              string                           `json:"market"`
	TradeDate           string                           `json:"trade_date"`
	SnapshotTime        string                           `json:"snapshot_time"`
	PlannedFullCodes    []string                         `json:"planned_full_codes"`
	HoldingFullCodes    []string                         `json:"holding_full_codes"`
	WatchFullCodes      []string                         `json:"watch_full_codes"`
	WatchedBlocks       []tradingMarketLoopBlockSelector `json:"watched_blocks"`
	CandidateBlockTypes []string                         `json:"candidate_block_types"`
	RankingLimit        int                              `json:"ranking_limit"`
	ScreenLimit         int                              `json:"screen_limit"`
	MaxStalenessSeconds int                              `json:"max_staleness_seconds"`
}

type tradingMarketLoopBlockSelector struct {
	Source    string `json:"source"`
	BlockType string `json:"block_type"`
	Name      string `json:"name"`
}

type tradingMarketLoopSnapshotResponse struct {
	TradeDate          string                         `json:"trade_date"`
	SnapshotTime       string                         `json:"snapshot_time"`
	SourcePolicy       tradingMarketLoopSourcePolicy  `json:"source_policy"`
	MarketBreadth      map[string]interface{}         `json:"market_breadth"`
	LimitStats         map[string]interface{}         `json:"limit_stats"`
	Screens            tradingMarketLoopScreens       `json:"screens"`
	PlannedInstruments []map[string]interface{}       `json:"planned_instruments"`
	HoldingInstruments []map[string]interface{}       `json:"holding_instruments"`
	WatchInstruments   []map[string]interface{}       `json:"watch_instruments"`
	WatchedBlocks      []tradingMarketLoopBlockStocks `json:"watched_blocks"`
	CandidateBlocks    tradingMarketLoopCandidates    `json:"candidate_blocks"`
	Failures           []tradingMarketLoopFailure     `json:"failures"`
}

type tradingMarketLoopSourcePolicy struct {
	DataSource          string `json:"data_source"`
	ProviderStatus      string `json:"provider_status"`
	FallbackAllowed     bool   `json:"fallback_allowed"`
	TickerUpdatedAt     string `json:"ticker_updated_at"`
	AgeSeconds          int    `json:"age_seconds"`
	MaxStalenessSeconds int    `json:"max_staleness_seconds"`
}

type tradingMarketLoopScreens struct {
	TopAmount  tradingMarketLoopScreen `json:"top_amount"`
	TopGainers tradingMarketLoopScreen `json:"top_gainers"`
	TopLosers  tradingMarketLoopScreen `json:"top_losers"`
	LimitUp    tradingMarketLoopScreen `json:"limit_up"`
	LimitDown  tradingMarketLoopScreen `json:"limit_down"`
	ActiveRisk tradingMarketLoopScreen `json:"active_risk"`
}

type tradingMarketLoopScreen struct {
	Count int                      `json:"count"`
	Total int                      `json:"total"`
	List  []map[string]interface{} `json:"list"`
}

type tradingMarketLoopBlockStocks struct {
	Source         string                  `json:"source"`
	BlockType      string                  `json:"block_type"`
	Name           string                  `json:"name"`
	BlockPctChange float64                 `json:"block_pct_change"`
	Amount         float64                 `json:"amount"`
	MemberCount    int                     `json:"member_count"`
	AvailableCount int                     `json:"available_count"`
	RiseCount      int                     `json:"rise_count"`
	FallCount      int                     `json:"fall_count"`
	FlatCount      int                     `json:"flat_count"`
	LimitUpCount   int                     `json:"limit_up_count"`
	LimitDownCount int                     `json:"limit_down_count"`
	Count          int                     `json:"count"`
	TopAmount      tradingMarketLoopScreen `json:"top_amount"`
	TopGainers     tradingMarketLoopScreen `json:"top_gainers"`
	LimitUp        tradingMarketLoopScreen `json:"limit_up"`
	ActiveRisk     tradingMarketLoopScreen `json:"active_risk"`
}

type tradingMarketLoopCandidates struct {
	ByChangePct    []map[string]interface{} `json:"by_change_pct"`
	ByAmount       []map[string]interface{} `json:"by_amount"`
	ByLimitUpCount []map[string]interface{} `json:"by_limit_up_count"`
}

type tradingMarketLoopFailure struct {
	FullCode string `json:"full_code,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Message  string `json:"message"`
}

type tradingMarketLoopError struct {
	ErrorCode  string         `json:"error_code"`
	ErrorType  string         `json:"error_type"`
	HTTPStatus int            `json:"http_status"`
	Retryable  bool           `json:"retryable"`
	Details    map[string]any `json:"details,omitempty"`
	Message    string         `json:"-"`
}

func (e *tradingMarketLoopError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type tradingMarketLoopEnvelope struct {
	Code      int                     `json:"code"`
	Message   string                  `json:"message"`
	RequestID string                  `json:"request_id"`
	Data      any                     `json:"data,omitempty"`
	Error     *tradingMarketLoopError `json:"error,omitempty"`
}

func handleTradingMarketLoopSnapshot(w http.ResponseWriter, r *http.Request) {
	requestID := newTradingMarketLoopRequestID()
	if r.Method != http.MethodPost {
		writeTradingMarketLoopError(w, requestID, marketLoopErr(http.StatusMethodNotAllowed, "INVALID_SCOPE", "method not allowed", false, map[string]any{
			"field":  "method",
			"reason": "only POST is supported",
		}))
		return
	}

	var req tradingMarketLoopSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeTradingMarketLoopError(w, requestID, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "请求参数错误: "+err.Error(), false, map[string]any{
			"field": "body",
		}))
		return
	}
	resp, err := buildTradingMarketLoopSnapshot(req, getTickerService())
	if err != nil {
		writeTradingMarketLoopError(w, requestID, err)
		return
	}
	writeTradingMarketLoopJSON(w, http.StatusOK, tradingMarketLoopEnvelope{
		Code:      0,
		Message:   "success",
		RequestID: requestID,
		Data:      resp,
	})
}

func buildTradingMarketLoopSnapshot(req tradingMarketLoopSnapshotRequest, ts *collectorpkg.TickerService) (tradingMarketLoopSnapshotResponse, *tradingMarketLoopError) {
	normalized, tradeDay, err := normalizeTradingMarketLoopRequest(req)
	if err != nil {
		return tradingMarketLoopSnapshotResponse{}, err
	}
	if ts == nil {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusServiceUnavailable, "LIVE_TICKER_NOT_READY", "live ticker service is not ready", true, nil)
	}
	updatedAt := ts.UpdatedAt()
	if updatedAt.IsZero() {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusServiceUnavailable, "LIVE_TICKER_NOT_READY", "live ticker snapshot is not ready", true, nil)
	}
	now := marketScreenNow()
	if !marketScreenInTickerSession(now) {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusServiceUnavailable, "LIVE_TICKER_OUT_OF_SESSION", "live ticker is outside the intraday session", false, map[string]any{
			"now": now.In(time.Local).Format(time.RFC3339),
		})
	}
	if updatedAt.In(time.Local).Format("20060102") != tradeDay.Format("20060102") {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusConflict, "LIVE_TICKER_DATE_MISMATCH", "live ticker date does not match requested trade_date", false, map[string]any{
			"ticker_updated_at": updatedAt.Format(time.RFC3339),
			"trade_date":        tradeDay.Format("2006-01-02"),
		})
	}
	ageSeconds := 0
	if now.After(updatedAt) {
		ageSeconds = int(now.Sub(updatedAt).Seconds())
	}
	if ageSeconds > normalized.MaxStalenessSeconds {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusServiceUnavailable, "LIVE_TICKER_STALE", "live ticker snapshot is stale", true, map[string]any{
			"ticker_updated_at":     updatedAt.Format(time.RFC3339),
			"max_staleness_seconds": normalized.MaxStalenessSeconds,
			"age_seconds":           ageSeconds,
		})
	}

	ticks := ts.GetAllStocks()
	if len(ticks) == 0 {
		return tradingMarketLoopSnapshotResponse{}, marketLoopErr(http.StatusServiceUnavailable, "LIVE_TICKER_EMPTY", "live ticker snapshot is empty", true, nil)
	}
	tickByCode := tradingMarketLoopTickLookup(ticks)
	failures := []tradingMarketLoopFailure{}

	planned, failures := tradingMarketLoopExplicitInstruments(normalized.PlannedFullCodes, "planned_full_codes", tickByCode, ts, failures)
	holdings, failures := tradingMarketLoopExplicitInstruments(normalized.HoldingFullCodes, "holding_full_codes", tickByCode, ts, failures)
	watch, failures := tradingMarketLoopExplicitInstruments(normalized.WatchFullCodes, "watch_full_codes", tickByCode, ts, failures)

	return tradingMarketLoopSnapshotResponse{
		TradeDate:    tradeDay.Format("2006-01-02"),
		SnapshotTime: normalized.SnapshotTime,
		SourcePolicy: tradingMarketLoopSourcePolicy{
			DataSource:          "ticker",
			ProviderStatus:      "live_tick",
			FallbackAllowed:     false,
			TickerUpdatedAt:     updatedAt.Format(time.RFC3339),
			AgeSeconds:          ageSeconds,
			MaxStalenessSeconds: normalized.MaxStalenessSeconds,
		},
		MarketBreadth:      buildMarketStatsData(ticks, string(collectorpkg.AssetTypeStock)),
		LimitStats:         buildMarketLimitStatsBreakdownData(collectorpkg.ComputeLimitStatsBreakdown(ticks)),
		Screens:            tradingMarketLoopBuildScreens(ticks, normalized.ScreenLimit, ts),
		PlannedInstruments: planned,
		HoldingInstruments: holdings,
		WatchInstruments:   watch,
		WatchedBlocks:      tradingMarketLoopWatchedBlocks(ts, normalized.WatchedBlocks, normalized.ScreenLimit),
		CandidateBlocks:    tradingMarketLoopCandidateBlocks(ts, normalized.CandidateBlockTypes, normalized.RankingLimit),
		Failures:           failures,
	}, nil
}

func normalizeTradingMarketLoopRequest(req tradingMarketLoopSnapshotRequest) (tradingMarketLoopSnapshotRequest, time.Time, *tradingMarketLoopError) {
	req.Market = strings.ToUpper(strings.TrimSpace(req.Market))
	if req.Market == "" {
		req.Market = "CN"
	}
	if req.Market != "CN" {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "market only supports CN", false, map[string]any{"field": "market"})
	}
	tradeDay, parseErr := parseWorkdayDate(strings.TrimSpace(req.TradeDate))
	if parseErr != nil {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "trade_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD", false, map[string]any{"field": "trade_date"})
	}
	req.SnapshotTime = strings.TrimSpace(req.SnapshotTime)
	if _, parseErr := time.ParseInLocation("15:04:05", req.SnapshotTime, time.Local); parseErr != nil {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "snapshot_time 参数格式错误，应为 HH:MM:SS", false, map[string]any{"field": "snapshot_time"})
	}
	if req.MaxStalenessSeconds <= 0 {
		req.MaxStalenessSeconds = tradingMarketLoopDefaultMaxStalenessSeconds
	}
	if req.ScreenLimit <= 0 {
		req.ScreenLimit = 20
	}
	if req.RankingLimit <= 0 {
		req.RankingLimit = 20
	}
	if req.ScreenLimit > tradingMarketLoopMaxScreenLimit {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "screen_limit exceeds maximum", false, map[string]any{"max": tradingMarketLoopMaxScreenLimit})
	}
	if req.RankingLimit > tradingMarketLoopMaxRankingLimit {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "ranking_limit exceeds maximum", false, map[string]any{"max": tradingMarketLoopMaxRankingLimit})
	}
	if len(req.PlannedFullCodes)+len(req.HoldingFullCodes)+len(req.WatchFullCodes) > tradingMarketLoopMaxExplicitCodes {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "explicit full code count exceeds maximum", false, map[string]any{"max": tradingMarketLoopMaxExplicitCodes})
	}
	if len(req.WatchedBlocks) > tradingMarketLoopMaxWatchedBlocks {
		return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "watched_blocks exceeds maximum", false, map[string]any{"max": tradingMarketLoopMaxWatchedBlocks})
	}
	req.PlannedFullCodes = normalizeTradingMarketLoopFullCodes(req.PlannedFullCodes)
	req.HoldingFullCodes = normalizeTradingMarketLoopFullCodes(req.HoldingFullCodes)
	req.WatchFullCodes = normalizeTradingMarketLoopFullCodes(req.WatchFullCodes)
	for idx, block := range req.WatchedBlocks {
		block.Source = strings.ToLower(strings.TrimSpace(block.Source))
		block.BlockType = strings.ToLower(strings.TrimSpace(block.BlockType))
		block.Name = strings.TrimSpace(block.Name)
		if block.Source == "" || block.BlockType == "" || block.Name == "" {
			return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "watched_blocks contains incomplete block selector", false, map[string]any{"field": "watched_blocks"})
		}
		req.WatchedBlocks[idx] = block
	}
	for idx, value := range req.CandidateBlockTypes {
		blockType := strings.ToLower(strings.TrimSpace(value))
		switch blockType {
		case "concept", "style", "index_block":
			req.CandidateBlockTypes[idx] = blockType
		case "":
			return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "candidate_block_types must not contain empty values", false, map[string]any{"field": "candidate_block_types"})
		default:
			return req, time.Time{}, marketLoopErr(http.StatusBadRequest, "INVALID_SCOPE", "unsupported candidate_block_type: "+value, false, map[string]any{"field": "candidate_block_types"})
		}
	}
	return req, tradeDay, nil
}

func normalizeTradingMarketLoopFullCodes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		fullCode := strings.ToLower(strings.TrimSpace(value))
		if fullCode == "" {
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

func tradingMarketLoopTickLookup(ticks []collectorpkg.StockTick) map[string]collectorpkg.StockTick {
	out := make(map[string]collectorpkg.StockTick, len(ticks)*2)
	for _, tick := range ticks {
		out[strings.ToLower(tick.Code)] = tick
		out[strings.ToLower(bareCode(tick.Code))] = tick
	}
	return out
}

func tradingMarketLoopExplicitInstruments(fullCodes []string, scope string, tickByCode map[string]collectorpkg.StockTick, ts *collectorpkg.TickerService, failures []tradingMarketLoopFailure) ([]map[string]interface{}, []tradingMarketLoopFailure) {
	out := make([]map[string]interface{}, 0, len(fullCodes))
	for _, fullCode := range fullCodes {
		tick, ok := tickByCode[strings.ToLower(fullCode)]
		if !ok {
			failures = append(failures, tradingMarketLoopFailure{FullCode: fullCode, Scope: scope, Message: "full_code not found in live ticker snapshot"})
			continue
		}
		out = append(out, tradingMarketLoopInstrumentMap(tick, ts))
	}
	return out, failures
}

func tradingMarketLoopBuildScreens(ticks []collectorpkg.StockTick, limit int, ts *collectorpkg.TickerService) tradingMarketLoopScreens {
	return tradingMarketLoopScreens{
		TopAmount:  tradingMarketLoopScreenFromTicks(ticks, "", "amount", "desc", limit, ts),
		TopGainers: tradingMarketLoopScreenFromTicks(ticks, "", "pct_change", "desc", limit, ts),
		TopLosers:  tradingMarketLoopScreenFromTicks(ticks, "", "pct_change", "asc", limit, ts),
		LimitUp:    tradingMarketLoopScreenFromTicks(ticks, "limit_up", "amount", "desc", limit, ts),
		LimitDown:  tradingMarketLoopScreenFromTicks(ticks, "limit_down", "amount", "desc", limit, ts),
		ActiveRisk: tradingMarketLoopScreenFromTicks(ticks, "active_risk", "amount", "desc", limit, ts),
	}
}

func tradingMarketLoopScreenFromTicks(ticks []collectorpkg.StockTick, filter, sortBy, order string, limit int, ts *collectorpkg.TickerService) tradingMarketLoopScreen {
	filtered := make([]collectorpkg.StockTick, 0, len(ticks))
	for _, tick := range ticks {
		if tick.AssetType != string(collectorpkg.AssetTypeStock) {
			continue
		}
		switch filter {
		case "limit_up":
			if !tick.IsLimitUp {
				continue
			}
		case "limit_down":
			if !tick.IsLimitDown {
				continue
			}
		case "active_risk":
			if !tradingMarketLoopIsActiveRisk(tick) {
				continue
			}
		}
		filtered = append(filtered, tick)
	}
	sortMarketScreenTicks(filtered, sortBy, order)
	total := len(filtered)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	items := make([]map[string]interface{}, 0, len(filtered))
	for _, tick := range filtered {
		items = append(items, tradingMarketLoopInstrumentMap(tick, ts))
	}
	return tradingMarketLoopScreen{
		Count: len(items),
		Total: total,
		List:  items,
	}
}

func tradingMarketLoopWatchedBlocks(ts *collectorpkg.TickerService, blocks []tradingMarketLoopBlockSelector, limit int) []tradingMarketLoopBlockStocks {
	if len(blocks) == 0 {
		return []tradingMarketLoopBlockStocks{}
	}
	out := make([]tradingMarketLoopBlockStocks, 0, len(blocks))
	for _, block := range blocks {
		blockPct, ticks := ts.GetBlockStocks(block.Source, block.BlockType, block.Name, "amount", "desc", 0)
		rank := tradingMarketLoopFindBlockRank(ts, block.Source, block.BlockType, block.Name)
		out = append(out, tradingMarketLoopBlockStocks{
			Source:         block.Source,
			BlockType:      block.BlockType,
			Name:           block.Name,
			BlockPctChange: blockPct,
			Amount:         rank.Amount,
			MemberCount:    rank.MemberCount,
			AvailableCount: rank.AvailableCount,
			RiseCount:      rank.RiseCount,
			FallCount:      rank.FallCount,
			FlatCount:      rank.FlatCount,
			LimitUpCount:   rank.LimitUpCount,
			LimitDownCount: rank.LimitDownCount,
			Count:          len(ticks),
			TopAmount:      tradingMarketLoopScreenFromTicks(ticks, "", "amount", "desc", limit, ts),
			TopGainers:     tradingMarketLoopScreenFromTicks(ticks, "", "pct_change", "desc", limit, ts),
			LimitUp:        tradingMarketLoopScreenFromTicks(ticks, "limit_up", "amount", "desc", limit, ts),
			ActiveRisk:     tradingMarketLoopScreenFromTicks(ticks, "active_risk", "amount", "desc", limit, ts),
		})
	}
	return out
}

func tradingMarketLoopCandidateBlocks(ts *collectorpkg.TickerService, blockTypes []string, limit int) tradingMarketLoopCandidates {
	if len(blockTypes) == 0 {
		return tradingMarketLoopCandidates{
			ByChangePct:    []map[string]interface{}{},
			ByAmount:       []map[string]interface{}{},
			ByLimitUpCount: []map[string]interface{}{},
		}
	}
	return tradingMarketLoopCandidates{
		ByChangePct:    tradingMarketLoopBlockRanking(ts, blockTypes, "pct_change", "desc", limit),
		ByAmount:       tradingMarketLoopBlockRanking(ts, blockTypes, "amount", "desc", limit),
		ByLimitUpCount: tradingMarketLoopBlockRanking(ts, blockTypes, "limit_up", "desc", limit),
	}
}

func tradingMarketLoopBlockRanking(ts *collectorpkg.TickerService, blockTypes []string, sortBy, order string, limit int) []map[string]interface{} {
	ranks := make([]collectorpkg.BlockRank, 0, len(blockTypes)*limit)
	for _, blockType := range blockTypes {
		ranks = append(ranks, ts.GetBlockRanking(tradingMarketLoopBlockSource(blockType), blockType, sortBy, order, limit)...)
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		var left, right float64
		switch sortBy {
		case "amount":
			left, right = ranks[i].Amount, ranks[j].Amount
		case "limit_up":
			left, right = float64(ranks[i].LimitUpCount), float64(ranks[j].LimitUpCount)
		default:
			left, right = ranks[i].PctChange, ranks[j].PctChange
		}
		if strings.EqualFold(order, "asc") {
			return left < right
		}
		return left > right
	})
	if limit > 0 && len(ranks) > limit {
		ranks = ranks[:limit]
	}
	items := make([]map[string]interface{}, 0, len(ranks))
	for _, rank := range ranks {
		items = append(items, tradingMarketLoopBlockRankMap(ts, rank))
	}
	return items
}

func tradingMarketLoopFindBlockRank(ts *collectorpkg.TickerService, source, blockType, name string) collectorpkg.BlockRank {
	ranks := ts.GetBlockRanking(source, blockType, "amount", "desc", 0)
	for _, rank := range ranks {
		if rank.Name == name && rank.Source == source && rank.BlockType == blockType {
			return rank
		}
	}
	return collectorpkg.BlockRank{}
}

func tradingMarketLoopBlockRankMap(ts *collectorpkg.TickerService, rank collectorpkg.BlockRank) map[string]interface{} {
	item := blockRankToProviderMap(rank)
	_, ticks := ts.GetBlockStocks(rank.Source, rank.BlockType, rank.Name, "amount", "desc", 0)
	item["top_amount"] = tradingMarketLoopScreenFromTicks(ticks, "", "amount", "desc", tradingMarketLoopCandidateMembers, ts)
	item["top_gainers"] = tradingMarketLoopScreenFromTicks(ticks, "", "pct_change", "desc", tradingMarketLoopCandidateMembers, ts)
	item["limit_up"] = tradingMarketLoopScreenFromTicks(ticks, "limit_up", "amount", "desc", tradingMarketLoopCandidateMembers, ts)
	item["active_risk"] = tradingMarketLoopScreenFromTicks(ticks, "active_risk", "amount", "desc", tradingMarketLoopCandidateMembers, ts)
	return item
}

func tradingMarketLoopInstrumentMap(tick collectorpkg.StockTick, ts *collectorpkg.TickerService) map[string]interface{} {
	item := stockTickToProviderMap(tick)
	item["price_change"] = tick.PriceChange
	item["amplitude"] = tick.Amplitude
	item["high_change_pct"] = tradingMarketLoopChangePct(tick.High, tick.PreClose)
	item["low_change_pct"] = tradingMarketLoopChangePct(tick.Low, tick.PreClose)
	item["pullback_from_high_pp"] = tradingMarketLoopPullbackFromHighPP(tick)
	if avg := tradingMarketLoopAvgTradePrice(tick); avg > 0 {
		item["avg_trade_price"] = avg
	}
	flags := tradingMarketLoopRiskFlags(tick)
	if p := ts.GetLimitUpPublic(tick.Code); p != nil {
		mergeLimitPublic(item, p, true)
		if p.BreakCount > 0 {
			flags = append(flags, "LIMIT_UP_OPENED")
		}
	}
	if p := ts.GetLimitDownPublic(tick.Code); p != nil {
		mergeLimitPublic(item, p, false)
	}
	item["risk_flags"] = flags
	return item
}

func tradingMarketLoopAvgTradePrice(tick collectorpkg.StockTick) float64 {
	if tick.Volume <= 0 || tick.Amount <= 0 {
		return 0
	}
	return roundMarketScreen(tick.Amount/float64(tick.Volume*100), 3)
}

func tradingMarketLoopChangePct(price, preClose float64) float64 {
	if preClose <= 0 || price <= 0 {
		return 0
	}
	return roundMarketScreen((price-preClose)/preClose*100, 2)
}

func tradingMarketLoopPullbackFromHighPP(tick collectorpkg.StockTick) float64 {
	return roundMarketScreen(tick.PctChange-tradingMarketLoopChangePct(tick.High, tick.PreClose), 2)
}

func tradingMarketLoopIsActiveRisk(tick collectorpkg.StockTick) bool {
	return tick.PctChange <= -5 || tradingMarketLoopPullbackFromHighPP(tick) <= -5 || tick.IsLimitDown
}

func tradingMarketLoopRiskFlags(tick collectorpkg.StockTick) []string {
	flags := []string{}
	if tick.PctChange <= -5 {
		flags = append(flags, "BIG_DROP")
	}
	if tradingMarketLoopPullbackFromHighPP(tick) <= -5 {
		flags = append(flags, "HIGH_PULLBACK")
	}
	if tick.IsLimitDown {
		flags = append(flags, "LIMIT_DOWN")
	}
	return flags
}

func tradingMarketLoopBlockSource(blockType string) string {
	switch blockType {
	case "concept":
		return "block_gn.dat"
	case "style":
		return "block_fg.dat"
	case "index_block":
		return "block_zs.dat"
	default:
		return ""
	}
}

func marketLoopErr(status int, code, message string, retryable bool, details map[string]any) *tradingMarketLoopError {
	errorType := "live_data_error"
	if status >= 400 && status < 500 {
		errorType = "client_error"
	}
	if code == "PROVIDER_ERROR" {
		errorType = "provider_error"
	}
	return &tradingMarketLoopError{
		ErrorCode:  code,
		ErrorType:  errorType,
		HTTPStatus: status,
		Retryable:  retryable,
		Details:    details,
		Message:    message,
	}
}

func writeTradingMarketLoopError(w http.ResponseWriter, requestID string, err *tradingMarketLoopError) {
	if err == nil {
		err = marketLoopErr(http.StatusInternalServerError, "PROVIDER_ERROR", "internal error", false, nil)
	}
	message := err.Message
	if strings.TrimSpace(message) == "" {
		message = err.ErrorCode
	}
	writeTradingMarketLoopJSON(w, err.HTTPStatus, tradingMarketLoopEnvelope{
		Code:      tradingMarketLoopNumericCode(err.ErrorCode),
		Message:   message,
		RequestID: requestID,
		Error:     err,
	})
}

func writeTradingMarketLoopJSON(w http.ResponseWriter, status int, payload tradingMarketLoopEnvelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func newTradingMarketLoopRequestID() string {
	return "req_tml_" + time.Now().UTC().Format("20060102_150405") + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func tradingMarketLoopNumericCode(errorCode string) int {
	switch errorCode {
	case "INVALID_SCOPE":
		return 4002001
	case "LIVE_TICKER_DATE_MISMATCH":
		return 4092001
	case "LIVE_TICKER_NOT_READY":
		return 5032001
	case "LIVE_TICKER_OUT_OF_SESSION":
		return 5032002
	case "LIVE_TICKER_STALE":
		return 5032003
	case "LIVE_TICKER_EMPTY":
		return 5032004
	default:
		return 5002000
	}
}
