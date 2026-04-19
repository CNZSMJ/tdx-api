package collector

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRuntimeResolveDeepAuditDatesFromExplicitWindow(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &stubProvider{
		tradingDays: []TradingDay{
			{Date: "20260330", Time: time.Date(2026, 3, 30, 15, 0, 0, 0, time.Local)},
			{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
			{Date: "20260401", Time: time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local)},
		},
	}
	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now: func() time.Time { return time.Date(2026, 4, 21, 3, 0, 0, 0, time.Local) },
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

	dates, err := runtime.ResolveDeepAuditDates(context.Background(), "20260330", "20260331", false, 0)
	if err != nil {
		t.Fatalf("resolve deep audit dates: %v", err)
	}
	want := []string{"20260330", "20260331"}
	if len(dates) != len(want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("dates = %v, want %v", dates, want)
		}
	}
}

func TestRuntimeResolveDeepAuditDatesFromOpenGapBacklog(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &stubProvider{
		tradingDays: []TradingDay{
			{Date: "20260330", Time: time.Date(2026, 3, 30, 15, 0, 0, 0, time.Local)},
			{Date: "20260331", Time: time.Date(2026, 3, 31, 15, 0, 0, 0, time.Local)},
			{Date: "20260401", Time: time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local)},
		},
	}
	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now: func() time.Time { return time.Date(2026, 4, 21, 3, 0, 0, 0, time.Local) },
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

	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeStock),
		Instrument: "sh600000",
		Period:     string(PeriodDay),
		StartKey:   strconv.FormatInt(time.Date(2026, 3, 30, 15, 0, 0, 0, time.Local).Unix(), 10),
		EndKey:     strconv.FormatInt(time.Date(2026, 4, 1, 15, 0, 0, 0, time.Local).Unix(), 10),
		Status:     CollectGapStatusOpen,
		Reason:     "historical gap",
	}); err != nil {
		t.Fatalf("seed collect gap: %v", err)
	}

	dates, err := runtime.ResolveDeepAuditDates(context.Background(), "", "", true, 0)
	if err != nil {
		t.Fatalf("resolve deep audit backlog dates: %v", err)
	}
	want := []string{"20260330", "20260331", "20260401"}
	if len(dates) != len(want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("dates = %v, want %v", dates, want)
		}
	}
}
