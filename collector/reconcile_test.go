package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectorReconcileDateWritesReportAndRepairsTradingDay(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &acceptanceProvider{
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
	}

	runtime, err := NewRuntime(store, provider, RuntimeConfig{
		Now:                   func() time.Time { return time.Date(2026, 4, 1, 19, 0, 0, 0, time.Local) },
		ReportDir:             filepath.Join(tmp, "reports"),
		KlinePeriods:          []KlinePeriod{PeriodDay},
		ReconcileScheduleName: "collector_daily_reconcile",
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

	report, err := runtime.ReconcileDate(context.Background(), "20260401")
	if err != nil {
		t.Fatalf("reconcile date: %v", err)
	}
	if report.Status != "passed" {
		t.Fatalf("expected passed reconcile report, got %+v", report)
	}
	if report.ReportPath == "" {
		t.Fatalf("expected report path to be populated")
	}
	if _, err := os.Stat(report.ReportPath); err != nil {
		t.Fatalf("expected report file to exist: %v", err)
	}

	loaded, err := ReadReconcileReport(filepath.Join(tmp, "reports"), "20260401")
	if err != nil {
		t.Fatalf("read reconcile report: %v", err)
	}
	if loaded.Date != "20260401" {
		t.Fatalf("unexpected loaded report date: %+v", loaded)
	}
	if len(loaded.Domains) == 0 {
		t.Fatalf("expected domain results in reconcile report")
	}
	foundTrade := false
	for _, domain := range loaded.Domains {
		if domain.Domain != "trade_history" {
			continue
		}
		foundTrade = true
		if domain.AfterRows == 0 {
			t.Fatalf("expected trade_history after_rows to be populated: %+v", domain)
		}
		if domain.ExpectedItems == 0 || domain.CoveredItems == 0 {
			t.Fatalf("expected trade_history coverage fields to be populated: %+v", domain)
		}
	}
	if !foundTrade {
		t.Fatalf("expected trade_history domain in reconcile report")
	}
}

func TestCollectorReconcileCancellationMarksInterrupted(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	runtime, err := NewRuntime(store, &runtimeCancelProvider{}, RuntimeConfig{
		Now:                   func() time.Time { return time.Date(2026, 4, 1, 19, 0, 0, 0, time.Local) },
		ReportDir:             filepath.Join(tmp, "reports"),
		KlinePeriods:          []KlinePeriod{PeriodDay},
		ReconcileScheduleName: "collector_daily_reconcile",
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runtime.ReconcileDate(ctx, "20260401"); err == nil {
		t.Fatalf("expected canceled reconcile to return an error")
	}

	run, err := store.LatestScheduleRun("collector_daily_reconcile")
	if err != nil {
		t.Fatalf("load reconcile run: %v", err)
	}
	if run == nil {
		t.Fatalf("expected reconcile run record")
	}
	if run.Status != "interrupted" {
		t.Fatalf("expected interrupted reconcile run, got %+v", run)
	}
}
