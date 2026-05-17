package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/market/billboard"
)

func TestHandleMarketBillboardReturnsFreshnessAndHidesSourceByDefault(t *testing.T) {
	originalStore := marketBillboardStore
	defer func() { marketBillboardStore = originalStore }()

	store, err := billboard.OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer store.Close()
	marketBillboardStore = store

	if _, err := store.UpsertEntry(billboard.EntryRecord{
		TradeDate:                "20240515",
		FullCode:                 "sz000504",
		Code:                     "000504",
		Exchange:                 "sz",
		Name:                     "*ST生物",
		AssetType:                "stock",
		BillboardBuyAmountMilli:  50185957000,
		BillboardSellAmountMilli: 69639811660,
		BillboardNetAmountMilli:  -19453854660,
		SourceReportName:         billboard.ReportDailyDetails,
		SourceTradeID:            "4967145",
		SourceChangeType:         "137001002002002",
		SourceRowID:              "source-row",
		SourcePayloadHash:        "source-hash",
		FetchedAt:                time.Now(),
	}, []billboard.ReasonRecord{{ReasonText: "日跌幅偏离值达到7%的前5只证券", ReasonHash: billboard.HashText("日跌幅偏离值达到7%的前5只证券")}}); err != nil {
		t.Fatalf("upsert entry: %v", err)
	}
	for _, report := range billboard.CoreReports() {
		if err := store.UpsertSyncStatus(billboard.SyncStatusRecord{
			TradeDate:   "20240515",
			ReportName:  report,
			Status:      billboard.SyncStatusPassed,
			RowCount:    1,
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("upsert status: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/market/billboard?trade_date=20240515", nil)
	rec := httptest.NewRecorder()
	handleMarketBillboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Items     []map[string]any `json:"items"`
			Freshness struct {
				Domain   string `json:"domain"`
				Coverage string `json:"coverage"`
				Status   string `json:"status"`
			} `json:"freshness"`
			Source struct {
				Provider string   `json:"provider"`
				Module   string   `json:"module"`
				Reports  []string `json:"reports"`
			} `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Code != 0 || len(payload.Data.Items) != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.Data.Freshness.Domain != billboard.DomainMarketBillboard || payload.Data.Freshness.Coverage != billboard.CoverageComplete || payload.Data.Freshness.Status != "fresh" {
		t.Fatalf("unexpected freshness: %#v", payload.Data.Freshness)
	}
	if payload.Data.Source.Provider != "eastmoney" || payload.Data.Source.Module != "rpt" || len(payload.Data.Source.Reports) == 0 {
		t.Fatalf("unexpected source: %#v", payload.Data.Source)
	}
	if _, ok := payload.Data.Items[0]["source_row_id"]; ok {
		t.Fatalf("source fields should be hidden by default: %#v", payload.Data.Items[0])
	}
}

func TestHandleMarketBillboardStatsUsesTradeAllWatermark(t *testing.T) {
	originalStore := marketBillboardStore
	defer func() { marketBillboardStore = originalStore }()

	store, err := billboard.OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer store.Close()
	marketBillboardStore = store

	if err := store.UpsertInstrumentStat(billboard.InstrumentStatRecord{
		FullCode:          "bj920580",
		Code:              "920580",
		Exchange:          "bj",
		Name:              "科创新材",
		StatisticsCycle:   "04",
		PeriodLabel:       "近一年",
		LatestTradeDate:   "20260515",
		BillboardTimes:    7,
		SourceReportName:  billboard.ReportTradeAll,
		SourceRowID:       "trade-all-row",
		SourcePayloadHash: "trade-all-hash",
		FetchedAt:         time.Now(),
	}); err != nil {
		t.Fatalf("upsert instrument stat: %v", err)
	}
	if err := store.UpsertSyncStatus(billboard.SyncStatusRecord{
		TradeDate:   "20260515",
		ReportName:  billboard.ReportTradeAll,
		Status:      billboard.SyncStatusPassed,
		RowCount:    1,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert tradeall status: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/market/billboard/stats?full_code=bj920580&limit=1", nil)
	rec := httptest.NewRecorder()
	handleMarketBillboardStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Items     []map[string]any    `json:"items"`
			Freshness billboard.Freshness `json:"freshness"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Code != 0 || len(payload.Data.Items) != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.Data.Freshness.Coverage != billboard.CoverageComplete || payload.Data.Freshness.Watermark != "20260515" {
		t.Fatalf("unexpected freshness: %#v", payload.Data.Freshness)
	}
}
