package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeResolveRecentTradingDatesUsesLatestTwoTradingDays(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &stubProvider{
		tradingDays: []TradingDay{
			{Date: "20260415", Time: time.Date(2026, 4, 15, 15, 0, 0, 0, time.Local)},
			{Date: "20260416", Time: time.Date(2026, 4, 16, 15, 0, 0, 0, time.Local)},
			{Date: "20260417", Time: time.Date(2026, 4, 17, 15, 0, 0, 0, time.Local)},
			{Date: "20260420", Time: time.Date(2026, 4, 20, 15, 0, 0, 0, time.Local)},
		},
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now: func() time.Time { return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local) },
		Metadata: MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	dates, err := runtime.ResolveRecentTradingDates(context.Background(), 2)
	if err != nil {
		t.Fatalf("resolve recent trading dates: %v", err)
	}
	want := []string{"20260417", "20260420"}
	if len(dates) != len(want) {
		t.Fatalf("dates = %+v, want %+v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("dates = %+v, want %+v", dates, want)
		}
	}
}

type closeSyncRecordingProvider struct {
	acceptanceProvider
	tradeDates       []string
	liveDates        []string
	orderDates       []string
	klineDates       []string
	financeCalls     int
	f10CategoryCalls int
	f10ContentCalls  int
	failTrade        string
}

func (p *closeSyncRecordingProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	p.tradeDates = append(p.tradeDates, query.Date)
	if query.Date == p.failTrade {
		return nil, errors.New("trade timeout")
	}
	return p.acceptanceProvider.TradeHistory(ctx, query)
}

func (p *closeSyncRecordingProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	p.liveDates = append(p.liveDates, query.Date)
	return p.acceptanceProvider.Minutes(ctx, query)
}

func (p *closeSyncRecordingProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	p.klineDates = append(p.klineDates, query.Since.Format("20060102"))
	return p.acceptanceProvider.Klines(ctx, query)
}

func (p *closeSyncRecordingProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	p.orderDates = append(p.orderDates, query.Date)
	return &OrderHistorySnapshot{
		Code: query.Code,
		Date: query.Date,
		Items: []OrderHistoryEntry{
			{Price: 12300, BuySellDelta: 10, Volume: 90},
		},
	}, nil
}

func (p *closeSyncRecordingProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	p.financeCalls++
	return p.acceptanceProvider.Finance(ctx, code)
}

func (p *closeSyncRecordingProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	p.f10CategoryCalls++
	return p.acceptanceProvider.F10Categories(ctx, code)
}

func (p *closeSyncRecordingProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	p.f10ContentCalls++
	return p.acceptanceProvider.F10Content(ctx, query)
}

func TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &closeSyncRecordingProvider{
		acceptanceProvider: acceptanceProvider{
			instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
			tradingDays: []TradingDay{
				{Date: "20260417", Time: time.Date(2026, 4, 17, 15, 0, 0, 0, time.Local)},
				{Date: "20260420", Time: time.Date(2026, 4, 20, 15, 0, 0, 0, time.Local)},
			},
			minutes: []MinutePoint{
				{Code: "sh600000", Date: "20260417", Clock: "09:30", Price: 12300, Number: 10},
				{Code: "sh600000", Date: "20260420", Clock: "09:30", Price: 12400, Number: 11},
			},
			klines: []KlineBar{
				newDayBar("sh600000", "20260417", 12200, 12300),
				newDayBar("sh600000", "20260420", 12300, 12400),
			},
			trades: []TradeTick{
				newTradeTick("sh600000", "20260417", "09:30", 12300, 10, 0),
				newTradeTick("sh600000", "20260420", "09:30", 12400, 11, 0),
			},
			finance: &FinanceSnapshot{
				Code:        "sh600000",
				UpdatedDate: "20260420",
			},
		},
		failTrade: "20260420",
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local) },
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
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	failures, err := runtime.ExecuteDailyCloseSync(context.Background(), []string{"20260417", "20260420"})
	if err != nil {
		t.Fatalf("execute daily close sync: %v", err)
	}

	wantDates := []string{"20260417", "20260420"}
	assertDateSet := func(label string, got []string) {
		t.Helper()
		seen := make(map[string]struct{}, len(got))
		for _, date := range got {
			seen[date] = struct{}{}
		}
		if len(seen) != len(wantDates) {
			t.Fatalf("%s dates = %v, want unique set %v", label, got, wantDates)
		}
		for _, want := range wantDates {
			if _, ok := seen[want]; !ok {
				t.Fatalf("%s dates = %v, want unique set %v", label, got, wantDates)
			}
		}
	}
	assertDateSet("trade", provider.tradeDates)
	assertDateSet("live", provider.liveDates)
	assertDateSet("order", provider.orderDates)
	if len(failures) != 2 {
		t.Fatalf("failures = %+v, want trade_history + live_capture failures", failures)
	}
	gotDomains := map[string]bool{
		failures[0].Domain: true,
		failures[1].Domain: true,
	}
	if !gotDomains["trade_history"] || !gotDomains["live_capture"] {
		t.Fatalf("unexpected failure payloads: %+v", failures)
	}
}

func TestRuntimeRepairCloseSyncFailureTargetsSingleDomainWindow(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &closeSyncRecordingProvider{
		acceptanceProvider: acceptanceProvider{
			instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
			tradingDays: []TradingDay{
				{Date: "20260420", Time: time.Date(2026, 4, 20, 15, 0, 0, 0, time.Local)},
			},
			minutes: []MinutePoint{
				{Code: "sh600000", Date: "20260420", Clock: "09:30", Price: 12400, Number: 11},
			},
			klines: []KlineBar{
				newDayBar("sh600000", "20260420", 12300, 12400),
			},
			trades: []TradeTick{
				newTradeTick("sh600000", "20260420", "09:30", 12400, 11, 0),
			},
		},
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local) },
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
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	cases := []struct {
		name      string
		failure   CloseSyncFailure
		wantTrade int
		wantLive  int
		wantOrder int
		wantKline int
	}{
		{
			name:      "trade_history",
			failure:   CloseSyncFailure{Domain: "trade_history", Date: "20260420", Instrument: "sh600000"},
			wantTrade: 1,
		},
		{
			name:      "live_capture",
			failure:   CloseSyncFailure{Domain: "live_capture", Date: "20260420", Instrument: "sh600000"},
			wantTrade: 1,
			wantLive:  1,
		},
		{
			name:      "order_history",
			failure:   CloseSyncFailure{Domain: "order_history", Date: "20260420", Instrument: "sh600000"},
			wantOrder: 1,
		},
		{
			name:      "kline",
			failure:   CloseSyncFailure{Domain: "kline", Date: "20260420", Instrument: "sh600000", Period: PeriodDay},
			wantKline: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			provider.tradeDates = nil
			provider.liveDates = nil
			provider.orderDates = nil
			provider.klineDates = nil

			if err := runtime.RepairCloseSyncFailure(context.Background(), tc.failure); err != nil {
				t.Fatalf("repair close sync failure: %v", err)
			}

			if len(provider.tradeDates) != tc.wantTrade {
				t.Fatalf("trade dates = %v, want count %d", provider.tradeDates, tc.wantTrade)
			}
			if len(provider.liveDates) != tc.wantLive {
				t.Fatalf("live dates = %v, want count %d", provider.liveDates, tc.wantLive)
			}
			if len(provider.orderDates) != tc.wantOrder {
				t.Fatalf("order dates = %v, want count %d", provider.orderDates, tc.wantOrder)
			}
			if len(provider.klineDates) != tc.wantKline {
				t.Fatalf("kline dates = %v, want count %d", provider.klineDates, tc.wantKline)
			}
		})
	}
}

func TestRuntimeExecuteDailyCloseSyncIncrementallyRefreshesFundamentalsDomains(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &closeSyncRecordingProvider{
		acceptanceProvider: acceptanceProvider{
			instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
			tradingDays: []TradingDay{
				{Date: "20260420", Time: time.Date(2026, 4, 20, 15, 0, 0, 0, time.Local)},
			},
			minutes: []MinutePoint{
				{Code: "sh600000", Date: "20260420", Clock: "09:30", Price: 12400, Number: 11},
			},
			klines: []KlineBar{
				newDayBar("sh600000", "20260420", 12300, 12400),
			},
			trades: []TradeTick{
				newTradeTick("sh600000", "20260420", "09:30", 12400, 11, 0),
			},
			finance: &FinanceSnapshot{
				Code:        "sh600000",
				UpdatedDate: "20260420",
			},
			categories: []F10Category{
				{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10},
			},
			contents: map[string]string{
				"000001.txt": "浦发银行股份有限公司",
			},
		},
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local) },
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
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "finance",
		AssetType:  MetadataAssetType,
		Instrument: "sh600000",
		Cursor:     "20260331",
	}); err != nil {
		t.Fatalf("seed finance cursor: %v", err)
	}
	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "f10",
		AssetType:  MetadataAssetType,
		Instrument: "sh600000",
		Cursor:     "stale-f10-signature",
	}); err != nil {
		t.Fatalf("seed f10 cursor: %v", err)
	}

	failures, err := runtime.ExecuteDailyCloseSync(context.Background(), []string{"20260420"})
	if err != nil {
		t.Fatalf("execute daily close sync: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if provider.financeCalls != 1 {
		t.Fatalf("finance calls = %d, want 1", provider.financeCalls)
	}
	if provider.f10CategoryCalls != 1 {
		t.Fatalf("f10 category calls = %d, want 1", provider.f10CategoryCalls)
	}
	if provider.f10ContentCalls != 1 {
		t.Fatalf("f10 content calls = %d, want 1", provider.f10ContentCalls)
	}

	financeCursor, err := store.GetCollectCursor("finance", MetadataAssetType, "sh600000", "")
	if err != nil {
		t.Fatalf("get finance cursor: %v", err)
	}
	if financeCursor == nil || financeCursor.Cursor != "20260420" {
		t.Fatalf("finance cursor = %+v, want updated_date 20260420", financeCursor)
	}

	f10Cursor, err := store.GetCollectCursor("f10", MetadataAssetType, "sh600000", "")
	if err != nil {
		t.Fatalf("get f10 cursor: %v", err)
	}
	if f10Cursor == nil || f10Cursor.Cursor == "" || f10Cursor.Cursor == "stale-f10-signature" {
		t.Fatalf("f10 cursor = %+v, want refreshed signature", f10Cursor)
	}
}

func TestRuntimeExecuteDailyCloseSyncSkipsUnchangedFundamentalsArtifacts(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	categories := []F10Category{
		{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10},
	}
	provider := &closeSyncRecordingProvider{
		acceptanceProvider: acceptanceProvider{
			instruments: []Instrument{{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock}},
			tradingDays: []TradingDay{
				{Date: "20260420", Time: time.Date(2026, 4, 20, 15, 0, 0, 0, time.Local)},
			},
			minutes: []MinutePoint{
				{Code: "sh600000", Date: "20260420", Clock: "09:30", Price: 12400, Number: 11},
			},
			klines: []KlineBar{
				newDayBar("sh600000", "20260420", 12300, 12400),
			},
			trades: []TradeTick{
				newTradeTick("sh600000", "20260420", "09:30", 12400, 11, 0),
			},
			finance: &FinanceSnapshot{
				Code:        "sh600000",
				UpdatedDate: "20260420",
			},
			categories: categories,
			contents: map[string]string{
				"000001.txt": "浦发银行股份有限公司",
			},
		},
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 20, 18, 0, 0, 0, time.Local) },
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
		Block:        BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "finance",
		AssetType:  MetadataAssetType,
		Instrument: "sh600000",
		Cursor:     "20260420",
	}); err != nil {
		t.Fatalf("seed finance cursor: %v", err)
	}
	if err := store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "f10",
		AssetType:  MetadataAssetType,
		Instrument: "sh600000",
		Cursor:     f10CategorySignature(categories),
	}); err != nil {
		t.Fatalf("seed f10 cursor: %v", err)
	}

	failures, err := runtime.ExecuteDailyCloseSync(context.Background(), []string{"20260420"})
	if err != nil {
		t.Fatalf("execute daily close sync: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if provider.financeCalls != 1 {
		t.Fatalf("finance calls = %d, want 1", provider.financeCalls)
	}
	if provider.f10CategoryCalls != 1 {
		t.Fatalf("f10 category calls = %d, want 1", provider.f10CategoryCalls)
	}
	if provider.f10ContentCalls != 0 {
		t.Fatalf("f10 content calls = %d, want 0", provider.f10ContentCalls)
	}
}
