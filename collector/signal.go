package collector

import (
	"context"
	"log"
	"os"
	"path/filepath"
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
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	SignalType string  `json:"signal_type"` // new_high | new_low | volume_spike
	Window     int     `json:"window,omitempty"`
	Price      float64 `json:"price,omitempty"`
	High       float64 `json:"ref_high,omitempty"`
	Low        float64 `json:"ref_low,omitempty"`
	Volume     int64   `json:"volume,omitempty"`
	AvgVolume  float64 `json:"avg_volume,omitempty"`
	VolumeRatio float64 `json:"volume_ratio,omitempty"`
	ChangePct  float64 `json:"change_pct,omitempty"`
}

// SignalSnapshot is the published result of one full scan (atomic swap).
type SignalSnapshot struct {
	Status         string        `json:"status"` // ready (internal); API may override scanning/stale/not_ready
	UpdatedAt      time.Time     `json:"updated_at"`
	ScanDurationMs int64         `json:"scan_duration_ms"`
	NewHigh        []SignalItem  `json:"new_high"`
	NewLow         []SignalItem  `json:"new_low"`
	VolumeSpike    []SignalItem  `json:"volume_spike"`
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
		Status:    "ready",
		NewHigh:   make([]SignalItem, 0, 64),
		NewLow:    make([]SignalItem, 0, 64),
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

	loc := s.cfg.Now().Location()
	today0 := time.Date(s.cfg.Now().Year(), s.cfg.Now().Month(), s.cfg.Now().Day(), 0, 0, 0, 0, loc).Unix()

	i := 0
	if len(rows) > 0 && rows[0].Date >= today0 {
		i = 1
	}
	if i+win > len(rows) {
		return
	}
	hist := rows[i : i+win]

	var maxH, minL PriceMilli
	for j := range hist {
		if j == 0 {
			maxH = hist[j].High
			minL = hist[j].Low
			continue
		}
		if hist[j].High > maxH {
			maxH = hist[j].High
		}
		if hist[j].Low < minL {
			minL = hist[j].Low
		}
	}

	th := maxH.Float64()
	tl := minL.Float64()
	if tick.High >= th-1e-9 {
		next.NewHigh = append(next.NewHigh, SignalItem{
			Code:       code,
			Name:       tick.Name,
			SignalType: "new_high",
			Window:     win,
			Price:      tick.Last,
			High:       th,
			Low:        tick.Low,
			Volume:     tick.Volume,
			ChangePct:  tick.PctChange,
		})
	}
	if tick.Low <= tl+1e-9 {
		next.NewLow = append(next.NewLow, SignalItem{
			Code:       code,
			Name:       tick.Name,
			SignalType: "new_low",
			Window:     win,
			Price:      tick.Last,
			Low:        tl,
			High:       tick.High,
			Volume:     tick.Volume,
			ChangePct:  tick.PctChange,
		})
	}

	if i+win+vlb <= len(rows) {
		volSlice := rows[i+win : i+win+vlb]
		var sum int64
		for _, r := range volSlice {
			sum += r.Volume
		}
		if len(volSlice) > 0 {
			avg := float64(sum) / float64(len(volSlice))
			if avg > 0 && float64(tick.Volume)/avg >= s.cfg.VolumeRatioMin {
				next.VolumeSpike = append(next.VolumeSpike, SignalItem{
					Code:        code,
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
}
