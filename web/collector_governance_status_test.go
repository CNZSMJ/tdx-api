package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

type governanceStatusProviderStub struct{}

func (s *governanceStatusProviderStub) Instruments(ctx context.Context, query collectorpkg.InstrumentQuery) ([]collectorpkg.Instrument, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) TradingDays(ctx context.Context, query collectorpkg.TradingDayQuery) ([]collectorpkg.TradingDay, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return false, nil
}

func (s *governanceStatusProviderStub) Quotes(ctx context.Context, codes []string) ([]collectorpkg.QuoteSnapshot, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) Minutes(ctx context.Context, query collectorpkg.MinuteQuery) ([]collectorpkg.MinutePoint, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) Klines(ctx context.Context, query collectorpkg.KlineQuery) ([]collectorpkg.KlineBar, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) TradeHistory(ctx context.Context, query collectorpkg.TradeHistoryQuery) ([]collectorpkg.TradeTick, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) OrderHistory(ctx context.Context, query collectorpkg.OrderHistoryQuery) (*collectorpkg.OrderHistorySnapshot, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) Finance(ctx context.Context, code string) (*collectorpkg.FinanceSnapshot, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) F10Categories(ctx context.Context, code string) ([]collectorpkg.F10Category, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) F10Content(ctx context.Context, query collectorpkg.F10ContentQuery) (*collectorpkg.F10Content, error) {
	return nil, nil
}

func (s *governanceStatusProviderStub) BlockGroups(ctx context.Context, filename string) ([]collectorpkg.BlockInfo, error) {
	return nil, nil
}

func TestHandleCollectorStatusIncludesGovernanceView(t *testing.T) {
	originalRuntime := collectorRuntime
	originalControl := collectorControl
	originalStore := governanceStore
	originalPaths := governancePaths
	defer func() {
		collectorRuntime = originalRuntime
		collectorControl = originalControl
		governanceStore = originalStore
		governancePaths = originalPaths
	}()

	collectorControl = newCollectorControlState()

	tmp := t.TempDir()
	store, err := collectorpkg.OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 19, 20, 0, 0, 0, time.Local)
	runtime, err := collectorpkg.NewRuntime(store, &governanceStatusProviderStub{}, collectorpkg.RuntimeConfig{
		Now: func() time.Time { return now },
		Metadata: collectorpkg.MetadataConfig{
			CodesDBPath:   filepath.Join(tmp, "codes.db"),
			WorkdayDBPath: filepath.Join(tmp, "workday.db"),
		},
		Kline:        collectorpkg.KlineConfig{BaseDir: filepath.Join(tmp, "kline")},
		Trade:        collectorpkg.TradeConfig{BaseDir: filepath.Join(tmp, "trade")},
		OrderHistory: collectorpkg.OrderHistoryConfig{BaseDir: filepath.Join(tmp, "order_history")},
		Live:         collectorpkg.LiveCaptureConfig{BaseDir: filepath.Join(tmp, "live")},
		Fundamentals: collectorpkg.FundamentalsConfig{BaseDir: filepath.Join(tmp, "fundamentals")},
		Block:        collectorpkg.BlockConfig{BaseDir: filepath.Join(tmp, "block"), DisableAutoRefresh: true},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()
	collectorRuntime = runtime

	for _, record := range []collectorpkg.ScheduleRunRecord{
		{
			ScheduleName: "collector_startup_catchup",
			Status:       "passed",
			StartedAt:    now.Add(-3 * time.Hour),
			EndedAt:      now.Add(-2 * time.Hour),
			Details:      "legacy startup run",
		},
		{
			ScheduleName: "collector_daily_full_sync",
			Status:       "passed",
			StartedAt:    now.Add(-2 * time.Hour),
			EndedAt:      now.Add(-90 * time.Minute),
			Details:      "legacy full sync",
		},
	} {
		record := record
		if err := store.AddScheduleRun(&record); err != nil {
			t.Fatalf("seed schedule run: %v", err)
		}
	}
	for _, cursor := range []collectorpkg.CollectCursorRecord{
		{Domain: "codes", AssetType: collectorpkg.MetadataAssetType, Instrument: collectorpkg.MetadataAllKey, Cursor: "1713517200"},
		{Domain: "workday", AssetType: collectorpkg.MetadataAssetType, Instrument: collectorpkg.MetadataAllKey, Cursor: "20260418"},
	} {
		cursor := cursor
		if err := store.UpsertCollectCursor(&cursor); err != nil {
			t.Fatalf("seed cursor: %v", err)
		}
	}

	governancePaths = collectorpkg.ResolveGovernancePaths(tmp)
	governanceStore, err = collectorpkg.OpenGovernanceStore(governancePaths.DBPath)
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer governanceStore.Close()

	if err := governanceStore.UpsertTask(&collectorpkg.GovernanceTaskRecord{
		TaskKey:      "task-1",
		JobName:      string(collectorpkg.GovernanceJobDailyAudit),
		Domain:       "kline",
		Status:       collectorpkg.GovernanceTaskStatusOpen,
		Priority:     2,
		Reason:       "coverage gap",
		TargetWindow: "20260418",
	}); err != nil {
		t.Fatalf("seed governance task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/collector/status", nil)
	rec := httptest.NewRecorder()
	handleCollectorStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Governance struct {
				Paths struct {
					DBPath string `json:"db_path"`
				} `json:"paths"`
				Jobs []struct {
					Name string `json:"name"`
				} `json:"jobs"`
				Domains []struct {
					Domain string `json:"domain"`
				} `json:"domains"`
				Tasks []struct {
					TaskKey string `json:"task_key"`
				} `json:"tasks"`
			} `json:"governance"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if payload.Code != 0 {
		t.Fatalf("response code = %d, want 0", payload.Code)
	}
	if payload.Data.Governance.Paths.DBPath != governancePaths.DBPath {
		t.Fatalf("governance db path = %s, want %s", payload.Data.Governance.Paths.DBPath, governancePaths.DBPath)
	}
	if len(payload.Data.Governance.Jobs) != 5 {
		t.Fatalf("governance jobs = %d, want 5", len(payload.Data.Governance.Jobs))
	}
	if len(payload.Data.Governance.Domains) == 0 {
		t.Fatalf("expected governance domains in status payload")
	}
	if len(payload.Data.Governance.Tasks) != 1 || payload.Data.Governance.Tasks[0].TaskKey != "task-1" {
		t.Fatalf("unexpected governance tasks: %+v", payload.Data.Governance.Tasks)
	}
}
