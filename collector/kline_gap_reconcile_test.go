package collector

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestKlineGapReconcileDryRunPlansUniqueTasksWithoutMutating(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &klineStubProvider{
		items: []KlineBar{
			newIntradayBar("sh513623", Period15Minute, "20260407 0945", 10000, 10050),
			newIntradayBar("sh513623", Period15Minute, "20260407 1000", 10050, 10100),
			newIntradayBar("sh513623", Period15Minute, "20260408 0945", 10100, 10150),
		},
	}
	service, err := NewKlineService(store, provider, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 0945", 0, 0).Time.Unix(), 10),
		EndKey:     strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix(), 10),
		Status:     "open",
		Reason:     "seed gap one",
	}); err != nil {
		t.Fatalf("seed first gap: %v", err)
	}
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1015", 0, 0).Time.Unix(), 10),
		EndKey:     strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix(), 10),
		Status:     "open",
		Reason:     "seed gap two",
	}); err != nil {
		t.Fatalf("seed second gap: %v", err)
	}

	report, err := service.ReconcileCollectGaps(context.Background(), KlineGapReconcileOptions{
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
		StartDate: "20260407",
		EndDate:   "20260407",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("reconcile gaps dry run: %v", err)
	}
	if report.MatchedGaps != 2 {
		t.Fatalf("matched gaps = %d, want 2", report.MatchedGaps)
	}
	if report.Planned != 1 || len(report.Tasks) != 1 {
		t.Fatalf("planned tasks = %d len=%d, want 1", report.Planned, len(report.Tasks))
	}
	task := report.Tasks[0]
	if task.Instrument != "sh513623" || task.Period != Period15Minute || task.Date != "20260407" {
		t.Fatalf("unexpected task: %+v", task)
	}

	openCount, err := store.engine.Where("Status = ?", "open").Count(new(CollectGapRecord))
	if err != nil {
		t.Fatalf("count open gaps: %v", err)
	}
	if openCount != 2 {
		t.Fatalf("dry run should not mutate gaps, got %d open rows", openCount)
	}
}

func TestKlineGapReconcileApplyRepairsGapAndClosesStatus(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &klineStubProvider{
		items: []KlineBar{
			newIntradayBar("sh513623", Period15Minute, "20260407 0945", 10000, 10050),
			newIntradayBar("sh513623", Period15Minute, "20260407 1000", 10050, 10100),
			newIntradayBar("sh513623", Period15Minute, "20260408 0945", 10100, 10150),
		},
	}
	service, err := NewKlineService(store, provider, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := service.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 0945", 0, 0).Time.Unix(), 10),
		EndKey:     strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix(), 10),
		Status:     "open",
		Reason:     "seed gap",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.ReconcileCollectGaps(context.Background(), KlineGapReconcileOptions{
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
		StartDate: "20260407",
		EndDate:   "20260407",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("reconcile gaps apply: %v", err)
	}
	if report.Executed != 1 || report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report counters: %+v", report)
	}
	if report.RemainingOpenGaps != 0 {
		t.Fatalf("remaining open gaps = %d, want 0", report.RemainingOpenGaps)
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?",
		"kline", string(AssetTypeETF), "sh513623", string(Period15Minute),
	).Get(record)
	if err != nil {
		t.Fatalf("load gap record: %v", err)
	}
	if !has {
		t.Fatalf("expected gap audit row to remain")
	}
	if record.Status != "closed" {
		t.Fatalf("gap status = %s, want closed", record.Status)
	}

	rows := loadKlineRows(t, filepath.Join(tmp, "kline", "sh513623.db"), "Minute15Kline")
	if len(rows) != 3 {
		t.Fatalf("expected reconciled kline rows, got %d", len(rows))
	}
	if got := time.Unix(rows[0].Date, 0).Format("20060102"); got != "20260407" {
		t.Fatalf("first reconciled row date = %s, want 20260407", got)
	}
}

func TestKlineGapReconcileApplyUsesLiveCacheFallback(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &klineStubProvider{
		items: []KlineBar{
			newIntradayBar("sh513623", Period15Minute, "20260408 0945", 10100, 10150),
		},
	}
	service, err := NewKlineService(store, provider, KlineConfig{
		BaseDir:     filepath.Join(tmp, "kline"),
		LiveBaseDir: filepath.Join(tmp, "live"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := service.Refresh(context.Background(), KlineCollectQuery{
		Code:      "sh513623",
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
	}); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if err := seedMinuteLiveRows(filepath.Join(tmp, "live"), "sh513623", "20260407"); err != nil {
		t.Fatalf("seed minute live rows: %v", err)
	}
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 0945", 0, 0).Time.Unix(), 10),
		EndKey:     strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix(), 10),
		Status:     "open",
		Reason:     "seed gap",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.ReconcileCollectGaps(context.Background(), KlineGapReconcileOptions{
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
		StartDate: "20260407",
		EndDate:   "20260407",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("reconcile gaps apply: %v", err)
	}
	if report.Executed != 1 || report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report counters: %+v", report)
	}
	if report.RemainingOpenGaps != 0 {
		t.Fatalf("remaining open gaps = %d, want 0", report.RemainingOpenGaps)
	}

	rows := loadKlineRows(t, filepath.Join(tmp, "kline", "sh513623.db"), "Minute15Kline")
	if len(rows) == 0 {
		t.Fatalf("expected reconciled rows from live cache")
	}
	if got := time.Unix(rows[0].Date, 0).Format("20060102"); got != "20260407" {
		t.Fatalf("first reconciled row date = %s, want 20260407", got)
	}
}

func TestKlineGapReconcileApplyReportsNoEffectWhenGapRemains(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{}, KlineConfig{
		BaseDir:     filepath.Join(tmp, "kline"),
		LiveBaseDir: filepath.Join(tmp, "live"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 0945", 0, 0).Time.Unix(), 10),
		EndKey:     strconv.FormatInt(newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix(), 10),
		Status:     "open",
		Reason:     "seed gap",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.ReconcileCollectGaps(context.Background(), KlineGapReconcileOptions{
		AssetType: AssetTypeETF,
		Period:    Period15Minute,
		StartDate: "20260407",
		EndDate:   "20260407",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("reconcile gaps apply: %v", err)
	}
	if report.Executed != 1 || report.Succeeded != 0 || report.Failed != 1 {
		t.Fatalf("unexpected report counters: %+v", report)
	}
	if report.RemainingOpenGaps != 1 {
		t.Fatalf("remaining open gaps = %d, want 1", report.RemainingOpenGaps)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("expected one report error, got %+v", report.Errors)
	}
}

func seedMinuteLiveRows(baseDir, code, tradeDate string) error {
	if err := os.MkdirAll(baseDir, 0o777); err != nil {
		return err
	}
	engine, err := openMetadataEngine(filepath.Join(baseDir, code+".db"))
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.Table("MinuteLive").Sync2(new(MinuteLiveRow)); err != nil {
		return err
	}
	rows := make([]any, 0, 240)
	for hour, minute := 9, 31; hour < 11 || (hour == 11 && minute <= 30); minute++ {
		for minute >= 60 {
			hour++
			minute -= 60
		}
		rows = append(rows, &MinuteLiveRow{
			Code:      code,
			TradeDate: tradeDate,
			Clock:     time.Date(2000, 1, 1, hour, minute, 0, 0, time.Local).Format("15:04"),
			Price:     PriceMilli(10000 + len(rows)),
		})
	}
	for hour, minute := 13, 1; hour < 15 || (hour == 15 && minute <= 0); minute++ {
		for minute >= 60 {
			hour++
			minute -= 60
		}
		rows = append(rows, &MinuteLiveRow{
			Code:      code,
			TradeDate: tradeDate,
			Clock:     time.Date(2000, 1, 1, hour, minute, 0, 0, time.Local).Format("15:04"),
			Price:     PriceMilli(10000 + len(rows)),
		})
	}
	_, err = engine.Table("MinuteLive").Insert(rows...)
	return err
}
