package collector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

type RuntimeConfig struct {
	ScheduleName     string
	Now              func() time.Time
	CalendarLookback int
	KlinePeriods     []KlinePeriod
	Metadata         MetadataConfig
	Kline            KlineConfig
	Trade            TradeConfig
	OrderHistory     OrderHistoryConfig
	Live             LiveCaptureConfig
	Fundamentals     FundamentalsConfig
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
	if cfg.CalendarLookback <= 0 {
		cfg.CalendarLookback = 30
	}
	if len(cfg.KlinePeriods) == 0 {
		cfg.KlinePeriods = []KlinePeriod{PeriodDay}
	}

	wireRuntimeConfig(&cfg)

	metadata, err := NewMetadataService(store, provider, cfg.Metadata)
	if err != nil {
		return nil, err
	}
	kline, err := NewKlineService(store, provider, cfg.Kline)
	if err != nil {
		return nil, err
	}
	trade, err := NewTradeService(store, provider, cfg.Trade)
	if err != nil {
		return nil, err
	}
	orderHistory, err := NewOrderHistoryService(store, provider, cfg.OrderHistory)
	if err != nil {
		return nil, err
	}
	live, err := NewLiveCaptureService(store, provider, cfg.Live)
	if err != nil {
		return nil, err
	}
	fundamentals, err := NewFundamentalsService(store, provider, cfg.Fundamentals)
	if err != nil {
		return nil, err
	}

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
	}, nil
}

func (r *Runtime) RunStartupCatchUp(ctx context.Context) (err error) {
	run := &ScheduleRunRecord{
		ScheduleName: r.cfg.ScheduleName,
		Status:       "running",
		StartedAt:    r.cfg.Now(),
	}
	if err := r.store.AddScheduleRun(run); err != nil {
		return err
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

	if err := r.metadata.RefreshAll(ctx); err != nil {
		return err
	}

	instruments, err := r.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock, AssetTypeETF, AssetTypeIndex},
	})
	if err != nil {
		return err
	}
	instruments = normalizeInstruments(instruments)

	tradingDays, err := r.loadTradingDays(ctx, instruments)
	if err != nil {
		return err
	}

	quoteCodes := quoteCaptureCodes(instruments)
	if len(quoteCodes) > 0 {
		trading, err := r.provider.IsTradingDay(ctx, r.cfg.Now())
		if err != nil {
			return err
		}
		if trading {
			if err := r.live.CaptureQuotes(ctx, QuoteCaptureQuery{
				Codes:       quoteCodes,
				CaptureTime: r.cfg.Now(),
			}); err != nil {
				return err
			}
		}
	}

	var klineRuns int
	var tradeRuns int
	var orderRuns int
	var liveRuns int
	var financeRuns int
	var f10Runs int

	for _, instrument := range instruments {
		for _, period := range r.cfg.KlinePeriods {
			if err := r.kline.Refresh(ctx, KlineCollectQuery{
				Code:      instrument.Code,
				AssetType: instrument.AssetType,
				Period:    period,
			}); err != nil {
				return err
			}
			klineRuns++
		}

		if instrument.AssetType == AssetTypeStock || instrument.AssetType == AssetTypeETF {
			dates, err := r.pendingTradingDates("trade_history", instrument.AssetType, instrument.Code, tradingDays)
			if err != nil {
				return err
			}
			for _, date := range dates {
				if err := r.trade.RefreshDay(ctx, TradeCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					return err
				}
				tradeRuns++
			}

			dates, err = r.pendingTradingDates("live_capture", instrument.AssetType, instrument.Code, tradingDays)
			if err != nil {
				return err
			}
			for _, date := range dates {
				if err := r.live.ReconcileDay(ctx, SessionCaptureQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					return err
				}
				liveRuns++
			}
		}

		if instrument.AssetType == AssetTypeStock {
			dates, err := r.pendingTradingDates("order_history", instrument.AssetType, instrument.Code, tradingDays)
			if err != nil {
				return err
			}
			for _, date := range dates {
				if err := r.orderHistory.RefreshDay(ctx, OrderHistoryCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					return err
				}
				orderRuns++
			}

			if err := r.fundamentals.RefreshFinance(ctx, instrument.Code); err != nil {
				return err
			}
			financeRuns++
			if err := r.fundamentals.SyncF10(ctx, instrument.Code); err != nil {
				return err
			}
			f10Runs++
		}
	}

	details = fmt.Sprintf(
		"instruments=%d trading_days=%d kline_runs=%d trade_runs=%d order_runs=%d live_runs=%d finance_runs=%d f10_runs=%d quote_codes=%d",
		len(instruments), len(tradingDays), klineRuns, tradeRuns, orderRuns, liveRuns, financeRuns, f10Runs, len(quoteCodes),
	)
	return r.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("runtime-%d", time.Now().UnixNano()),
		PhaseID:     "phase_7",
		SuiteName:   "collector_startup_catchup",
		Status:      "passed",
		Blocking:    true,
		CommandText: "collector runtime startup catch-up",
		OutputText:  details,
	})
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
}

func (r *Runtime) loadTradingDays(ctx context.Context, instruments []Instrument) ([]TradingDay, error) {
	start := r.cfg.Now().AddDate(0, 0, -r.cfg.CalendarLookback)
	end := r.cfg.Now().Add(24 * time.Hour)
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
	cursor, err := r.store.GetCollectCursor(domain, string(assetType), code, "")
	if err != nil {
		return nil, err
	}
	if cursor == nil || cursor.Cursor == "" {
		return []string{tradingDays[len(tradingDays)-1].Date}, nil
	}

	out := make([]string, 0, len(tradingDays))
	for _, day := range tradingDays {
		if tradeDateAfter(day.Date, cursor.Cursor) {
			out = append(out, day.Date)
		}
	}
	return out, nil
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
