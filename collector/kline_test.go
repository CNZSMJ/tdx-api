package collector

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type klineStubProvider struct {
	items       []KlineBar
	tradingDays map[string]bool
}

func (s *klineStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	items := make([]TradingDay, 0, 16)
	start := query.Start
	if start.IsZero() {
		start = time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)
	}
	end := query.End
	if end.IsZero() {
		end = start.AddDate(0, 0, 31)
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		ok, err := s.IsTradingDay(ctx, day)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, TradingDay{
				Date: day.Format("20060102"),
				Time: day,
			})
		}
	}
	return items, nil
}

func (s *klineStubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	if len(s.tradingDays) > 0 {
		return s.tradingDays[day.Format("20060102")], nil
	}
	switch day.Weekday() {
	case time.Saturday, time.Sunday:
		return false, nil
	default:
		return true, nil
	}
}

func (s *klineStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	result := make([]KlineBar, 0, len(s.items))
	for _, item := range s.items {
		if item.Code != query.Code || item.Period != query.Period {
			continue
		}
		if !query.Since.IsZero() && !item.Time.After(query.Since) {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *klineStubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	return nil, errors.New("not implemented")
}

func (s *klineStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestKlineRefreshPublishesAndPersistsCursor(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &klineStubProvider{
		items: []KlineBar{
			newDayBar("sh600000", "20260330", 12000, 12100),
			newDayBar("sh600000", "20260331", 12100, 12200),
		},
	}
	service, err := NewKlineService(store, provider, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := service.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("refresh kline: %v", err)
	}

	rows := loadKlineRows(t, filepath.Join(tmp, "kline", "sh600000.db"), "DayKline")
	if len(rows) != 2 {
		t.Fatalf("expected 2 published day bars, got %d", len(rows))
	}
	cursor, err := store.GetCollectCursor("kline", string(AssetTypeStock), "sh600000", string(PeriodDay))
	if err != nil {
		t.Fatalf("kline cursor: %v", err)
	}
	if cursor == nil || cursor.Cursor == "" {
		t.Fatalf("expected persisted kline cursor")
	}
}

func TestKlineRefreshIsOverlapSafeAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	klineDir := filepath.Join(tmp, "kline")

	makeService := func(items []KlineBar) (*Store, *KlineService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewKlineService(store, &klineStubProvider{items: items}, KlineConfig{
			BaseDir:       klineDir,
			ReplayOverlap: 1,
		})
		if err != nil {
			t.Fatalf("new kline service: %v", err)
		}
		return store, service
	}

	first := []KlineBar{
		newDayBar("sh600000", "20260330", 12000, 12100),
		newDayBar("sh600000", "20260331", 12100, 12200),
	}
	store1, service1 := makeService(first)
	if err := service1.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	second := []KlineBar{
		newDayBar("sh600000", "20260331", 12100, 12300),
		newDayBar("sh600000", "20260401", 12300, 12400),
	}
	store2, service2 := makeService(second)
	defer store2.Close()
	if err := service2.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	rows := loadKlineRows(t, filepath.Join(klineDir, "sh600000.db"), "DayKline")
	if len(rows) != 3 {
		t.Fatalf("expected 3 day bars after overlap replay, got %d", len(rows))
	}
	if rows[1].Close != 12300 {
		t.Fatalf("expected replayed 20260331 close to be replaced, got %d", rows[1].Close)
	}
}

func TestKlineRefreshRecordsGap(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	klineDir := filepath.Join(tmp, "kline")

	makeService := func(items []KlineBar) (*Store, *KlineService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewKlineService(store, &klineStubProvider{items: items}, KlineConfig{
			BaseDir:       klineDir,
			ReplayOverlap: 1,
		})
		if err != nil {
			t.Fatalf("new kline service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService([]KlineBar{
		newDayBar("sh600000", "20260330", 12000, 12100),
	})
	if err := service1.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService([]KlineBar{
		newDayBar("sh600000", "20260401", 12100, 12300),
	})
	defer store2.Close()
	if err := service2.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	gap, err := store2.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?",
		"kline", string(AssetTypeStock), "sh600000", string(PeriodDay),
	).Get(new(CollectGapRecord))
	if err != nil {
		t.Fatalf("query kline gap: %v", err)
	}
	if !gap {
		t.Fatalf("expected kline gap record after skipped day")
	}
}

func TestKlineRefreshDoesNotRecordIntradayGapAcrossTradingBreak(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	klineDir := filepath.Join(tmp, "kline")

	makeService := func(items []KlineBar) (*Store, *KlineService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewKlineService(store, &klineStubProvider{items: items}, KlineConfig{
			BaseDir:       klineDir,
			ReplayOverlap: 1,
		})
		if err != nil {
			t.Fatalf("new kline service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService([]KlineBar{
		newIntradayBar("sh513623", Period15Minute, "20260415 1500", 10000, 10050),
	})
	if err := service1.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService([]KlineBar{
		newIntradayBar("sh513623", Period15Minute, "20260416 0945", 10050, 10100),
	})
	defer store2.Close()
	if err := service2.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	count, err := store2.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ? AND Status = ?",
		"kline", string(AssetTypeETF), "sh513623", string(Period15Minute), "open",
	).Count(new(CollectGapRecord))
	if err != nil {
		t.Fatalf("count kline gap: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no intraday gap across overnight session, got %d", count)
	}
}

func TestKlineRefreshDoesNotRecordGapAcrossWeekend(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	klineDir := filepath.Join(tmp, "kline")

	makeService := func(items []KlineBar) (*Store, *KlineService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewKlineService(store, &klineStubProvider{items: items}, KlineConfig{
			BaseDir:       klineDir,
			ReplayOverlap: 1,
		})
		if err != nil {
			t.Fatalf("new kline service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService([]KlineBar{
		newDayBar("sh600000", "20260410", 12000, 12100),
	})
	if err := service1.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService([]KlineBar{
		newDayBar("sh600000", "20260413", 12100, 12200),
	})
	defer store2.Close()
	if err := service2.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh600000",
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	count, err := store2.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ? AND Status = ?",
		"kline", string(AssetTypeStock), "sh600000", string(PeriodDay), "open",
	).Count(new(CollectGapRecord))
	if err != nil {
		t.Fatalf("count day gap: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no day gap across weekend, got %d", count)
	}
}

func TestKlineRefreshRecordsTrueIntradayGap(t *testing.T) {
	tmp := t.TempDir()
	collectorDB := filepath.Join(tmp, "collector.db")
	klineDir := filepath.Join(tmp, "kline")

	makeService := func(items []KlineBar) (*Store, *KlineService) {
		store, err := OpenStore(collectorDB)
		if err != nil {
			t.Fatalf("open collector store: %v", err)
		}
		service, err := NewKlineService(store, &klineStubProvider{items: items}, KlineConfig{
			BaseDir:       klineDir,
			ReplayOverlap: 1,
		})
		if err != nil {
			t.Fatalf("new kline service: %v", err)
		}
		return store, service
	}

	store1, service1 := makeService([]KlineBar{
		newIntradayBar("sh513623", Period15Minute, "20260416 0945", 10000, 10050),
	})
	if err := service1.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_ = store1.Close()

	store2, service2 := makeService([]KlineBar{
		newIntradayBar("sh513623", Period15Minute, "20260416 1015", 10050, 10100),
	})
	defer store2.Close()
	if err := service2.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	record := new(CollectGapRecord)
	has, err := store2.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ? AND Status = ?",
		"kline", string(AssetTypeETF), "sh513623", string(Period15Minute), "open",
	).Get(record)
	if err != nil {
		t.Fatalf("query intraday gap: %v", err)
	}
	if !has {
		t.Fatalf("expected intraday gap record")
	}
	expected := newIntradayBar("sh513623", Period15Minute, "20260416 1000", 0, 0).Time.Unix()
	if record.StartKey != strconv.FormatInt(expected, 10) || record.EndKey != strconv.FormatInt(expected, 10) {
		t.Fatalf("expected 10:00 gap window, got start=%s end=%s", record.StartKey, record.EndKey)
	}
}

func TestKlineReconcileClosesInvalidLegacyGap(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{
		items: []KlineBar{
			newIntradayBar("sh513623", Period15Minute, "20260408 0945", 10000, 10050),
			newIntradayBar("sh513623", Period15Minute, "20260408 1000", 10050, 10100),
		},
	}, KlineConfig{
		BaseDir:       filepath.Join(tmp, "kline"),
		ReplayOverlap: 1,
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(dayAt(time.Date(2026, 4, 7, 0, 0, 0, 0, time.Local), 15, 15).Unix(), 10),
		EndKey:     strconv.FormatInt(dayAt(time.Date(2026, 4, 8, 0, 0, 0, 0, time.Local), 9, 30).Unix(), 10),
		Status:     "open",
		Reason:     "legacy false positive",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	if err := service.ReconcileDate(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}, "20260408"); err != nil {
		t.Fatalf("reconcile date: %v", err)
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?",
		"kline", string(AssetTypeETF), "sh513623", string(Period15Minute),
	).Get(record)
	if err != nil {
		t.Fatalf("query legacy gap: %v", err)
	}
	if !has {
		t.Fatalf("expected gap record to remain for audit trail")
	}
	if record.Status != "closed" {
		t.Fatalf("expected legacy gap to be closed, got %s", record.Status)
	}
}

func newDayBar(code, date string, open, close PriceMilli) KlineBar {
	t, _ := time.ParseInLocation("20060102", date, time.Local)
	return KlineBar{
		Code:       code,
		AssetType:  AssetTypeStock,
		Period:     PeriodDay,
		Time:       time.Date(t.Year(), t.Month(), t.Day(), 15, 0, 0, 0, time.Local),
		Open:       open,
		High:       close,
		Low:        open,
		Close:      close,
		VolumeHand: 1000,
		Amount:     close * 1000,
	}
}

func newIntradayBar(code string, period KlinePeriod, stamp string, open, close PriceMilli) KlineBar {
	t, _ := time.ParseInLocation("20060102 1504", stamp, time.Local)
	return KlineBar{
		Code:       code,
		AssetType:  detectAssetType(code),
		Period:     period,
		Time:       t,
		Open:       open,
		High:       close,
		Low:        open,
		Close:      close,
		VolumeHand: 1000,
		Amount:     close * 1000,
	}
}

func loadKlineRows(t *testing.T, filename, table string) []KlinePublishRow {
	t.Helper()
	engine, err := openMetadataEngine(filename)
	if err != nil {
		t.Fatalf("open kline db: %v", err)
	}
	defer engine.Close()

	rows := make([]KlinePublishRow, 0)
	if err := engine.Table(table).Asc("Date").Find(&rows); err != nil {
		t.Fatalf("load kline rows: %v", err)
	}
	return rows
}
