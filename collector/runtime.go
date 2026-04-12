package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RuntimeConfig struct {
	ScheduleName            string
	DailySyncScheduleName   string
	ReconcileScheduleName   string
	ReportDir               string
	Now                     func() time.Time
	CalendarLookback        int
	BootstrapStartDate      string
	TradeBootstrapStartDate string
	LiveBootstrapStartDate  string
	RequestMinInterval      time.Duration
	CatchUpWorkers          int
	KlinePeriods            []KlinePeriod
	Metadata                MetadataConfig
	Kline                   KlineConfig
	Trade                   TradeConfig
	OrderHistory            OrderHistoryConfig
	Live                    LiveCaptureConfig
	Fundamentals            FundamentalsConfig
	Block                   BlockConfig
	Ticker                  TickerConfig
}

type Runtime struct {
	store        *Store
	provider     Provider
	cfg          RuntimeConfig
	metadata     *MetadataService
	kline        *KlineService
	trade        *TradeService
	orderHistory *OrderHistoryService
	live         *LiveCaptureService
	fundamentals *FundamentalsService
	block        *BlockService
	ticker       *TickerService
	signal       *SignalService
}

func NewRuntime(store *Store, provider Provider, cfg RuntimeConfig) (*Runtime, error) {
	if store == nil {
		return nil, errors.New("collector runtime requires collector store")
	}
	if provider == nil {
		return nil, errors.New("collector runtime requires provider")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ScheduleName == "" {
		cfg.ScheduleName = "collector_startup_catchup"
	}
	if cfg.DailySyncScheduleName == "" {
		cfg.DailySyncScheduleName = "collector_daily_full_sync"
	}
	if cfg.ReconcileScheduleName == "" {
		cfg.ReconcileScheduleName = "collector_daily_reconcile"
	}
	if cfg.CalendarLookback <= 0 {
		cfg.CalendarLookback = 30
	}
	if len(cfg.KlinePeriods) == 0 {
		cfg.KlinePeriods = []KlinePeriod{PeriodDay}
	}
	if cfg.CatchUpWorkers <= 0 {
		cfg.CatchUpWorkers = 1
	}

	wireRuntimeConfig(&cfg)
	rawProvider := provider // keep un-throttled reference for ticker
	provider = newThrottledProvider(provider, cfg.RequestMinInterval, cfg.CatchUpWorkers)

	metadata, err := NewMetadataService(store, provider, cfg.Metadata)
	if err != nil {
		return nil, err
	}
	kline, err := NewKlineService(store, provider, cfg.Kline)
	if err != nil {
		return nil, err
	}
	tradeCfg := cfg.Trade
	tradeCfg.BootstrapStartDate = cfg.TradeBootstrapStartDate
	trade, err := NewTradeService(store, provider, tradeCfg)
	if err != nil {
		return nil, err
	}
	orderHistory, err := NewOrderHistoryService(store, provider, cfg.OrderHistory)
	if err != nil {
		return nil, err
	}
	liveCfg := cfg.Live
	liveCfg.BootstrapStartDate = cfg.LiveBootstrapStartDate
	live, err := NewLiveCaptureService(store, provider, liveCfg)
	if err != nil {
		return nil, err
	}
	fundamentals, err := NewFundamentalsService(store, provider, cfg.Fundamentals)
	if err != nil {
		return nil, err
	}
	block, err := NewBlockService(store, provider, cfg.Block)
	if err != nil {
		return nil, err
	}

	// Ticker uses the raw (un-throttled) provider: it has its own 3s cadence
	// and must not compete with catch-up for throttle slots.
	ticker := NewTickerService(rawProvider, block, cfg.Ticker)

	signal := NewSignalService(SignalConfig{
		Now:          cfg.Now,
		KlineBaseDir: cfg.Kline.BaseDir,
	})

	return &Runtime{
		store:        store,
		provider:     provider,
		cfg:          cfg,
		metadata:     metadata,
		kline:        kline,
		trade:        trade,
		orderHistory: orderHistory,
		live:         live,
		fundamentals: fundamentals,
		block:        block,
		ticker:       ticker,
		signal:       signal,
	}, nil
}

func (r *Runtime) BlockService() *BlockService {
	return r.block
}

func (r *Runtime) TickerService() *TickerService {
	return r.ticker
}

func (r *Runtime) SignalService() *SignalService {
	return r.signal
}

// ensureTickerStarted launches the ticker if not already running.
// Called internally once instruments are available (early in catch-up).
func (r *Runtime) ensureTickerStarted(instruments []Instrument) {
	if r.ticker == nil || r.ticker.Running() {
		return
	}
	codes := make([]string, 0, len(instruments))
	nameMap := make(map[string]string, len(instruments))
	for _, inst := range instruments {
		if inst.AssetType == AssetTypeStock || inst.AssetType == AssetTypeETF {
			codes = append(codes, inst.Code)
		}
		if inst.Name != "" {
			nameMap[inst.Code] = inst.Name
		}
	}
	nameResolver := func(code string) string { return nameMap[code] }
	r.ticker.Start(codes, nameResolver)
	log.Printf("ticker: launched with %d codes (early start during catch-up)", len(codes))
}

// ensureSignalStarted launches the K-line signal scanner (stocks only).
func (r *Runtime) ensureSignalStarted(instruments []Instrument) {
	if r.signal == nil || r.ticker == nil || r.signal.Running() {
		return
	}
	stockCodes := make([]string, 0, len(instruments))
	for _, inst := range instruments {
		if inst.AssetType == AssetTypeStock {
			stockCodes = append(stockCodes, inst.Code)
		}
	}
	if len(stockCodes) == 0 {
		return
	}
	r.signal.Start(context.Background(), stockCodes, r.ticker)
	log.Printf("signal: launched with %d stock codes (K-line scan)", len(stockCodes))
}

// StopTicker stops the background real-time quote polling.
func (r *Runtime) StopTicker() {
	if r.ticker != nil {
		r.ticker.Stop()
	}
	if r.signal != nil {
		r.signal.Stop()
	}
}

func (r *Runtime) RunStartupCatchUp(ctx context.Context) error {
	return r.runCatchUp(ctx, r.cfg.ScheduleName, "collector_startup_catchup")
}

func (r *Runtime) RunDailyFullSync(ctx context.Context) error {
	return r.runCatchUp(ctx, r.cfg.DailySyncScheduleName, "collector_daily_full_sync")
}

func (r *Runtime) RecoverInterruptedRuns() error {
	if r == nil || r.store == nil {
		return nil
	}
	count, err := r.store.InterruptRunningScheduleRuns("", "collector process restarted before run finished", r.cfg.Now())
	if err != nil {
		return err
	}
	if count > 0 {
		log.Printf("collector: marked %d stale running schedule runs as interrupted", count)
	}
	return nil
}

func (r *Runtime) SeedTradeHistoryCoverageStarts() error {
	if r == nil || r.store == nil {
		return nil
	}
	count, err := r.store.SeedTradeHistoryCoverageStarts(r.cfg.TradeBootstrapStartDate)
	if err != nil {
		return err
	}
	if count > 0 {
		log.Printf("collector: seeded %d trade history coverage-start cursors", count)
	}
	return nil
}

func (r *Runtime) SeedLiveCaptureCoverageStarts() error {
	if r == nil || r.store == nil {
		return nil
	}
	count, err := r.store.SeedLiveCaptureCoverageStarts(r.cfg.LiveBootstrapStartDate)
	if err != nil {
		return err
	}
	if count > 0 {
		log.Printf("collector: seeded %d live capture coverage-start cursors", count)
	}
	return nil
}

func (r *Runtime) runCatchUp(ctx context.Context, scheduleName, suiteName string) (err error) {
	startedAt := r.cfg.Now()
	if count, interruptErr := r.store.InterruptRunningScheduleRuns(scheduleName, "superseded by newer run", startedAt); interruptErr != nil {
		return interruptErr
	} else if count > 0 {
		log.Printf("collector: interrupted %d stale %s runs before starting a new one", count, scheduleName)
	}

	run := &ScheduleRunRecord{
		ScheduleName: scheduleName,
		Status:       "running",
		StartedAt:    startedAt,
	}
	if err := r.store.AddScheduleRun(run); err != nil {
		return err
	}
	progress := func(format string, args ...any) {
		log.Printf("collector catch-up progress: schedule=%s "+format, append([]any{scheduleName}, args...)...)
	}

	details := ""
	defer func() {
		run.EndedAt = r.cfg.Now()
		if err != nil {
			run.Status = "failed"
			run.Details = err.Error()
		} else {
			run.Status = "passed"
			run.Details = details
		}
		_ = r.store.UpdateScheduleRun(run)
	}()

	progress("phase=metadata status=starting")
	if err := r.metadata.RefreshAll(ctx); err != nil {
		return err
	}
	progress("phase=metadata status=done")

	progress("phase=block_sync status=starting")
	if err := r.block.SyncBlocks(ctx); err != nil {
		progress("phase=block_sync status=SKIPPED err=%v", err)
	} else {
		progress("phase=block_sync status=done")
	}

	instruments, err := r.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock, AssetTypeETF, AssetTypeIndex},
	})
	if err != nil {
		return err
	}
	instruments = normalizeInstruments(instruments)
	progress("phase=instruments status=done count=%d", len(instruments))

	// Start the real-time ticker as early as possible (metadata + block + instruments are ready).
	// This is idempotent — subsequent calls are no-ops if already running.
	r.ensureTickerStarted(instruments)
	// NOTE: signal scanner is started AFTER catch-up completes (see below) so that
	// K-line DBs are up-to-date and the ticker has published its first snapshot.

	tradingDays, err := r.loadTradingDays(ctx, instruments)
	if err != nil {
		return err
	}
	progress("phase=trading_days status=done count=%d range=%s", len(tradingDays), tradingDayWindow(tradingDays))

	quoteCodes := quoteCaptureCodes(instruments)
	if len(quoteCodes) > 0 {
		trading, err := r.provider.IsTradingDay(ctx, r.cfg.Now())
		if err != nil {
			progress("phase=quote_snapshot status=SKIPPED err=%v", err)
		} else if trading {
			progress("phase=quote_snapshot status=starting count=%d", len(quoteCodes))
			if err := r.live.CaptureQuotes(ctx, QuoteCaptureQuery{
				Codes:       quoteCodes,
				CaptureTime: r.cfg.Now(),
			}); err != nil {
				progress("phase=quote_snapshot status=SKIPPED count=%d err=%v", len(quoteCodes), err)
			} else {
				progress("phase=quote_snapshot status=done count=%d", len(quoteCodes))
			}
		}
	}

	counters := &catchUpCounters{}
	if err := r.runCatchUpInstruments(ctx, instruments, tradingDays, progress, counters); err != nil {
		return err
	}

	// K-line catch-up is done for all instruments; start the signal scanner now
	// so that its first scan has complete K-line data and the ticker has had time
	// to publish at least one snapshot.
	r.ensureSignalStarted(instruments)

	skipped := counters.skipped.Load()
	details = fmt.Sprintf(
		"instruments=%d trading_days=%d kline_runs=%d trade_runs=%d order_runs=%d live_runs=%d finance_runs=%d f10_runs=%d quote_codes=%d skipped=%d",
		len(instruments), len(tradingDays), counters.klineRuns.Load(), counters.tradeRuns.Load(), counters.orderRuns.Load(), counters.liveRuns.Load(), counters.financeRuns.Load(), counters.f10Runs.Load(), len(quoteCodes), skipped,
	)
	if errSummary := counters.errorSummary(5); errSummary != "" {
		details += " errors=[" + errSummary + "]"
	}
	if skipped > 0 {
		progress("catch-up finished with %d skipped items; see details for error summary", skipped)
	}
	return r.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("runtime-%d", time.Now().UnixNano()),
		PhaseID:     "phase_7",
		SuiteName:   suiteName,
		Status:      "passed",
		Blocking:    true,
		CommandText: scheduleName,
		OutputText:  details,
	})
}

type catchUpCounters struct {
	klineRuns   atomic.Int64
	tradeRuns   atomic.Int64
	orderRuns   atomic.Int64
	liveRuns    atomic.Int64
	financeRuns atomic.Int64
	f10Runs     atomic.Int64
	skipped     atomic.Int64
	errMessages sync.Map
}

func (c *catchUpCounters) recordError(phase, code string, err error) {
	c.skipped.Add(1)
	key := fmt.Sprintf("%s/%s", phase, code)
	c.errMessages.Store(key, err.Error())
}

func (c *catchUpCounters) errorSummary(limit int) string {
	count := 0
	var samples []string
	c.errMessages.Range(func(key, value any) bool {
		count++
		if len(samples) < limit {
			samples = append(samples, fmt.Sprintf("%s: %s", key, value))
		}
		return true
	})
	if count == 0 {
		return ""
	}
	summary := strings.Join(samples, "; ")
	if count > limit {
		summary += fmt.Sprintf(" ... and %d more", count-limit)
	}
	return summary
}

type catchUpInstrumentJob struct {
	index      int
	instrument Instrument
}

func (r *Runtime) runCatchUpInstruments(ctx context.Context, instruments []Instrument, tradingDays []TradingDay, progress func(string, ...any), counters *catchUpCounters) error {
	if len(instruments) == 0 {
		return nil
	}
	workerCount := r.cfg.CatchUpWorkers
	if workerCount <= 1 || len(instruments) == 1 {
		for index, instrument := range instruments {
			if err := r.runCatchUpInstrument(ctx, index, len(instruments), instrument, tradingDays, progress, counters); err != nil {
				return err
			}
		}
		return nil
	}
	if workerCount > len(instruments) {
		workerCount = len(instruments)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan catchUpInstrumentJob)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := r.runCatchUpInstrument(ctx, job.index, len(instruments), job.instrument, tradingDays, progress, counters); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}

	for index, instrument := range instruments {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			select {
			case err := <-errCh:
				return err
			default:
				return ctx.Err()
			}
		case jobs <- catchUpInstrumentJob{index: index, instrument: instrument}:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func isFatalCtxError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (r *Runtime) runCatchUpInstrument(ctx context.Context, index, total int, instrument Instrument, tradingDays []TradingDay, progress func(string, ...any), counters *catchUpCounters) error {
	progress("phase=instrument status=starting instrument=%d/%d code=%s asset=%s", index+1, total, instrument.Code, instrument.AssetType)
	for periodIndex, period := range r.cfg.KlinePeriods {
		progress("phase=kline status=starting instrument=%d/%d code=%s period=%s step=%d/%d", index+1, total, instrument.Code, period, periodIndex+1, len(r.cfg.KlinePeriods))
		if err := r.kline.Refresh(ctx, KlineCollectQuery{
			Code:      instrument.Code,
			AssetType: instrument.AssetType,
			Period:    period,
		}); err != nil {
			if isFatalCtxError(err) {
				return err
			}
			counters.recordError("kline/"+string(period), instrument.Code, err)
			progress("phase=kline status=SKIPPED instrument=%d/%d code=%s period=%s err=%v", index+1, total, instrument.Code, period, err)
			continue
		}
		totalRuns := counters.klineRuns.Add(1)
		progress("phase=kline status=done instrument=%d/%d code=%s period=%s total_runs=%d", index+1, total, instrument.Code, period, totalRuns)
	}

	if instrument.AssetType == AssetTypeStock || instrument.AssetType == AssetTypeETF {
		dates, err := r.pendingTradingDates("trade_history", instrument.AssetType, instrument.Code, tradingDays)
		if err != nil {
			return err
		}
		if len(dates) > 0 {
			progress("phase=trade_history status=pending instrument=%d/%d code=%s dates=%d range=%s", index+1, total, instrument.Code, len(dates), dateWindow(dates))
		}
		consecutiveFails := 0
		for dateIndex, date := range dates {
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=trade_history status=starting instrument=%d/%d code=%s date=%s progress=%d/%d", index+1, total, instrument.Code, date, dateIndex+1, len(dates))
			}
			if err := r.trade.RefreshDay(ctx, TradeCollectQuery{
				Code:      instrument.Code,
				AssetType: instrument.AssetType,
				Date:      date,
			}); err != nil {
				if isFatalCtxError(err) {
					return err
				}
				consecutiveFails++
				counters.recordError("trade_history", instrument.Code+"/"+date, err)
				progress("phase=trade_history status=SKIPPED instrument=%d/%d code=%s date=%s err=%v", index+1, total, instrument.Code, date, err)
				if consecutiveFails >= 3 {
					progress("phase=trade_history status=BAIL instrument=%d/%d code=%s consecutive_fails=%d remaining_dates=%d", index+1, total, instrument.Code, consecutiveFails, len(dates)-dateIndex-1)
					break
				}
				continue
			}
			consecutiveFails = 0
			totalRuns := counters.tradeRuns.Add(1)
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=trade_history status=done instrument=%d/%d code=%s date=%s total_runs=%d", index+1, total, instrument.Code, date, totalRuns)
			}
		}

		dates, err = r.pendingTradingDates("live_capture", instrument.AssetType, instrument.Code, tradingDays)
		if err != nil {
			return err
		}
		if len(dates) > 0 {
			progress("phase=live_capture status=pending instrument=%d/%d code=%s dates=%d range=%s", index+1, total, instrument.Code, len(dates), dateWindow(dates))
		}
		consecutiveFails = 0
		for dateIndex, date := range dates {
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=live_capture status=starting instrument=%d/%d code=%s date=%s progress=%d/%d", index+1, total, instrument.Code, date, dateIndex+1, len(dates))
			}
			if err := r.live.ReconcileDay(ctx, SessionCaptureQuery{
				Code:      instrument.Code,
				AssetType: instrument.AssetType,
				Date:      date,
			}); err != nil {
				if isFatalCtxError(err) {
					return err
				}
				consecutiveFails++
				counters.recordError("live_capture", instrument.Code+"/"+date, err)
				progress("phase=live_capture status=SKIPPED instrument=%d/%d code=%s date=%s err=%v", index+1, total, instrument.Code, date, err)
				if consecutiveFails >= 3 {
					progress("phase=live_capture status=BAIL instrument=%d/%d code=%s consecutive_fails=%d remaining_dates=%d", index+1, total, instrument.Code, consecutiveFails, len(dates)-dateIndex-1)
					break
				}
				continue
			}
			consecutiveFails = 0
			totalRuns := counters.liveRuns.Add(1)
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=live_capture status=done instrument=%d/%d code=%s date=%s total_runs=%d", index+1, total, instrument.Code, date, totalRuns)
			}
		}
	}

	if instrument.AssetType == AssetTypeStock {
		dates, err := r.pendingTradingDates("order_history", instrument.AssetType, instrument.Code, tradingDays)
		if err != nil {
			return err
		}
		if len(dates) > 0 {
			progress("phase=order_history status=pending instrument=%d/%d code=%s dates=%d range=%s", index+1, total, instrument.Code, len(dates), dateWindow(dates))
		}
		consecutiveFails := 0
		for dateIndex, date := range dates {
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=order_history status=starting instrument=%d/%d code=%s date=%s progress=%d/%d", index+1, total, instrument.Code, date, dateIndex+1, len(dates))
			}
			if err := r.orderHistory.RefreshDay(ctx, OrderHistoryCollectQuery{
				Code:      instrument.Code,
				AssetType: instrument.AssetType,
				Date:      date,
			}); err != nil {
				if isFatalCtxError(err) {
					return err
				}
				consecutiveFails++
				counters.recordError("order_history", instrument.Code+"/"+date, err)
				progress("phase=order_history status=SKIPPED instrument=%d/%d code=%s date=%s err=%v", index+1, total, instrument.Code, date, err)
				if consecutiveFails >= 3 {
					progress("phase=order_history status=BAIL instrument=%d/%d code=%s consecutive_fails=%d remaining_dates=%d", index+1, total, instrument.Code, consecutiveFails, len(dates)-dateIndex-1)
					break
				}
				continue
			}
			consecutiveFails = 0
			totalRuns := counters.orderRuns.Add(1)
			if shouldLogDateProgress(dateIndex, len(dates)) {
				progress("phase=order_history status=done instrument=%d/%d code=%s date=%s total_runs=%d", index+1, total, instrument.Code, date, totalRuns)
			}
		}

		progress("phase=finance status=starting instrument=%d/%d code=%s", index+1, total, instrument.Code)
		if err := r.fundamentals.RefreshFinance(ctx, instrument.Code); err != nil {
			if isFatalCtxError(err) {
				return err
			}
			counters.recordError("finance", instrument.Code, err)
			progress("phase=finance status=SKIPPED instrument=%d/%d code=%s err=%v", index+1, total, instrument.Code, err)
		} else {
			financeRuns := counters.financeRuns.Add(1)
			progress("phase=finance status=done instrument=%d/%d code=%s total_runs=%d", index+1, total, instrument.Code, financeRuns)
		}

		progress("phase=f10 status=starting instrument=%d/%d code=%s", index+1, total, instrument.Code)
		if err := r.fundamentals.SyncF10(ctx, instrument.Code); err != nil {
			if isFatalCtxError(err) {
				return err
			}
			counters.recordError("f10", instrument.Code, err)
			progress("phase=f10 status=SKIPPED instrument=%d/%d code=%s err=%v", index+1, total, instrument.Code, err)
		} else {
			f10Runs := counters.f10Runs.Add(1)
			progress("phase=f10 status=done instrument=%d/%d code=%s total_runs=%d", index+1, total, instrument.Code, f10Runs)
		}
	}

	progress(
		"phase=instrument status=done instrument=%d/%d code=%s kline_runs=%d trade_runs=%d order_runs=%d live_runs=%d finance_runs=%d f10_runs=%d skipped=%d",
		index+1,
		total,
		instrument.Code,
		counters.klineRuns.Load(),
		counters.tradeRuns.Load(),
		counters.orderRuns.Load(),
		counters.liveRuns.Load(),
		counters.financeRuns.Load(),
		counters.f10Runs.Load(),
		counters.skipped.Load(),
	)
	return nil
}

func shouldLogDateProgress(index, total int) bool {
	if total <= 5 {
		return true
	}
	position := index + 1
	return position == 1 || position == total || position%50 == 0
}

func dateWindow(dates []string) string {
	if len(dates) == 0 {
		return "-"
	}
	if len(dates) == 1 {
		return dates[0]
	}
	return dates[0] + ".." + dates[len(dates)-1]
}

func tradingDayWindow(items []TradingDay) string {
	if len(items) == 0 {
		return "-"
	}
	if len(items) == 1 {
		return items[0].Date
	}
	return items[0].Date + ".." + items[len(items)-1].Date
}

func wireRuntimeConfig(cfg *RuntimeConfig) {
	if cfg.Metadata.Now == nil {
		cfg.Metadata.Now = cfg.Now
	}
	if cfg.Kline.Now == nil {
		cfg.Kline.Now = cfg.Now
	}
	if cfg.Trade.Now == nil {
		cfg.Trade.Now = cfg.Now
	}
	if cfg.OrderHistory.Now == nil {
		cfg.OrderHistory.Now = cfg.Now
	}
	if cfg.Live.Now == nil {
		cfg.Live.Now = cfg.Now
	}
	if cfg.Fundamentals.Now == nil {
		cfg.Fundamentals.Now = cfg.Now
	}
	if cfg.Block.Now == nil {
		cfg.Block.Now = cfg.Now
	}
	if cfg.Ticker.Now == nil {
		cfg.Ticker.Now = cfg.Now
	}

	if cfg.Kline.BaseDir == "" {
		cfg.Kline.BaseDir = filepath.Join(DefaultBaseDir, "kline")
	}
	if cfg.Trade.BaseDir == "" {
		cfg.Trade.BaseDir = filepath.Join(DefaultBaseDir, "trade")
	}
	if cfg.OrderHistory.BaseDir == "" {
		cfg.OrderHistory.BaseDir = filepath.Join(DefaultBaseDir, "order_history")
	}
	if cfg.Live.BaseDir == "" {
		cfg.Live.BaseDir = filepath.Join(DefaultBaseDir, "live")
	}
	if cfg.Fundamentals.BaseDir == "" {
		cfg.Fundamentals.BaseDir = filepath.Join(DefaultBaseDir, "fundamentals")
	}
	if cfg.Block.BaseDir == "" {
		cfg.Block.BaseDir = filepath.Join(DefaultBaseDir, "block")
	}
	if cfg.ReportDir == "" {
		cfg.ReportDir = filepath.Join(DefaultBaseDir, "collector_reports")
	}
}

func (r *Runtime) loadTradingDays(ctx context.Context, instruments []Instrument) ([]TradingDay, error) {
	start := r.cfg.Now().AddDate(0, 0, -r.cfg.CalendarLookback)
	end := r.cfg.Now().Add(24 * time.Hour)
	needsBootstrap := false
	for _, instrument := range instruments {
		cursors := []struct {
			domain string
			asset  AssetType
		}{
			{domain: "trade_history", asset: instrument.AssetType},
			{domain: "live_capture", asset: instrument.AssetType},
		}
		if instrument.AssetType == AssetTypeStock {
			cursors = append(cursors, struct {
				domain string
				asset  AssetType
			}{domain: "order_history", asset: instrument.AssetType})
		}
		for _, item := range cursors {
			cursor, err := r.store.GetCollectCursor(item.domain, string(item.asset), instrument.Code, "")
			if err != nil {
				return nil, err
			}
			if cursor == nil || cursor.Cursor == "" {
				needsBootstrap = true
				continue
			}
			parsed, err := parseTradeCursor(cursor.Cursor)
			if err != nil {
				return nil, err
			}
			if !parsed.IsZero() && parsed.Before(start) {
				start = parsed
			}
		}
	}
	if needsBootstrap {
		start = parseBootstrapStartDate(r.cfg.BootstrapStartDate)
	}

	items, err := r.provider.TradingDays(ctx, TradingDayQuery{
		Start: start,
		End:   end,
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Time.Equal(items[j].Time) {
			return items[i].Date < items[j].Date
		}
		return items[i].Time.Before(items[j].Time)
	})

	out := make([]TradingDay, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Date == "" {
			continue
		}
		if _, ok := seen[item.Date]; ok {
			continue
		}
		seen[item.Date] = struct{}{}
		out = append(out, item)
	}
	return out, nil
}

func (r *Runtime) pendingTradingDates(domain string, assetType AssetType, code string, tradingDays []TradingDay) ([]string, error) {
	if len(tradingDays) == 0 {
		return nil, nil
	}
	floor := r.bootstrapStartForDomain(domain)
	filterByFloor := func(date string) bool {
		if floor.IsZero() {
			return true
		}
		parsed, err := parseTradeCursor(date)
		if err != nil {
			return date >= floor.Format("20060102")
		}
		return !parsed.Before(floor)
	}
	switch domain {
	case tradeHistoryDomain:
		return r.pendingCoverageDates(domain, tradeHistoryCoverageStartDomain, assetType, code, tradingDays, filterByFloor)
	case liveCaptureDomain:
		return r.pendingCoverageDates(domain, liveCaptureCoverageStartDomain, assetType, code, tradingDays, filterByFloor)
	}
	cursor, err := r.store.GetCollectCursor(domain, string(assetType), code, "")
	if err != nil {
		return nil, err
	}
	if cursor == nil || cursor.Cursor == "" {
		out := make([]string, 0, len(tradingDays))
		for _, day := range tradingDays {
			if filterByFloor(day.Date) {
				out = append(out, day.Date)
			}
		}
		return out, nil
	}

	out := make([]string, 0, len(tradingDays))
	for _, day := range tradingDays {
		if tradeDateAfter(day.Date, cursor.Cursor) && filterByFloor(day.Date) {
			out = append(out, day.Date)
		}
	}
	return out, nil
}

func (r *Runtime) pendingCoverageDates(domain, coverageDomain string, assetType AssetType, code string, tradingDays []TradingDay, filterByFloor func(string) bool) ([]string, error) {
	latest, err := r.store.GetCollectCursor(domain, string(assetType), code, "")
	if err != nil {
		return nil, err
	}
	coverageStart, err := r.store.GetCollectCursor(coverageDomain, string(assetType), code, "")
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(tradingDays))
	for _, day := range tradingDays {
		if !filterByFloor(day.Date) {
			continue
		}
		if coverageStart != nil && coverageStart.Cursor != "" && tradeDateAfter(coverageStart.Cursor, day.Date) {
			out = append(out, day.Date)
			continue
		}
		if latest == nil || latest.Cursor == "" || tradeDateAfter(day.Date, latest.Cursor) {
			out = append(out, day.Date)
		}
	}
	return out, nil
}

func (r *Runtime) bootstrapStartForDomain(domain string) time.Time {
	if domain == tradeHistoryDomain {
		if start := parseBootstrapStartDate(r.cfg.TradeBootstrapStartDate); !start.IsZero() {
			return start
		}
	}
	if domain == liveCaptureDomain {
		if start := parseBootstrapStartDate(r.cfg.LiveBootstrapStartDate); !start.IsZero() {
			return start
		}
	}
	return parseBootstrapStartDate(r.cfg.BootstrapStartDate)
}

func parseBootstrapStartDate(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	ts, err := parseTradeCursor(value)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func normalizeInstruments(items []Instrument) []Instrument {
	sort.Slice(items, func(i, j int) bool {
		if items[i].AssetType == items[j].AssetType {
			return items[i].Code < items[j].Code
		}
		return items[i].AssetType < items[j].AssetType
	})
	out := make([]Instrument, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Code == "" {
			continue
		}
		if _, ok := seen[item.Code]; ok {
			continue
		}
		seen[item.Code] = struct{}{}
		out = append(out, item)
	}
	return out
}

func quoteCaptureCodes(items []Instrument) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.AssetType != AssetTypeStock && item.AssetType != AssetTypeETF {
			continue
		}
		out = append(out, item.Code)
	}
	return out
}
