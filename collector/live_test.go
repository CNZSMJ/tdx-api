package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type liveStubProvider struct {
	quotes  []QuoteSnapshot
	minutes []MinutePoint
	trades  []TradeTick
}

func (s *liveStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

func (s *liveStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return s.quotes, nil
}

func (s *liveStubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return s.minutes, nil
}

func (s *liveStubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	return s.trades, nil
}

func (s *liveStubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return nil, errors.New("not implemented")
}

func (s *liveStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestLiveCaptureStoresQuotesAndSessionData(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewLiveCaptureService(store, &liveStubProvider{
		quotes: []QuoteSnapshot{
			{Code: "sh600000", Last: 12300, PreClose: 12200, Open: 12250, High: 12350, Low: 12200, VolumeHand: 1000, AmountYuan: 123456},
		},
		minutes: []MinutePoint{
			{Code: "sh600000", Date: "20260331", Clock: "09:30", Price: 12300, Number: 10},
			{Code: "sh600000", Date: "20260331", Clock: "09:31", Price: 12310, Number: 12},
		},
		trades: []TradeTick{
			newTradeTick("sh600000", "20260331", "09:30", 12300, 10, 0),
		},
	}, LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")})
	if err != nil {
		t.Fatalf("new live capture service: %v", err)
	}

	if err := service.CaptureQuotes(context.Background(), QuoteCaptureQuery{
		Codes:       []string{"sh600000"},
		CaptureTime: time.Date(2026, 3, 31, 9, 31, 0, 0, time.Local),
	}); err != nil {
		t.Fatalf("capture quotes: %v", err)
	}
	if err := service.CaptureSession(context.Background(), SessionCaptureQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("capture session: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(tmp, "live", "quotes.db"))
	if err != nil {
		t.Fatalf("open quote db: %v", err)
	}
	defer engine.Close()
	quoteRows := make([]QuoteSnapshotRow, 0)
	if err := engine.Table("QuoteSnapshot").Find(&quoteRows); err != nil {
		t.Fatalf("load quote rows: %v", err)
	}
	if len(quoteRows) != 1 {
		t.Fatalf("expected 1 quote snapshot row, got %d", len(quoteRows))
	}
}

func TestLiveCaptureReplayAndReconcileAreSafe(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	baseDir := filepath.Join(tmp, "live")

	makeService := func(minutes []MinutePoint, trades []TradeTick) (*Store, *LiveCaptureService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewLiveCaptureService(store, &liveStubProvider{
			minutes: minutes,
			trades:  trades,
		}, LiveCaptureConfig{BaseDir: baseDir})
		if err != nil {
			t.Fatalf("new live capture service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService(
		[]MinutePoint{{Code: "sh600000", Date: "20260331", Clock: "09:30", Price: 12300, Number: 10}},
		[]TradeTick{newTradeTick("sh600000", "20260331", "09:30", 12300, 10, 0)},
	)
	if err := service1.CaptureSession(context.Background(), SessionCaptureQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("first live capture: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService(
		[]MinutePoint{
			{Code: "sh600000", Date: "20260331", Clock: "09:30", Price: 12300, Number: 10},
			{Code: "sh600000", Date: "20260331", Clock: "09:31", Price: 12320, Number: 12},
		},
		[]TradeTick{
			newTradeTick("sh600000", "20260331", "09:30", 12300, 10, 0),
			newTradeTick("sh600000", "20260331", "09:31", 12320, 20, 1),
		},
	)
	defer store2.Close()
	if err := service2.ReconcileDay(context.Background(), SessionCaptureQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("reconcile day: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(baseDir, "sh600000.db"))
	if err != nil {
		t.Fatalf("open live code db: %v", err)
	}
	defer engine.Close()

	minuteRows := make([]MinuteLiveRow, 0)
	if err := engine.Table("MinuteLive").Where("TradeDate = ?", "20260331").Asc("Clock").Find(&minuteRows); err != nil {
		t.Fatalf("load live minute rows: %v", err)
	}
	if len(minuteRows) != 2 || minuteRows[1].Price != 12320 {
		t.Fatalf("expected reconciled live minute rows, got %+v", minuteRows)
	}

	tradeRows := make([]TradeLiveRow, 0)
	if err := engine.Table("TradeLive").Where("TradeDate = ?", "20260331").Asc("Seq").Find(&tradeRows); err != nil {
		t.Fatalf("load live trade rows: %v", err)
	}
	if len(tradeRows) != 2 || tradeRows[1].Price != 12320 {
		t.Fatalf("expected reconciled live trade rows, got %+v", tradeRows)
	}
}
