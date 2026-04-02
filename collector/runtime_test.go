package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

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
