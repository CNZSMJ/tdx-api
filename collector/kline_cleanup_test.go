package collector

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestKlineGapCleanupDryRunDoesNotMutateStore(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{}, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	start := dayAt(time.Date(2026, 4, 7, 0, 0, 0, 0, time.Local), 15, 15)
	end := dayAt(time.Date(2026, 4, 8, 0, 0, 0, 0, time.Local), 9, 30)
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(start.Unix(), 10),
		EndKey:     strconv.FormatInt(end.Unix(), 10),
		Status:     "open",
		Reason:     "legacy false positive",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.CleanupCollectGaps(context.Background(), KlineGapCleanupOptions{
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("cleanup gaps dry-run: %v", err)
	}
	if report.Closed != 1 || report.Updated != 0 {
		t.Fatalf("expected dry-run to propose one close, got closed=%d updated=%d", report.Closed, report.Updated)
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where("Domain = ? AND Instrument = ?", "kline", "sh513623").Get(record)
	if err != nil {
		t.Fatalf("query gap after dry-run: %v", err)
	}
	if !has {
		t.Fatalf("expected seeded gap to remain")
	}
	if record.Status != "open" {
		t.Fatalf("dry-run should not mutate status, got %s", record.Status)
	}
	if record.StartKey != strconv.FormatInt(start.Unix(), 10) || record.EndKey != strconv.FormatInt(end.Unix(), 10) {
		t.Fatalf("dry-run should not mutate range, got start=%s end=%s", record.StartKey, record.EndKey)
	}
}

func TestKlineGapCleanupApplyClosesFalsePositiveGap(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{}, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	start := dayAt(time.Date(2026, 4, 7, 0, 0, 0, 0, time.Local), 15, 15)
	end := dayAt(time.Date(2026, 4, 8, 0, 0, 0, 0, time.Local), 9, 30)
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(start.Unix(), 10),
		EndKey:     strconv.FormatInt(end.Unix(), 10),
		Status:     "open",
		Reason:     "legacy false positive",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.CleanupCollectGaps(context.Background(), KlineGapCleanupOptions{})
	if err != nil {
		t.Fatalf("cleanup gaps apply: %v", err)
	}
	if report.Closed != 1 || report.Updated != 0 {
		t.Fatalf("expected one closed gap, got closed=%d updated=%d", report.Closed, report.Updated)
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where("Domain = ? AND Instrument = ?", "kline", "sh513623").Get(record)
	if err != nil {
		t.Fatalf("query gap after cleanup: %v", err)
	}
	if !has {
		t.Fatalf("expected cleaned gap row to remain for audit")
	}
	if record.Status != "closed" {
		t.Fatalf("expected cleaned gap to be closed, got %s", record.Status)
	}
}

func TestKlineGapCleanupApplyShrinksGapWindow(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	service, err := NewKlineService(store, &klineStubProvider{}, KlineConfig{
		BaseDir: filepath.Join(tmp, "kline"),
	})
	if err != nil {
		t.Fatalf("new kline service: %v", err)
	}

	start := dayAt(time.Date(2026, 4, 7, 0, 0, 0, 0, time.Local), 10, 0)
	end := dayAt(time.Date(2026, 4, 8, 0, 0, 0, 0, time.Local), 9, 30)
	if err := store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(AssetTypeETF),
		Instrument: "sh513623",
		Period:     string(Period15Minute),
		StartKey:   strconv.FormatInt(start.Unix(), 10),
		EndKey:     strconv.FormatInt(end.Unix(), 10),
		Status:     "open",
		Reason:     "mixed true and false positive window",
	}); err != nil {
		t.Fatalf("seed gap: %v", err)
	}

	report, err := service.CleanupCollectGaps(context.Background(), KlineGapCleanupOptions{})
	if err != nil {
		t.Fatalf("cleanup gaps apply: %v", err)
	}
	if report.Closed != 0 || report.Updated != 1 {
		t.Fatalf("expected one updated gap, got closed=%d updated=%d", report.Closed, report.Updated)
	}

	record := new(CollectGapRecord)
	has, err := store.engine.Where("Domain = ? AND Instrument = ?", "kline", "sh513623").Get(record)
	if err != nil {
		t.Fatalf("query gap after cleanup: %v", err)
	}
	if !has {
		t.Fatalf("expected updated gap row to remain")
	}
	wantStart := newIntradayBar("sh513623", Period15Minute, "20260407 1000", 0, 0).Time.Unix()
	wantEnd := newIntradayBar("sh513623", Period15Minute, "20260407 1500", 0, 0).Time.Unix()
	if record.StartKey != strconv.FormatInt(wantStart, 10) || record.EndKey != strconv.FormatInt(wantEnd, 10) {
		t.Fatalf("expected shrunk range %d..%d, got %s..%s", wantStart, wantEnd, record.StartKey, record.EndKey)
	}
	if record.Status != "open" {
		t.Fatalf("expected updated gap to remain open, got %s", record.Status)
	}
}
