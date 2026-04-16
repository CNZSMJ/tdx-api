package collector

import (
	"context"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	tickerDefaultInterval = 3 * time.Second
	quoteFetchBatchSize   = 80
	// A-share continuous trading: 09:15 ~ 15:05 (with some buffer)
	marketOpenHour  = 9
	marketOpenMin   = 15
	marketCloseHour = 15
	marketCloseMin  = 5
)

// TickerConfig configures the real-time ticker service.
type TickerConfig struct {
	Interval time.Duration
	Now      func() time.Time
}

// StockTick is the per-stock real-time snapshot exposed to API consumers.
type StockTick struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Exchange    string  `json:"exchange"`
	AssetType   string  `json:"asset_type"`
	Last        float64 `json:"last"`
	PreClose    float64 `json:"pre_close"`
	Open        float64 `json:"open"`
	High        float64 `json:"high"`
	Low         float64 `json:"low"`
	PctChange   float64 `json:"pct_change"`
	PriceChange float64 `json:"price_change"`
	Volume      int64   `json:"volume"`
	Amount      float64 `json:"amount"`
	Amplitude   float64 `json:"amplitude"`
	IsLimitUp   bool    `json:"is_limit_up"`
	IsLimitDown bool    `json:"is_limit_down"`
}

// AssetOverview aggregates breadth-style metrics for one asset bucket.
type AssetOverview struct {
	Total       int     `json:"total"`
	Up          int     `json:"up"`
	Down        int     `json:"down"`
	Flat        int     `json:"flat"`
	UpRatio     float64 `json:"up_ratio"`
	LimitUp     int     `json:"limit_up"`
	LimitDown   int     `json:"limit_down"`
	TotalAmount float64 `json:"total_amount"`
	TotalVolume int64   `json:"total_volume"`
}

// MarketOverview is precomputed once per tick from the full stocks map.
type MarketOverview struct {
	Stock      AssetOverview            `json:"stock"`
	ETF        AssetOverview            `json:"etf"`
	ByExchange map[string]AssetOverview `json:"by_exchange"`
}

// BlockRank is the aggregated ranking entry for a single block.
type BlockRank struct {
	Name           string  `json:"name"`
	Source         string  `json:"source"`
	BlockType      string  `json:"block_type"`
	PctChange      float64 `json:"pct_change"`
	Amount         float64 `json:"amount"`
	LeadingCode    string  `json:"leading_stock_code"`
	LeadingName    string  `json:"leading_stock_name"`
	LeadingPct     float64 `json:"leading_stock_pct"`
	RiseCount      int     `json:"rise_count"`
	FallCount      int     `json:"fall_count"`
	FlatCount      int     `json:"flat_count"`
	LimitUpCount   int     `json:"limit_up_count"`
	LimitDownCount int     `json:"limit_down_count"`
	MemberCount    int     `json:"member_count"`
	AvailableCount int     `json:"available_count"`
}

type tickerCache struct {
	stocks    map[string]*StockTick
	blocks    []BlockRank
	overview  MarketOverview
	updatedAt time.Time
}

// limitSideState tracks intraday limit-up / limit-down for stocks only.
type limitSideState struct {
	Active      bool
	FirstSeen   time.Time
	LastSeen    time.Time
	BreakCount  int
	EdgeVolume  int64 // buy1 at limit-up, sell1 at limit-down
	Approximate bool  // true when FirstSeen is a discovery time, not the actual transition
}

// LimitSidePublic is an API-safe view of limitSideState.
type LimitSidePublic struct {
	FirstSeen       string `json:"limit_first_seen,omitempty"`
	FirstSeenApprox bool   `json:"limit_first_seen_approx,omitempty"`
	LastSeen        string `json:"limit_last_seen,omitempty"`
	BreakCount      int    `json:"limit_break_count"`
	Bid1Volume      int64  `json:"bid1_volume,omitempty"`
	Ask1Volume      int64  `json:"ask1_volume,omitempty"`
}

// TickerService provides near-real-time market snapshots by polling TDX
// every N seconds during trading hours.
type TickerService struct {
	provider  QuoteProvider
	block     *BlockService
	cfg       TickerConfig
	mu        sync.RWMutex
	cache     tickerCache
	cancel    context.CancelFunc
	running   bool
	limitUp   map[string]*limitSideState
	limitDown map[string]*limitSideState
	limitDate string
}

func NewTickerService(provider QuoteProvider, block *BlockService, cfg TickerConfig) *TickerService {
	if cfg.Interval <= 0 {
		cfg.Interval = tickerDefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &TickerService{
		provider:  provider,
		block:     block,
		cfg:       cfg,
		limitUp:   make(map[string]*limitSideState),
		limitDown: make(map[string]*limitSideState),
	}
}

// Start begins the background polling loop. Safe to call multiple times.
func (t *TickerService) Start(allCodes []string, nameResolver func(string) string) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running = true
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.mu.Unlock()

	go t.loop(ctx, allCodes, nameResolver)
}

// Stop terminates the background polling loop.
func (t *TickerService) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	t.running = false
}

func (t *TickerService) Running() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

// GetOverview returns the last precomputed market overview.
func (t *TickerService) GetOverview() MarketOverview {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cache.overview
}

// GetAllStocks returns a copy slice of all cached ticks.
func (t *TickerService) GetAllStocks() []StockTick {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]StockTick, 0, len(t.cache.stocks))
	for _, v := range t.cache.stocks {
		if v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// GetLimitUpPublic returns limit-up tracking for a stock code.
func (t *TickerService) GetLimitUpPublic(code string) *LimitSidePublic {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return limitStateToPublic(t.limitUp[code], true)
}

// GetLimitDownPublic returns limit-down tracking for a stock code.
func (t *TickerService) GetLimitDownPublic(code string) *LimitSidePublic {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return limitStateToPublic(t.limitDown[code], false)
}

func limitStateToPublic(s *limitSideState, isUp bool) *LimitSidePublic {
	if s == nil || s.FirstSeen.IsZero() {
		return nil
	}
	p := &LimitSidePublic{
		BreakCount:      s.BreakCount,
		FirstSeen:       s.FirstSeen.Format(time.RFC3339),
		FirstSeenApprox: s.Approximate,
	}
	if !s.LastSeen.IsZero() {
		p.LastSeen = s.LastSeen.Format(time.RFC3339)
	}
	if isUp {
		p.Bid1Volume = s.EdgeVolume
	} else {
		p.Ask1Volume = s.EdgeVolume
	}
	return p
}

// GetBlockRanking returns a sorted copy of block rankings.
func (t *TickerService) GetBlockRanking(source, blockType string, sortBy string, order string, limit int) []BlockRank {
	t.mu.RLock()
	src := t.cache.blocks
	t.mu.RUnlock()

	var filtered []BlockRank
	for _, b := range src {
		if source != "" && b.Source != source {
			continue
		}
		if blockType != "" && b.BlockType != blockType {
			continue
		}
		filtered = append(filtered, b)
	}

	sortBlockRanks(filtered, sortBy, order)

	if limit > 0 && limit < len(filtered) {
		filtered = filtered[:limit]
	}
	return filtered
}

// GetBlockStocks returns sorted stock ticks for a given block.
func (t *TickerService) GetBlockStocks(source, blockType, blockName string, sortBy string, order string, limit int) (blockPct float64, ticks []StockTick) {
	codes := t.block.GetBlockMembers(source, blockType, blockName)
	if len(codes) == 0 {
		return 0, nil
	}

	t.mu.RLock()
	cache := t.cache
	t.mu.RUnlock()

	ticks = make([]StockTick, 0, len(codes))
	for _, code := range codes {
		if tick, ok := cache.stocks[code]; ok {
			ticks = append(ticks, *tick)
		}
	}

	for _, b := range cache.blocks {
		if b.Name == blockName && b.Source == source && (blockType == "" || b.BlockType == blockType) {
			blockPct = b.PctChange
			break
		}
	}

	sortStockTicks(ticks, sortBy, order)

	if limit > 0 && limit < len(ticks) {
		ticks = ticks[:limit]
	}
	return blockPct, ticks
}

// GetStockTick returns the latest tick for a single code.
func (t *TickerService) GetStockTick(code string) *StockTick {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if tick, ok := t.cache.stocks[code]; ok {
		cp := *tick
		return &cp
	}
	return nil
}

// UpdatedAt returns the last update time.
func (t *TickerService) UpdatedAt() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cache.updatedAt
}

// MarketScreen returns sorted ticks after asset_type and optional limit-up/down filter.
// filterNote is non-empty when filter forces stock-only semantics.
func (t *TickerService) MarketScreen(sortBy, order, filter, assetType string, limit int) ([]StockTick, string) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	filterNote := ""
	if filter == "limit_up" || filter == "limit_down" {
		assetType = "stock"
		filterNote = "涨跌停筛选仅适用于股票"
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	list := make([]StockTick, 0, len(t.cache.stocks))
	for _, v := range t.cache.stocks {
		if v == nil {
			continue
		}
		switch assetType {
		case "all":
			// no filter
		case "etf":
			if v.AssetType != string(AssetTypeETF) {
				continue
			}
		case "stock":
			fallthrough
		default:
			if v.AssetType != string(AssetTypeStock) {
				continue
			}
		}
		switch filter {
		case "limit_up":
			if !v.IsLimitUp {
				continue
			}
		case "limit_down":
			if !v.IsLimitDown {
				continue
			}
		}
		list = append(list, *v)
	}

	if sortBy == "change_pct" || sortBy == "" {
		sortBy = "pct_change"
	}
	sortStockTicks(list, sortBy, order)
	if len(list) > limit {
		list = list[:limit]
	}
	return list, filterNote
}

func (t *TickerService) loop(ctx context.Context, allCodes []string, nameResolver func(string) string) {
	log.Printf("ticker: started, interval=%v, codes=%d", t.cfg.Interval, len(allCodes))

	t.tick(ctx, allCodes, nameResolver)

	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("ticker: stopped")
			return
		case <-ticker.C:
			if !t.inTradingSession() {
				continue
			}
			t.tick(ctx, allCodes, nameResolver)
		}
	}
}

func (t *TickerService) inTradingSession() bool {
	now := t.cfg.Now()
	loc := now.Location()
	h, m, _ := now.Clock()
	startMin := marketOpenHour*60 + marketOpenMin
	endMin := marketCloseHour*60 + marketCloseMin
	nowMin := h*60 + m
	if nowMin < startMin || nowMin > endMin {
		return false
	}
	wd := now.In(loc).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

func (t *TickerService) tick(ctx context.Context, allCodes []string, nameResolver func(string) string) {
	snapshots, err := t.batchFetchQuotes(ctx, allCodes)
	if err != nil {
		log.Printf("ticker: fetch error: %v", err)
		return
	}
	if len(snapshots) == 0 {
		return
	}

	now := t.cfg.Now()
	tradeDate := now.Format("20060102")

	stocks := make(map[string]*StockTick, len(snapshots))
	for i := range snapshots {
		q := &snapshots[i]
		preClose := q.PreClose.Float64()
		last := q.Last.Float64()
		high := q.High.Float64()
		low := q.Low.Float64()

		var pctChange, priceChange, amplitude float64
		if preClose > 0 {
			priceChange = last - preClose
			pctChange = priceChange / preClose * 100
			amplitude = (high - low) / preClose * 100
		}

		name := q.Name
		if name == "" && nameResolver != nil {
			name = nameResolver(q.Code)
		}

		at := q.AssetType
		if at == AssetTypeUnknown || at == "" {
			at = assetTypeFromCode(q.Code)
		}

		tick := &StockTick{
			Code:        q.Code,
			Name:        name,
			Exchange:    q.Exchange,
			AssetType:   string(at),
			Last:        last,
			PreClose:    preClose,
			Open:        q.Open.Float64(),
			High:        high,
			Low:         low,
			PctChange:   math.Round(pctChange*100) / 100,
			PriceChange: math.Round(priceChange*1000) / 1000,
			Volume:      q.VolumeHand,
			Amount:      q.AmountYuan,
			Amplitude:   math.Round(amplitude*100) / 100,
			IsLimitUp:   isLimitUp(pctChange, q.Code, name),
			IsLimitDown: isLimitDown(pctChange, q.Code, name),
		}
		stocks[q.Code] = tick
	}

	blocks := t.aggregateBlocks(stocks)
	overview := computeMarketOverview(stocks)

	t.mu.Lock()
	if t.limitDate != tradeDate {
		t.limitUp = make(map[string]*limitSideState)
		t.limitDown = make(map[string]*limitSideState)
		t.limitDate = tradeDate
	}
	// On the very first tick we discover existing states rather than observing
	// transitions, so timestamps are approximate (review issue P2).
	firstTick := t.cache.updatedAt.IsZero()
	for i := range snapshots {
		q := &snapshots[i]
		at := q.AssetType
		if at == AssetTypeUnknown || at == "" {
			at = assetTypeFromCode(q.Code)
		}
		if at != AssetTypeStock {
			continue
		}
		tick := stocks[q.Code]
		if tick == nil {
			continue
		}
		approx := firstTick && t.limitUp[q.Code] == nil
		updateLimitSideMap(t.limitUp, q.Code, tick.IsLimitUp, now, buy1Volume(q), approx)
		approx = firstTick && t.limitDown[q.Code] == nil
		updateLimitSideMap(t.limitDown, q.Code, tick.IsLimitDown, now, sell1Volume(q), approx)
	}

	t.cache = tickerCache{
		stocks:    stocks,
		blocks:    blocks,
		overview:  overview,
		updatedAt: now,
	}
	t.mu.Unlock()
}

func buy1Volume(q *QuoteSnapshot) int64 {
	if len(q.BuyLevels) > 0 {
		return int64(q.BuyLevels[0].Number)
	}
	return 0
}

func sell1Volume(q *QuoteSnapshot) int64 {
	if len(q.SellLevels) > 0 {
		return int64(q.SellLevels[0].Number)
	}
	return 0
}

func updateLimitSideMap(m map[string]*limitSideState, code string, active bool, now time.Time, edgeVol int64, approx bool) {
	st := m[code]
	if active {
		if st == nil {
			st = &limitSideState{}
			m[code] = st
		}
		if !st.Active {
			st.FirstSeen = now
			st.Active = true
			st.Approximate = approx
		}
		st.LastSeen = now
		st.EdgeVolume = edgeVol
		return
	}
	if st != nil && st.Active {
		st.BreakCount++
		st.Active = false
	}
}

func computeMarketOverview(stocks map[string]*StockTick) MarketOverview {
	ov := MarketOverview{
		ByExchange: map[string]AssetOverview{
			"sh": {},
			"sz": {},
			"bj": {},
		},
	}
	add := func(dst *AssetOverview, tick *StockTick, countLimits bool) {
		dst.Total++
		switch {
		case tick.PctChange > 0:
			dst.Up++
		case tick.PctChange < 0:
			dst.Down++
		default:
			dst.Flat++
		}
		dst.TotalAmount += tick.Amount
		dst.TotalVolume += tick.Volume
		if countLimits {
			if tick.IsLimitUp {
				dst.LimitUp++
			}
			if tick.IsLimitDown {
				dst.LimitDown++
			}
		}
	}

	for _, tick := range stocks {
		if tick == nil {
			continue
		}
		ex := strings.ToLower(strings.TrimSpace(tick.Exchange))
		if ex == "" {
			ex = "other"
		}
		bucket, ok := ov.ByExchange[ex]
		if !ok {
			bucket = AssetOverview{}
		}
		add(&bucket, tick, tick.AssetType == string(AssetTypeStock))
		ov.ByExchange[ex] = bucket

		switch tick.AssetType {
		case string(AssetTypeStock):
			add(&ov.Stock, tick, true)
		case string(AssetTypeETF):
			add(&ov.ETF, tick, false)
		}
	}

	finalize := func(a *AssetOverview) {
		if a.Total > 0 {
			a.UpRatio = math.Round(float64(a.Up)/float64(a.Total)*10000) / 100
		}
	}
	finalize(&ov.Stock)
	finalize(&ov.ETF)
	for k := range ov.ByExchange {
		v := ov.ByExchange[k]
		finalize(&v)
		ov.ByExchange[k] = v
	}
	return ov
}

func (t *TickerService) batchFetchQuotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	var all []QuoteSnapshot
	for i := 0; i < len(codes); i += quoteFetchBatchSize {
		end := i + quoteFetchBatchSize
		if end > len(codes) {
			end = len(codes)
		}
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}
		batch, err := t.provider.Quotes(ctx, codes[i:end])
		if err != nil {
			log.Printf("ticker: batch %d-%d/%d failed: %v", i, end, len(codes), err)
			continue
		}
		all = append(all, batch...)
	}
	return all, nil
}

func (t *TickerService) aggregateBlocks(stocks map[string]*StockTick) []BlockRank {
	if t.block == nil || !t.block.Loaded() {
		return nil
	}

	allGroups := t.block.GetBlocks("")
	ranks := make([]BlockRank, 0, len(allGroups))

	for _, grp := range allGroups {
		members := t.block.GetBlockMembers(grp.Source, grp.BlockType, grp.Name)
		rank := BlockRank{
			Name:        grp.Name,
			Source:      grp.Source,
			BlockType:   grp.BlockType,
			MemberCount: len(members),
		}

		var totalPct float64
		var leading *StockTick
		available := 0

		for _, code := range members {
			tick, ok := stocks[code]
			if !ok {
				continue
			}
			available++
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
				leading = tick
			}
		}

		rank.AvailableCount = available
		if available > 0 {
			rank.PctChange = math.Round(totalPct/float64(available)*100) / 100
		}
		if leading != nil {
			rank.LeadingCode = leading.Code
			rank.LeadingName = leading.Name
			rank.LeadingPct = leading.PctChange
		}

		ranks = append(ranks, rank)
	}

	return ranks
}

func isLimitUp(pct float64, code, name string) bool {
	threshold := limitThreshold(code, name)
	return pct >= threshold-0.05
}

func isLimitDown(pct float64, code, name string) bool {
	threshold := limitThreshold(code, name)
	return pct <= -(threshold - 0.05)
}

func limitThreshold(code, name string) float64 {
	bare := code
	if len(code) > 2 {
		bare = code[2:]
	}
	switch {
	case strings.HasPrefix(bare, "68"), strings.HasPrefix(bare, "30"):
		return 20.0
	case strings.HasPrefix(bare, "83"), strings.HasPrefix(bare, "87"),
		strings.HasPrefix(bare, "82"), strings.HasPrefix(bare, "43"):
		return 30.0
	default:
		if isSTStock(name) {
			return 5.0
		}
		return 10.0
	}
}

func isSTStock(name string) bool {
	return strings.Contains(strings.ToUpper(name), "ST")
}

func sortBlockRanks(ranks []BlockRank, sortBy, order string) {
	desc := !strings.EqualFold(order, "asc")
	less := func(i, j int) bool {
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
	}
	sort.SliceStable(ranks, less)
}

func sortStockTicks(ticks []StockTick, sortBy, order string) {
	desc := !strings.EqualFold(order, "asc")
	less := func(i, j int) bool {
		var a, b float64
		switch sortBy {
		case "amount":
			a, b = ticks[i].Amount, ticks[j].Amount
		case "volume":
			a, b = float64(ticks[i].Volume), float64(ticks[j].Volume)
		case "amplitude":
			a, b = ticks[i].Amplitude, ticks[j].Amplitude
		case "pct_change", "change_pct":
			a, b = ticks[i].PctChange, ticks[j].PctChange
		default:
			a, b = ticks[i].PctChange, ticks[j].PctChange
		}
		if desc {
			return a > b
		}
		return a < b
	}
	sort.SliceStable(ticks, less)
}
