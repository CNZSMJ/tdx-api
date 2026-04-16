package collector

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type tradeStubProvider struct {
	items []TradeTick
}

func (s *tradeStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

func (s *tradeStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	out := make([]TradeTick, 0, len(s.items))
	for _, item := range s.items {
		if item.Code == query.Code && item.Time.Format("20060102") == query.Date {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *tradeStubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return nil, errors.New("not implemented")
}

func (s *tradeStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestTradeRefreshPublishesDBFirstAndPersistsCursor(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewTradeService(store, &tradeStubProvider{
		items: []TradeTick{
			newTradeTick("sh600000", "20260331", "09:30", 12000, 10, 0),
			newTradeTick("sh600000", "20260331", "09:31", 12100, 20, 1),
		},
	}, TradeConfig{BaseDir: filepath.Join(tmp, "trade")})
	if err != nil {
		t.Fatalf("new trade service: %v", err)
	}

	if err := service.RefreshDay(context.Background(), TradeCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("refresh day: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(tmp, "trade", "sh600000.db"))
	if err != nil {
		t.Fatalf("open trade db: %v", err)
	}
	defer engine.Close()

	rawRows := make([]TradeHistoryRow, 0)
	if err := loadTradeRows(engine, "TradeHistory", "20260331", &rawRows); err != nil {
		t.Fatalf("load raw trade rows: %v", err)
	}
	if len(rawRows) != 2 {
		t.Fatalf("expected 2 raw trade rows, got %d", len(rawRows))
	}
	cursor, err := store.GetCollectCursor("trade_history", string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("trade cursor: %v", err)
	}
	if cursor == nil || cursor.Cursor != "20260331" {
		t.Fatalf("unexpected trade cursor: %#v", cursor)
	}
}

func TestTradeRefreshIsReplaySafeAndDerivedBarsAreReproducible(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	tradeDir := filepath.Join(tmp, "trade")

	makeService := func(items []TradeTick) (*Store, *TradeService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewTradeService(store, &tradeStubProvider{items: items}, TradeConfig{
			BaseDir: tradeDir,
		})
		if err != nil {
			t.Fatalf("new trade service: %v", err)
		}
		return store, service
	}

	first := []TradeTick{
		newTradeTick("sh600000", "20260331", "09:30", 12000, 10, 0),
		newTradeTick("sh600000", "20260331", "09:31", 12100, 20, 1),
	}
	store1, service1 := makeService(first)
	if err := service1.RefreshDay(context.Background(), TradeCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	second := []TradeTick{
		newTradeTick("sh600000", "20260331", "09:30", 12000, 10, 0),
		newTradeTick("sh600000", "20260331", "09:31", 12200, 30, 1),
	}
	store2, service2 := makeService(second)
	defer store2.Close()
	if err := service2.RefreshDay(context.Background(), TradeCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20260331",
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(tradeDir, "sh600000.db"))
	if err != nil {
		t.Fatalf("open trade db: %v", err)
	}
	defer engine.Close()

	rawRows := make([]TradeHistoryRow, 0)
	if err := loadTradeRows(engine, "TradeHistory", "20260331", &rawRows); err != nil {
		t.Fatalf("load raw trade rows: %v", err)
	}
	if len(rawRows) != 2 {
		t.Fatalf("expected replay-safe 2 raw trade rows, got %d", len(rawRows))
	}
	if rawRows[1].Price != 12200 || rawRows[1].VolumeHand != 30 {
		t.Fatalf("expected replayed raw trade row to be replaced, got %+v", rawRows[1])
	}

	publishedBars := make([]TradeBarRow, 0)
	if err := engine.Table("TradeMinute1Bar").Where("TradeDate = ?", "20260331").Asc("BucketTime").Find(&publishedBars); err != nil {
		t.Fatalf("load published trade bars: %v", err)
	}

	derived := deriveTradeBars(copyTradeRows(rawRows))
	if !reflect.DeepEqual(stripTradeBarInDate(publishedBars), stripTradeBarPtrs(derived[1])) {
		t.Fatalf("published minute bars are not reproducible from raw trades")
	}
}

func TestTradeRefreshKeepsLatestCursorAndTracksCoverageStart(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewTradeService(store, &tradeStubProvider{
		items: []TradeTick{
			newTradeTick("sh600000", "20200102", "09:30", 12000, 10, 0),
			newTradeTick("sh600000", "20181228", "09:30", 11900, 8, 0),
		},
	}, TradeConfig{
		BaseDir:            filepath.Join(tmp, "trade"),
		BootstrapStartDate: "20190101",
	})
	if err != nil {
		t.Fatalf("new trade service: %v", err)
	}

	if err := service.RefreshDay(context.Background(), TradeCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20200102",
	}); err != nil {
		t.Fatalf("refresh 20200102: %v", err)
	}
	if err := service.RefreshDay(context.Background(), TradeCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Date:      "20181228",
	}); err != nil {
		t.Fatalf("refresh 20181228: %v", err)
	}

	latest, err := store.GetCollectCursor(tradeHistoryDomain, string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("load latest trade cursor: %v", err)
	}
	if latest == nil || latest.Cursor != "20200102" {
		t.Fatalf("unexpected latest trade cursor: %#v", latest)
	}

	coverageStart, err := store.GetCollectCursor(tradeHistoryCoverageStartDomain, string(AssetTypeStock), "sh600000", "")
	if err != nil {
		t.Fatalf("load trade coverage-start cursor: %v", err)
	}
	if coverageStart == nil || coverageStart.Cursor != "20181228" {
		t.Fatalf("unexpected trade coverage-start cursor: %#v", coverageStart)
	}
}

func newTradeTick(code, date, clock string, price PriceMilli, volume int, status int) TradeTick {
	tm, _ := time.ParseInLocation("20060102 15:04", date+" "+clock, time.Local)
	return TradeTick{
		Code:       code,
		Time:       tm,
		Price:      price,
		VolumeHand: volume,
		Number:     1,
		StatusCode: status,
		Side:       map[int]string{0: "买入", 1: "卖出"}[status],
	}
}

func copyTradeRows(rows []TradeHistoryRow) []*TradeHistoryRow {
	out := make([]*TradeHistoryRow, 0, len(rows))
	for _, row := range rows {
		copyRow := row
		out = append(out, &copyRow)
	}
	return out
}

func stripTradeBarInDate(rows []TradeBarRow) []TradeBarRow {
	out := make([]TradeBarRow, 0, len(rows))
	for _, row := range rows {
		row.InDate = 0
		out = append(out, row)
	}
	return out
}

func stripTradeBarPtrs(rows []*TradeBarRow) []TradeBarRow {
	out := make([]TradeBarRow, 0, len(rows))
	for _, row := range rows {
		copyRow := *row
		copyRow.InDate = 0
		out = append(out, copyRow)
	}
	return out
}
