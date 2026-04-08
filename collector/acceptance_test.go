package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type acceptanceProvider struct {
	instruments []Instrument
	tradingDays []TradingDay
	quotes      []QuoteSnapshot
	minutes     []MinutePoint
	klines      []KlineBar
	trades      []TradeTick
	order       *OrderHistorySnapshot
	finance     *FinanceSnapshot
	categories  []F10Category
	contents    map[string]string
}

func (p *acceptanceProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	if len(query.AssetTypes) == 0 {
		return p.instruments, nil
	}
	allowed := make(map[AssetType]struct{}, len(query.AssetTypes))
	for _, item := range query.AssetTypes {
		allowed[item] = struct{}{}
	}
	out := make([]Instrument, 0, len(p.instruments))
	for _, item := range p.instruments {
		if _, ok := allowed[item.AssetType]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (p *acceptanceProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	out := make([]TradingDay, 0, len(p.tradingDays))
	for _, item := range p.tradingDays {
		if !query.Start.IsZero() && item.Time.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && item.Time.After(query.End) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (p *acceptanceProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	target := day.Format("20060102")
	for _, item := range p.tradingDays {
		if item.Date == target {
			return true, nil
		}
	}
	return false, nil
}

func (p *acceptanceProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return p.quotes, nil
}

func (p *acceptanceProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return p.minutes, nil
}

func (p *acceptanceProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	out := make([]KlineBar, 0, len(p.klines))
	for _, item := range p.klines {
		if item.Code != query.Code || item.Period != query.Period {
			continue
		}
		if !query.Since.IsZero() && !item.Time.After(query.Since) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (p *acceptanceProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	out := make([]TradeTick, 0, len(p.trades))
	for _, item := range p.trades {
		if item.Code == query.Code && item.Time.Format("20060102") == query.Date {
			out = append(out, item)
		}
	}
	return out, nil
}

func (p *acceptanceProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	if p.order != nil && p.order.Code == query.Code && p.order.Date == query.Date {
		return p.order, nil
	}
	return nil, errors.New("order history not found")
}

func (p *acceptanceProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return p.finance, nil
}

func (p *acceptanceProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return p.categories, nil
}

func (p *acceptanceProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return &F10Content{
		Code:     query.Code,
		Filename: query.Filename,
		Start:    query.Start,
		Length:   query.Length,
		Content:  p.contents[query.Filename],
	}, nil
}

func (p *acceptanceProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, nil
}

func TestCollectorFinalAcceptanceEndToEndCatchUp(t *testing.T) {
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

	required := []struct {
		domain     string
		assetType  string
		instrument string
		period     string
	}{
		{"codes", MetadataAssetType, MetadataAllKey, ""},
		{"workday", MetadataAssetType, MetadataAllKey, ""},
		{"kline", string(AssetTypeStock), "sh600000", string(PeriodDay)},
		{"trade_history", string(AssetTypeStock), "sh600000", ""},
		{"order_history", string(AssetTypeStock), "sh600000", ""},
		{"live_capture", string(AssetTypeStock), "sh600000", ""},
		{"finance", MetadataAssetType, "sh600000", ""},
		{"f10", MetadataAssetType, "sh600000", ""},
	}
	for _, item := range required {
		cursor, err := store2.GetCollectCursor(item.domain, item.assetType, item.instrument, item.period)
		if err != nil {
			t.Fatalf("cursor lookup %s: %v", item.domain, err)
		}
		if cursor == nil || cursor.Cursor == "" {
			t.Fatalf("expected non-empty cursor for domain %s", item.domain)
		}
	}
}
