package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type runtimeBootstrapProvider struct {
	acceptanceProvider
	orders map[string]*OrderHistorySnapshot
}

func (p *runtimeBootstrapProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	if p.orders != nil {
		if item, ok := p.orders[query.Date]; ok {
			return item, nil
		}
	}
	return p.acceptanceProvider.OrderHistory(ctx, query)
}

func TestCollectorRuntimeStartupCatchUpAcrossDomains(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")

	makeRuntime := func(provider Provider, now time.Time) (*Store, *Runtime) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		runtime, err := NewRuntime(store, provider, RuntimeConfig{
			Now:          func() time.Time { return now },
			KlinePeriods: []KlinePeriod{PeriodDay},
			Metadata: MetadataConfig{
				CodesDBPath:   filepath.Join(tmp, "codes.db"),
				WorkdayDBPath: filepath.Join(tmp, "workday.db"),
			},
			Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
			Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
			OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
			Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
			Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
		})
		if err != nil {
			t.Fatalf("new collector runtime: %v", err)
		}
		return store, runtime
	}

	initial := &acceptanceProvider{
		instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
		tradingDays: []TradingDay{{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)}},
		quotes:      []QuoteSnapshot{{Code: "sh600000", Last: 12300, PreClose: 12200}},
		minutes:     []MinutePoint{{Code: "sh600000", Date: "20260331", Clock: "09:30", Price: 12300, Number: 10}},
		klines:      []KlineBar{newDayBar("sh600000", "20260331", 12200, 12300)},
		trades:      []TradeTick{newTradeTick("sh600000", "20260331", "09:30", 12300, 10, 0)},
		order:       &OrderHistorySnapshot{Code: "sh600000", Date: "20260331", Items: []OrderHistoryEntry{{Price: 12300, BuySellDelta: -20, Volume: 100}}},
		finance:     &FinanceSnapshot{Code: "sh600000", UpdatedDate: "20260331"},
		categories:  []F10Category{{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10}},
		contents:    map[string]string{"000001.txt": "浦发银行初始内容"},
	}

	store1, runtime1 := makeRuntime(initial, time.Date(2026, 3, 31, 15, 1, 0, 0, time.Local))
	if err := runtime1.RunStartupCatchUp(context.Background()); err != nil {
		t.Fatalf("initial startup catch-up: %v", err)
	}
	_ = store1.Close()

	catchUp := &acceptanceProvider{
		instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
		tradingDays: []TradingDay{
			{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
			{Date: "20260401", Time: time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local)},
		},
		quotes:     []QuoteSnapshot{{Code: "sh600000", Last: 12400, PreClose: 12300}},
		minutes:    []MinutePoint{{Code: "sh600000", Date: "20260401", Clock: "09:30", Price: 12400, Number: 11}},
		klines:     []KlineBar{newDayBar("sh600000", "20260401", 12300, 12400)},
		trades:     []TradeTick{newTradeTick("sh600000", "20260401", "09:30", 12400, 12, 0)},
		order:      &OrderHistorySnapshot{Code: "sh600000", Date: "20260401", Items: []OrderHistoryEntry{{Price: 12400, BuySellDelta: 10, Volume: 90}}},
		finance:    &FinanceSnapshot{Code: "sh600000", UpdatedDate: "20260401"},
		categories: []F10Category{{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10}},
		contents:   map[string]string{"000001.txt": "浦发银行更新内容"},
	}

	store2, runtime2 := makeRuntime(catchUp, time.Date(2026, 4, 1, 15, 1, 0, 0, time.Local))
	defer store2.Close()
	if err := runtime2.RunStartupCatchUp(context.Background()); err != nil {
		t.Fatalf("catch-up startup run: %v", err)
	}

	schedules := make([]ScheduleRunRecord, 0)
	if err := store2.engine.Where("ScheduleName = ?", "collector_startup_catchup").Asc("StartedAt").Find(&schedules); err != nil {
		t.Fatalf("load schedule runs: %v", err)
	}
	if len(schedules) != 2 {
		t.Fatalf("expected 2 startup catch-up schedule runs, got %d", len(schedules))
	}
	if schedules[1].Status != "passed" {
		t.Fatalf("expected second startup catch-up run to pass, got %+v", schedules[1])
	}
	if schedules[1].Details == "" {
		t.Fatalf("expected startup catch-up details to be recorded")
	}
}

func TestCollectorRuntimeStartupCatchUpBootstrapsAllTradingDays(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &runtimeBootstrapProvider{
		acceptanceProvider: acceptanceProvider{
			instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
			tradingDays: []TradingDay{
				{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
				{Date: "20260401", Time: time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local)},
			},
			quotes: []QuoteSnapshot{{Code: "sh600000", Last: 12400, PreClose: 12300}},
			minutes: []MinutePoint{
				{Code: "sh600000", Date: "20260331", Clock: "09:30", Price: 12300, Number: 10},
				{Code: "sh600000", Date: "20260401", Clock: "09:30", Price: 12400, Number: 11},
			},
			klines: []KlineBar{
				newDayBar("sh600000", "20260331", 12200, 12300),
				newDayBar("sh600000", "20260401", 12300, 12400),
			},
			trades: []TradeTick{
				newTradeTick("sh600000", "20260331", "09:30", 12300, 10, 0),
				newTradeTick("sh600000", "20260401", "09:30", 12400, 11, 0),
			},
			finance:    &FinanceSnapshot{Code: "sh600000", UpdatedDate: "20260401"},
			categories: []F10Category{{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10}},
			contents:   map[string]string{"000001.txt": "浦发银行更新内容"},
		},
		orders: map[string]*OrderHistorySnapshot{
			"20260331": {Code: "sh600000", Date: "20260331", Items: []OrderHistoryEntry{{Price: 12300, BuySellDelta: -20, Volume: 100}}},
			"20260401": {Code: "sh600000", Date: "20260401", Items: []OrderHistoryEntry{{Price: 12400, BuySellDelta: 10, Volume: 90}}},
		},
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 1, 20, 0, 0, 0, time.Local) },
		KlinePeriods: []KlinePeriod{PeriodDay},
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
	})
	if err != nil {
		t.Fatalf("new collector runtime: %v", err)
	}

	if err := runtime.RunStartupCatchUp(context.Background()); err != nil {
		t.Fatalf("startup catch-up: %v", err)
	}

	tradeEngine, err := openMetadataEngine(filepath.Join(tmp, "trade", "sh600000.db"))
	if err != nil {
		t.Fatalf("open trade engine: %v", err)
	}
	defer tradeEngine.Close()

	tradeCount, err := tradeEngine.Table("TradeHistory").Count(new(TradeHistoryRow))
	if err != nil {
		t.Fatalf("count trade rows: %v", err)
	}
	if tradeCount != 2 {
		t.Fatalf("expected bootstrap to backfill 2 trade rows, got %d", tradeCount)
	}

	orderEngine, err := openMetadataEngine(filepath.Join(tmp, "order_history", "sh600000.db"))
	if err != nil {
		t.Fatalf("open order-history engine: %v", err)
	}
	defer orderEngine.Close()

	orderCount, err := orderEngine.Table("OrderHistory").Count(new(OrderHistoryRow))
	if err != nil {
		t.Fatalf("count order-history rows: %v", err)
	}
	if orderCount != 2 {
		t.Fatalf("expected bootstrap to backfill 2 order-history rows, got %d", orderCount)
	}

	syncRuns := make([]ScheduleRunRecord, 0)
	if err := store.engine.Where("ScheduleName = ?", "collector_startup_catchup").Find(&syncRuns); err != nil {
		t.Fatalf("load startup runs: %v", err)
	}
	if len(syncRuns) != 1 {
		t.Fatalf("expected one startup schedule run, got %d", len(syncRuns))
	}
}

func TestCollectorRuntimeDailyFullSyncUsesDistinctScheduleName(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	runtime, err := NewRuntime(store, &acceptanceProvider{
		instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
		tradingDays: []TradingDay{{Date: "20260401", Time: time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local)}},
		quotes:      []QuoteSnapshot{{Code: "sh600000", Last: 12400, PreClose: 12300}},
		minutes:     []MinutePoint{{Code: "sh600000", Date: "20260401", Clock: "09:30", Price: 12400, Number: 11}},
		klines:      []KlineBar{newDayBar("sh600000", "20260401", 12300, 12400)},
		trades:      []TradeTick{newTradeTick("sh600000", "20260401", "09:30", 12400, 12, 0)},
		order:       &OrderHistorySnapshot{Code: "sh600000", Date: "20260401", Items: []OrderHistoryEntry{{Price: 12400, BuySellDelta: 10, Volume: 90}}},
		finance:     &FinanceSnapshot{Code: "sh600000", UpdatedDate: "20260401"},
		categories:  []F10Category{{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10}},
		contents:    map[string]string{"000001.txt": "浦发银行更新内容"},
	}, RuntimeConfig{
		Now:                   func() time.Time { return time.Date(2026, 4, 1, 18, 0, 0, 0, time.Local) },
		DailySyncScheduleName: "collector_daily_full_sync",
		KlinePeriods:          []KlinePeriod{PeriodDay},
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
	})
	if err != nil {
		t.Fatalf("new collector runtime: %v", err)
	}

	if err := runtime.RunDailyFullSync(context.Background()); err != nil {
		t.Fatalf("daily full sync: %v", err)
	}

	run, err := store.LatestScheduleRun("collector_daily_full_sync")
	if err != nil {
		t.Fatalf("latest daily full sync: %v", err)
	}
	if run == nil || run.Status != "passed" {
		t.Fatalf("expected passed collector_daily_full_sync run, got %+v", run)
	}
}

func TestCollectorRuntimeTradePendingDatesCanBackfillBeforeBootstrapStart(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	runtime, err := NewRuntime(store, &acceptanceProvider{}, RuntimeConfig{
		Now:                     func() time.Time { return time.Date(2026, 4, 11, 12, 0, 0, 0, time.Local) },
		TradeBootstrapStartDate: "20190101",
		KlinePeriods:            []KlinePeriod{PeriodDay},
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
	})
	if err != nil {
		t.Fatalf("new collector runtime: %v", err)
	}

	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     tradeHistoryDomain,
		AssetType:  string(AssetTypeStock),
		Instrument: "sh600000",
		Cursor:     "20200103",
	}); err != nil {
		t.Fatalf("seed latest trade cursor: %v", err)
	}
	if _, err := store.SeedTradeHistoryCoverageStarts("20190101"); err != nil {
		t.Fatalf("seed trade coverage-start cursor: %v", err)
	}

	runtime.cfg.TradeBootstrapStartDate = "20180101"
	dates, err := runtime.pendingTradingDates(tradeHistoryDomain, AssetTypeStock, "sh600000", []TradingDay{
		{Date: "20180102", Time: time.Date(2018, 1, 2, 15, 0, 0, 0, time.Local)},
		{Date: "20181228", Time: time.Date(2018, 12, 28, 15, 0, 0, 0, time.Local)},
		{Date: "20190102", Time: time.Date(2019, 1, 2, 15, 0, 0, 0, time.Local)},
		{Date: "20200106", Time: time.Date(2020, 1, 6, 15, 0, 0, 0, time.Local)},
	})
	if err != nil {
		t.Fatalf("pending trade dates: %v", err)
	}

	expected := []string{"20180102", "20181228", "20200106"}
	if len(dates) != len(expected) {
		t.Fatalf("unexpected pending trade date count: got %v want %v", dates, expected)
	}
	for i := range expected {
		if dates[i] != expected[i] {
			t.Fatalf("unexpected pending trade dates: got %v want %v", dates, expected)
		}
	}
}

func TestCollectorRuntimeLivePendingDatesCanBackfillBeforeBootstrapStart(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	runtime, err := NewRuntime(store, &acceptanceProvider{}, RuntimeConfig{
		Now:                    func() time.Time { return time.Date(2026, 4, 11, 12, 0, 0, 0, time.Local) },
		LiveBootstrapStartDate: "20190101",
		KlinePeriods:           []KlinePeriod{PeriodDay},
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
	})
	if err != nil {
		t.Fatalf("new collector runtime: %v", err)
	}

	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     liveCaptureDomain,
		AssetType:  string(AssetTypeStock),
		Instrument: "sh600000",
		Cursor:     "20200103",
	}); err != nil {
		t.Fatalf("seed latest live cursor: %v", err)
	}
	if _, err := store.SeedLiveCaptureCoverageStarts("20190101"); err != nil {
		t.Fatalf("seed live coverage-start cursor: %v", err)
	}

	runtime.cfg.LiveBootstrapStartDate = "20180101"
	dates, err := runtime.pendingTradingDates(liveCaptureDomain, AssetTypeStock, "sh600000", []TradingDay{
		{Date: "20180102", Time: time.Date(2018, 1, 2, 15, 0, 0, 0, time.Local)},
		{Date: "20181228", Time: time.Date(2018, 12, 28, 15, 0, 0, 0, time.Local)},
		{Date: "20190102", Time: time.Date(2019, 1, 2, 15, 0, 0, 0, time.Local)},
		{Date: "20200106", Time: time.Date(2020, 1, 6, 15, 0, 0, 0, time.Local)},
	})
	if err != nil {
		t.Fatalf("pending live dates: %v", err)
	}

	expected := []string{"20180102", "20181228", "20200106"}
	if len(dates) != len(expected) {
		t.Fatalf("unexpected pending live date count: got %v want %v", dates, expected)
	}
	for i := range expected {
		if dates[i] != expected[i] {
			t.Fatalf("unexpected pending live dates: got %v want %v", dates, expected)
		}
	}
}
