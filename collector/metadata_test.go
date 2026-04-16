package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type stubProvider struct {
	instruments []Instrument
	tradingDays []TradingDay
}

func (s *stubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return s.instruments, nil
}

func (s *stubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	return s.tradingDays, nil
}

func (s *stubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	for _, item := range s.tradingDays {
		if item.Date == day.Format("20060102") {
			return true, nil
		}
	}
	return false, nil
}

func (s *stubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestMetadataRefreshPublishesCodesAndWorkdays(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &stubProvider{
		instruments: []Instrument{
			{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock, Multiple: 100, Decimal: 2, LastPrice: 12340},
			{Code: "sh510300", Name: "沪深300ETF", Exchange: "sh", AssetType: AssetTypeETF, Multiple: 100, Decimal: 3, LastPrice: 4567},
			{Code: "sh000001", Name: "上证指数", Exchange: "sh", AssetType: AssetTypeIndex, Multiple: 100, Decimal: 2, LastPrice: 3200123},
		},
		tradingDays: []TradingDay{
			{Date: "20260330", Time: time.Date(2026, 3, 30, 15, 0, 0, 0, time.Local)},
			{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
		},
	}
	service, err := NewMetadataService(store, provider, MetadataConfig{
		CodesDBPath:   filepath.Join(tmp, "codes.db"),
		WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		Now: func() time.Time {
			return time.Date(2026, 4, 2, 3, 0, 0, 0, time.Local)
		},
	})
	if err != nil {
		t.Fatalf("new metadata service: %v", err)
	}

	if err := service.RefreshAll(context.Background()); err != nil {
		t.Fatalf("refresh all: %v", err)
	}

	codesCount := countRows[MetadataCodeRecord](t, filepath.Join(tmp, "codes.db"))
	if codesCount != 3 {
		t.Fatalf("expected 3 published codes, got %d", codesCount)
	}
	workdayCount := countRows[MetadataWorkdayRecord](t, filepath.Join(tmp, "workday.db"))
	if workdayCount != 2 {
		t.Fatalf("expected 2 published workdays, got %d", workdayCount)
	}

	cursor, err := store.GetCollectCursor("codes", MetadataAssetType, MetadataAllKey, "")
	if err != nil {
		t.Fatalf("codes cursor: %v", err)
	}
	if cursor == nil || cursor.Cursor == "" {
		t.Fatalf("expected persisted codes cursor")
	}
}

func TestMetadataRefreshIsReplaySafeAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	codesDB := filepath.Join(tmp, "codes.db")
	workdayDB := filepath.Join(tmp, "workday.db")

	makeService := func() (*Store, *MetadataService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		provider := &stubProvider{
			instruments: []Instrument{
				{Code: "sh600000", Name: "浦发银行", Exchange: "sh", AssetType: AssetTypeStock, Multiple: 100, Decimal: 2, LastPrice: 12340},
				{Code: "sz159915", Name: "创业板ETF", Exchange: "sz", AssetType: AssetTypeETF, Multiple: 100, Decimal: 3, LastPrice: 2789},
			},
			tradingDays: []TradingDay{
				{Date: "20260330", Time: time.Date(2026, 3, 30, 15, 0, 0, 0, time.Local)},
				{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
			},
		}
		service, err := NewMetadataService(store, provider, MetadataConfig{
			CodesDBPath:   codesDB,
			WorkdayDBPath: workdayDB,
			Now: func() time.Time {
				return time.Date(2026, 4, 2, 3, 5, 0, 0, time.Local)
			},
		})
		if err != nil {
			t.Fatalf("new metadata service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService()
	if err := service1.RefreshAll(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService()
	defer store2.Close()
	if err := service2.RefreshAll(context.Background()); err != nil {
		t.Fatalf("second refresh after restart: %v", err)
	}

	if got := countRows[MetadataCodeRecord](t, codesDB); got != 2 {
		t.Fatalf("expected 2 published codes after replay, got %d", got)
	}
	if got := countRows[MetadataWorkdayRecord](t, workdayDB); got != 2 {
		t.Fatalf("expected 2 workdays after replay, got %d", got)
	}

	cursor, err := store2.GetCollectCursor("workday", MetadataAssetType, MetadataAllKey, "")
	if err != nil {
		t.Fatalf("workday cursor: %v", err)
	}
	if cursor == nil || cursor.Cursor != "20260331" {
		t.Fatalf("unexpected workday cursor after replay: %#v", cursor)
	}
}

func countRows[T any](t *testing.T, filename string) int64 {
	t.Helper()
	engine, err := openMetadataEngine(filename)
	if err != nil {
		t.Fatalf("open metadata engine %s: %v", filename, err)
	}
	defer engine.Close()

	var bean T
	count, err := engine.Count(&bean)
	if err != nil {
		t.Fatalf("count rows %s: %v", filename, err)
	}
	return count
}
