package collector

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestKlineGapDowngradeMarksMatchedGapsAsDegraded(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{
		items: []KlineBar{
			newIntradayBar("sh513623", Period15Minute, "20260408 0945", 10100, 10150),
		},
	}, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	start := newIntradayBar("sh513623", Period15Minute, "20260407 0945", 0, 0).Time.Unix()
	end := newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix()
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(start, 10),
		EndKey:     strconv.FormatInt(end, 10),
		Status:     CollectGapStatusOpen,
		Reason:     "seed gap",
	}); err != nil {
		t.Fatalf("seed open gap: %v", err)
	}

	report, err := service.DowngradeCollectGaps(context.Background(), KlineGapDowngradeOptions{
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
		Reason:    "manual downgrade after failed repair",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("downgrade gaps: %v", err)
	}
	if report.Downgraded != 1 || report.RemainingOpenGaps != 0 || report.DegradedGapCount != 1 {
		t.Fatalf("unexpected downgrade report: %+v", report)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("expected one downgrade entry, got %d", len(report.Entries))
	}
	if len(report.Entries[0].TaskDates) != 1 || report.Entries[0].TaskDates[0] != "20260407" {
		t.Fatalf("unexpected governance task dates: %+v", report.Entries[0])
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where("Domain = ? AND Instrument = ?", "kline", "sh513623").Get(record)
	if err != nil {
		t.Fatalf("load downgraded gap: %v", err)
	}
	if !has || record.Status != CollectGapStatusDegraded {
		t.Fatalf("expected degraded gap record, got %+v", record)
	}
}

func TestRuntimeDowngradeKlineGapsWritesReportFiles(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	runtime, err := NewRuntime(store, &klineStubProvider{
		items: []KlineBar{
			newDayBar("sh600000", "20260408", 10100, 10150),
		},
	}, RuntimeConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 19, 2, 10, 0, 0, time.Local) },
		ReportDir:    filepath.Join(tmp, "reports"),
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
		t.Fatalf("new runtime: %v", err)
	}

	start := time.Date(2026, 4, 7, 15, 0, 0, 0, time.Local).Unix()
	end := time.Date(2026, 4, 7, 15, 0, 0, 0, time.Local).Unix()
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeStock),
		Instrument: "sh600000",
		Period:     string(PeriodDay),
		StartKey:   strconv.FormatInt(start, 10),
		EndKey:     strconv.FormatInt(end, 10),
		Status:     CollectGapStatusOpen,
		Reason:     "seed daily gap",
	}); err != nil {
		t.Fatalf("seed daily gap: %v", err)
	}

	report, err := runtime.DowngradeKlineGaps(context.Background(), KlineGapDowngradeOptions{
		AssetType: AssetTypeStock,
		Period:    PeriodDay,
		Reason:    "manual downgrade",
	})
	if err != nil {
		t.Fatalf("runtime downgrade gaps: %v", err)
	}
	if report.ReportPath == "" || report.TaskListPath == "" {
		t.Fatalf("expected report files, got %+v", report)
	}
	if _, err := os.Stat(report.ReportPath); err != nil {
		t.Fatalf("expected JSON report to exist: %v", err)
	}
	if _, err := os.Stat(report.TaskListPath); err != nil {
		t.Fatalf("expected task list to exist: %v", err)
	}
}
