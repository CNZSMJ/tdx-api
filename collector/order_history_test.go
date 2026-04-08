package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type orderHistoryStubProvider struct {
	snapshot *OrderHistorySnapshot
}

func (s *orderHistoryStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	if s.snapshot != nil && s.snapshot.Code == query.Code && s.snapshot.Date == query.Date {
		return s.snapshot, nil
	}
	return &OrderHistorySnapshot{Code: query.Code, Date: query.Date}, nil
}

func (s *orderHistoryStubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return nil, errors.New("not implemented")
}

func (s *orderHistoryStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestOrderHistoryRefreshPublishesDBFirstAndPersistsCursor(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewOrderHistoryService(store, &orderHistoryStubProvider{
		snapshot: &OrderHistorySnapshot{
			Code: "sh600000",
			Date: "20260331",
			Items: []OrderHistoryEntry{
				{Price: 12000, BuySellDelta: -20, Volume: 100},
				{Price: 12100, BuySellDelta: 15, Volume: 80},
			},
		},
	}, OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")})
	if err != nil {
		t.Fatalf("new order history service: %v", err)
	}

	if err := service.RefreshDay(context.Background(), OrderHistoryCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("refresh order history: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(tmp, "order_history", "sh600000.db"))
	if err != nil {
		t.Fatalf("open order history db: %v", err)
	}
	defer engine.Close()

	rows := make([]OrderHistoryRow, 0)
	if err := engine.Table("OrderHistory").Where("TradeDate = ?", "20260331").Asc("Seq").Find(&rows); err != nil {
		t.Fatalf("load order history rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 order history rows, got %d", len(rows))
	}

	cursor, err := store.GetCollectCursor("order_history", string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("order history cursor: %v", err)
	}
	if cursor == nil || cursor.Cursor != "20260331" {
		t.Fatalf("unexpected order history cursor: %#v", cursor)
	}
}

func TestOrderHistoryReplayPreservesRawDeltaValues(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	baseDir := filepath.Join(tmp, "order_history")

	makeService := func(snapshot *OrderHistorySnapshot) (*Store, *OrderHistoryService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewOrderHistoryService(store, &orderHistoryStubProvider{snapshot: snapshot}, OrderHistoryConfig{
			BaseDir: baseDir,
		})
		if err != nil {
			t.Fatalf("new order history service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService(&OrderHistorySnapshot{
		Code: "sh600000",
		Date: "20260331",
		Items: []OrderHistoryEntry{
			{Price: 12000, BuySellDelta: -20, Volume: 100},
			{Price: 12100, BuySellDelta: 15, Volume: 80},
		},
	})
	if err := service1.RefreshDay(context.Background(), OrderHistoryCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService(&OrderHistorySnapshot{
		Code: "sh600000",
		Date: "20260331",
		Items: []OrderHistoryEntry{
			{Price: 12000, BuySellDelta: -25, Volume: 100},
			{Price: 12100, BuySellDelta: 18, Volume: 90},
		},
	})
	defer store2.Close()
	if err := service2.RefreshDay(context.Background(), OrderHistoryCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(baseDir, "sh600000.db"))
	if err != nil {
		t.Fatalf("open order history db: %v", err)
	}
	defer engine.Close()

	rows := make([]OrderHistoryRow, 0)
	if err := engine.Table("OrderHistory").Where("TradeDate = ?", "20260331").Asc("Seq").Find(&rows); err != nil {
		t.Fatalf("load order history rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected replay-safe 2 order history rows, got %d", len(rows))
	}
	if rows[0].BuySellDelta != -25 || rows[1].BuySellDelta != 18 {
		t.Fatalf("expected raw BuySellDelta values to be preserved, got %+v", rows)
	}
}
