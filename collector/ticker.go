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

// BlockRank is the aggregated ranking entry for a single block.
type BlockRank struct {
	Name             string  `json:"name"`
	BlockType        string  `json:"block_type"`
	PctChange        float64 `json:"pct_change"`
	Amount           float64 `json:"amount"`
	LeadingCode      string  `json:"leading_stock_code"`
	LeadingName      string  `json:"leading_stock_name"`
	LeadingPct       float64 `json:"leading_stock_pct"`
	RiseCount        int     `json:"rise_count"`
	FallCount        int     `json:"fall_count"`
	FlatCount        int     `json:"flat_count"`
	LimitUpCount     int     `json:"limit_up_count"`
	LimitDownCount   int     `json:"limit_down_count"`
	MemberCount      int     `json:"member_count"`
	AvailableCount   int     `json:"available_count"`
}

type tickerCache struct {
	stocks    map[string]*StockTick // code -> tick
	blocks    []BlockRank
	updatedAt time.Time
}

// TickerService provides near-real-time market snapshots by polling TDX
// every N seconds during trading hours.
type TickerService struct {
	provider QuoteProvider
	block    *BlockService
	cfg      TickerConfig
	mu       sync.RWMutex
	cache    tickerCache
	cancel   context.CancelFunc
	running  bool
}

func NewTickerService(provider QuoteProvider, block *BlockService, cfg TickerConfig) *TickerService {
	if cfg.Interval <= 0 {
		cfg.Interval = tickerDefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &TickerService{
		provider: provider,
		block:    block,
		cfg:      cfg,
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

// GetBlockRanking returns a sorted copy of block rankings.
func (t *TickerService) GetBlockRanking(blockType string, sortBy string, order string, limit int) []BlockRank {
	t.mu.RLock()
	src := t.cache.blocks
	t.mu.RUnlock()

	var filtered []BlockRank
	for _, b := range src {
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
func (t *TickerService) GetBlockStocks(blockType, blockName string, sortBy string, order string, limit int) (blockPct float64, ticks []StockTick) {
	codes := t.block.GetBlockMembers(blockType, blockName)
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
		if b.Name == blockName {
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

// GetStockTick returns the latest tick for a single stock.
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

// --------------- internal ---------------

func (t *TickerService) loop(ctx context.Context, allCodes []string, nameResolver func(string) string) {
	log.Printf("ticker: started, interval=%v, codes=%d", t.cfg.Interval, len(allCodes))

	// Always fetch once on startup regardless of trading session,
	// so the cache is never empty (TDX returns last-session snapshots outside hours).
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
	// skip weekends
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

		tick := &StockTick{
			Code:        q.Code,
			Name:        name,
			Exchange:    q.Exchange,
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

	t.mu.Lock()
	t.cache = tickerCache{
		stocks:    stocks,
		blocks:    blocks,
		updatedAt: t.cfg.Now(),
	}
	t.mu.Unlock()
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
		members := t.block.GetBlockMembers(grp.BlockType, grp.Name)
		rank := BlockRank{
			Name:        grp.Name,
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

// isLimitUp / isLimitDown use A-share price-limit rules:
//   - Science/Innovation board (68xxxx, 30xxxx): ±20%
//   - Beijing Stock Exchange  (83/87/82/43xxxx): ±30%
//   - Main board ST stocks (name contains "ST"):  ±5%
//   - Main board normal stocks:                   ±10%
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

// --------------- sorting helpers ---------------

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
		default: // pct_change
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
		default: // pct_change
			a, b = ticks[i].PctChange, ticks[j].PctChange
		}
		if desc {
			return a > b
		}
		return a < b
	}
	sort.SliceStable(ticks, less)
}
