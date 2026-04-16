package collector

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"xorm.io/xorm"
)

// SignalConfig configures the cold-path K-line scan for market signals.
type SignalConfig struct {
	Now            func() time.Time
	Interval       time.Duration // scan cadence; default 5m
	StaleAfter     time.Duration // API marks stale if older; default 15m
	KlineBaseDir   string
	HighLowWindow  int     // default 20
	VolumeLookback int     // default 5 trading days for avg volume
	VolumeRatioMin float64 // default 2.0
}

// SignalItem is one symbol flagged by the scanner.
type SignalItem struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	SignalType  string  `json:"signal_type"` // new_high | new_low | volume_spike
	Window      int     `json:"window,omitempty"`
	Price       float64 `json:"price,omitempty"`
	High        float64 `json:"ref_high,omitempty"`
	Low         float64 `json:"ref_low,omitempty"`
	Volume      int64   `json:"volume,omitempty"`
	AvgVolume   float64 `json:"avg_volume,omitempty"`
	VolumeRatio float64 `json:"volume_ratio,omitempty"`
	ChangePct   float64 `json:"change_pct,omitempty"`
}

// SignalSnapshot is the published result of one full scan (atomic swap).
type SignalSnapshot struct {
	Status         string       `json:"status"` // ready (internal); API may override scanning/stale/not_ready
	UpdatedAt      time.Time    `json:"updated_at"`
	ScanDurationMs int64        `json:"scan_duration_ms"`
	NewHigh        []SignalItem `json:"new_high"`
	NewLow         []SignalItem `json:"new_low"`
	VolumeSpike    []SignalItem `json:"volume_spike"`
}

// SignalService scans per-code K-line SQLite files and compares to Ticker snapshots.
// It does not run inside TickerService.
type SignalService struct {
	cfg    SignalConfig
	ticker *TickerService

	mu       sync.RWMutex
	codes    []string
	pub      *SignalSnapshot
	scanning bool
	cancel   context.CancelFunc
	running  bool
}

func NewSignalService(cfg SignalConfig) *SignalService {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = 15 * time.Minute
	}
	if cfg.HighLowWindow <= 0 {
		cfg.HighLowWindow = 20
	}
	if cfg.VolumeLookback <= 0 {
		cfg.VolumeLookback = 5
	}
	if cfg.VolumeRatioMin <= 0 {
		cfg.VolumeRatioMin = 2.0
	}
	return &SignalService{cfg: cfg}
}

// Start launches the background scan loop. Safe to call once.
func (s *SignalService) Start(ctx context.Context, codes []string, ticker *TickerService) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.codes = append([]string(nil), codes...)
	s.ticker = ticker
	scanCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	go s.loop(scanCtx)
}

func (s *SignalService) AttachTicker(ticker *TickerService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ticker != nil {
		s.ticker = ticker
	}
}

// Stop stops the scan loop.
func (s *SignalService) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	s.mu.Unlock()
}

// Running reports whether the service was started.
func (s *SignalService) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Snapshot returns the last completed scan plus effective status for APIs.
func (s *SignalService) Snapshot() (snap SignalSnapshot, apiStatus string) {
	s.mu.RLock()
	scanning := s.scanning
	pub := s.pub
	s.mu.RUnlock()

	if pub == nil {
		return SignalSnapshot{}, "not_ready"
	}
	snap = *pub
	switch {
	case scanning:
		apiStatus = "scanning"
	case time.Since(pub.UpdatedAt) > s.cfg.StaleAfter:
		apiStatus = "stale"
	default:
		apiStatus = "ready"
	}
	return snap, apiStatus
}

func (s *SignalService) CheckCodes(codes []string, signalTypes []string) ([]SignalItem, error) {
	if s.ticker == nil {
		return nil, errors.New("signal check requires ticker service")
	}
	if s.cfg.KlineBaseDir == "" {
		return nil, errors.New("signal check requires kline base dir")
	}

	filter, err := normalizeSignalTypeFilter(signalTypes)
	if err != nil {
		return nil, err
	}

	now := s.cfg.Now()
	uniq := make(map[string]struct{}, len(codes))
	items := make([]SignalItem, 0, len(codes))
	for _, rawCode := range codes {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			continue
		}
		if _, ok := uniq[code]; ok {
			continue
		}
		uniq[code] = struct{}{}

		tick := s.ticker.GetStockTick(code)
		if tick == nil {
			continue
		}

		dbPath := filepath.Join(s.cfg.KlineBaseDir, code+".db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		engine, err := openMetadataEngine(dbPath)
		if err != nil {
			continue
		}
		var rows []KlinePublishRow
		if err := engine.Table("DayKline").Desc("Date").Limit(s.cfg.HighLowWindow + s.cfg.VolumeLookback + 3).Find(&rows); err != nil {
			_ = engine.Close()
			continue
		}
		_ = engine.Close()

		for _, item := range evaluateSignalItems(tick, rows, s.cfg.HighLowWindow, s.cfg.VolumeLookback, now, s.cfg.VolumeRatioMin) {
			if filter[item.SignalType] {
				items = append(items, item)
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].SignalType < items[j].SignalType
	})
	return items, nil
}

func (s *SignalService) loop(ctx context.Context) {
	// Wait until the ticker has published at least one snapshot, otherwise the
	// first scan would see nil for every GetStockTick and publish an empty
	// "ready" snapshot that misleads callers until the next interval.
	if !s.waitForTickerReady(ctx, 90*time.Second) {
		return
	}

	s.runOneScan()

	interval := time.NewTicker(s.cfg.Interval)
	defer interval.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-interval.C:
			s.runOneScan()
		}
	}
}

// waitForTickerReady blocks until the ticker has a non-zero UpdatedAt or timeout/cancel.
func (s *SignalService) waitForTickerReady(ctx context.Context, timeout time.Duration) bool {
	if s.ticker == nil {
		return false
	}
	deadline := time.After(timeout)
	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	for {
		if !s.ticker.UpdatedAt().IsZero() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			log.Printf("signal: ticker not ready after %v, proceeding anyway", timeout)
			return true
		case <-poll.C:
		}
	}
}

func (s *SignalService) runOneScan() {
	if s.ticker == nil || s.cfg.KlineBaseDir == "" {
		return
	}

	s.mu.Lock()
	s.scanning = true
	s.mu.Unlock()

	start := s.cfg.Now()
	next := &SignalSnapshot{
		Status:      "ready",
		NewHigh:     make([]SignalItem, 0, 64),
		NewLow:      make([]SignalItem, 0, 64),
		VolumeSpike: make([]SignalItem, 0, 64),
	}

	codes := s.snapshotCodes()
	win := s.cfg.HighLowWindow
	vlb := s.cfg.VolumeLookback

	for _, code := range codes {
		dbPath := filepath.Join(s.cfg.KlineBaseDir, code+".db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		engine, err := openMetadataEngine(dbPath)
		if err != nil {
			continue
		}
		s.scanCode(engine, code, win, vlb, next)
		_ = engine.Close()
	}

	next.UpdatedAt = s.cfg.Now()
	next.ScanDurationMs = next.UpdatedAt.Sub(start).Milliseconds()

	s.mu.Lock()
	s.scanning = false
	s.pub = next
	s.mu.Unlock()

	log.Printf("signal: scan done codes=%d high=%d low=%d spike=%d in %dms",
		len(codes), len(next.NewHigh), len(next.NewLow), len(next.VolumeSpike), next.ScanDurationMs)
}

func (s *SignalService) snapshotCodes() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.codes))
	copy(out, s.codes)
	return out
}

func (s *SignalService) scanCode(engine *xorm.Engine, code string, win, vlb int, next *SignalSnapshot) {
	tick := s.ticker.GetStockTick(code)
	if tick == nil {
		return
	}

	var rows []KlinePublishRow
	if err := engine.Table("DayKline").Desc("Date").Limit(win + vlb + 3).Find(&rows); err != nil || len(rows) < win+1 {
		return
	}

	for _, item := range evaluateSignalItems(tick, rows, win, vlb, s.cfg.Now(), s.cfg.VolumeRatioMin) {
		switch item.SignalType {
		case "new_high":
			next.NewHigh = append(next.NewHigh, item)
		case "new_low":
			next.NewLow = append(next.NewLow, item)
		case "volume_spike":
			next.VolumeSpike = append(next.VolumeSpike, item)
		}
	}
}

func normalizeSignalTypeFilter(signalTypes []string) (map[string]bool, error) {
	filter := make(map[string]bool, 3)
	for _, raw := range signalTypes {
		switch normalized := strings.TrimSpace(strings.ToLower(raw)); normalized {
		case "new_high", "new_low", "volume_spike":
			filter[normalized] = true
		case "":
			continue
		default:
			return nil, errors.New("unsupported signal type: " + normalized)
		}
	}
	if len(filter) == 0 {
		return map[string]bool{
			"new_high":     true,
			"new_low":      true,
			"volume_spike": true,
		}, nil
	}
	return filter, nil
}

func evaluateSignalItems(tick *StockTick, rows []KlinePublishRow, win, vlb int, now time.Time, volumeRatioMin float64) []SignalItem {
	if tick == nil || len(rows) < win+1 {
		return nil
	}

	loc := now.Location()
	today0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()

	index := 0
	if len(rows) > 0 && rows[0].Date >= today0 {
		index = 1
	}
	if index+win > len(rows) {
		return nil
	}
	hist := rows[index : index+win]

	var maxHigh, minLow PriceMilli
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

	items := make([]SignalItem, 0, 3)
	refHigh := maxHigh.Float64()
	refLow := minLow.Float64()
	if tick.High >= refHigh-1e-9 {
		items = append(items, SignalItem{
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
		items = append(items, SignalItem{
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

	if index+win+vlb <= len(rows) {
		volSlice := rows[index+win : index+win+vlb]
		var sum int64
		for _, row := range volSlice {
			sum += row.Volume
		}
		if len(volSlice) > 0 {
			avg := float64(sum) / float64(len(volSlice))
			if avg > 0 && float64(tick.Volume)/avg >= volumeRatioMin {
				items = append(items, SignalItem{
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
	}

	return items
}
