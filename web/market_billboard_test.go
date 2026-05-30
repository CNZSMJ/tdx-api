package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
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

func TestHandleMarketBillboardUsesWatermarkWhenDateOmitted(t *testing.T) {
	originalStore := marketBillboardStore
	defer func() { marketBillboardStore = originalStore }()

	store, err := billboard.OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer store.Close()
	marketBillboardStore = store

	if _, err := store.UpsertEntry(billboard.EntryRecord{
		TradeDate:         "20260515",
		FullCode:          "bj920580",
		Code:              "920580",
		Exchange:          "bj",
		Name:              "科创新材",
		AssetType:         "stock",
		SourceReportName:  billboard.ReportDailyDetails,
		SourceTradeID:     "100325759",
		SourceChangeType:  "137001004001",
		SourceRowID:       "source-row",
		SourcePayloadHash: "source-hash",
		FetchedAt:         time.Now(),
	}, []billboard.ReasonRecord{{ReasonText: "日换手率达到20%的前5只证券", ReasonHash: billboard.HashText("日换手率达到20%的前5只证券")}}); err != nil {
		t.Fatalf("upsert entry: %v", err)
	}
	for _, report := range billboard.CoreReports() {
		if err := store.UpsertSyncStatus(billboard.SyncStatusRecord{
			TradeDate:   "20260515",
			ReportName:  report,
			Status:      billboard.SyncStatusPassed,
			RowCount:    1,
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("upsert status: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/market/billboard?limit=1", nil)
	rec := httptest.NewRecorder()
	handleMarketBillboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Freshness billboard.Freshness `json:"freshness"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Data.Freshness.Coverage != billboard.CoverageComplete || payload.Data.Freshness.Watermark != "20260515" {
		t.Fatalf("unexpected freshness: %#v", payload.Data.Freshness)
	}
}

func TestMarketBillboardCoverageUsesExpectedTradeDateWhenDateOmitted(t *testing.T) {
	store, err := billboard.OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer store.Close()

	for _, report := range billboard.CoreReports() {
		if err := store.UpsertSyncStatus(billboard.SyncStatusRecord{
			TradeDate:   "20260515",
			ReportName:  report,
			Status:      billboard.SyncStatusPassed,
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("upsert status: %v", err)
		}
	}

	freshness, err := marketBillboardCoverageForExpectedDate(store, "", "", billboard.CoreReports(), "20260522")
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if freshness.Status != "stale" || freshness.Coverage != billboard.CoverageMissing {
		t.Fatalf("freshness = %#v, want stale missing", freshness)
	}
	if freshness.Watermark != "20260515" || freshness.ExpectedTradeDate != "20260522" {
		t.Fatalf("freshness = %#v, want actual watermark and expected trade date", freshness)
	}
	if freshness.QueryStartDate != "20260522" || freshness.QueryEndDate != "20260522" {
		t.Fatalf("freshness = %#v, want query range pinned to expected trade date", freshness)
	}
}

func TestProjectMarketBillboardGovernanceDomainMarksLaggingWatermarkStale(t *testing.T) {
	originalBillboardStore := marketBillboardStore
	originalGovernanceStore := governanceStore
	defer func() {
		marketBillboardStore = originalBillboardStore
		governanceStore = originalGovernanceStore
	}()

	tmp := t.TempDir()
	billboardStore, err := billboard.OpenStore(filepath.Join(tmp, "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer billboardStore.Close()
	govStore, err := collectorpkg.OpenGovernanceStore(filepath.Join(tmp, "governance.db"))
	if err != nil {
		t.Fatalf("open governance store: %v", err)
	}
	defer govStore.Close()
	marketBillboardStore = billboardStore
	governanceStore = govStore

	if err := billboardStore.UpsertSyncStatus(billboard.SyncStatusRecord{
		TradeDate:   "20260515",
		ReportName:  billboard.ReportTradeAll,
		Status:      billboard.SyncStatusPassed,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert status: %v", err)
	}

	if err := projectMarketBillboardGovernanceDomainForExpectedDate("20260522"); err != nil {
		t.Fatalf("project domain: %v", err)
	}
	snapshot, err := govStore.GetDomainHealthSnapshot(billboard.DomainMarketBillboard)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatalf("snapshot missing")
	}
	if snapshot.Status != "degraded" || snapshot.Freshness != "stale" || snapshot.Coverage != billboard.CoverageMissing {
		t.Fatalf("snapshot = %+v, want degraded stale missing", snapshot)
	}
	if snapshot.LatestWatermark != "20260515" {
		t.Fatalf("snapshot watermark = %q, want 20260515", snapshot.LatestWatermark)
	}
}

func TestHandleMarketBillboardSeatSearchFiltersBeforePagination(t *testing.T) {
	originalStore := marketBillboardStore
	defer func() { marketBillboardStore = originalStore }()

	store, err := billboard.OpenStore(filepath.Join(t.TempDir(), "market_billboard.db"))
	if err != nil {
		t.Fatalf("open billboard store: %v", err)
	}
	defer store.Close()
	marketBillboardStore = store

	matchingSeatID, err := store.UpsertSeat(billboard.SeatRecord{
		SeatName: "机构专用",
		SeatType: billboard.SeatTypeInstitution,
	})
	if err != nil {
		t.Fatalf("upsert matching seat: %v", err)
	}
	otherSeatID, err := store.UpsertSeat(billboard.SeatRecord{
		SeatName: "申万宏源证券有限公司上海天钥桥路营业部",
		SeatType: billboard.SeatTypeBrokerage,
	})
	if err != nil {
		t.Fatalf("upsert other seat: %v", err)
	}
	for _, row := range []struct {
		seatID int64
		name   string
	}{
		{matchingSeatID, "matching"},
		{otherSeatID, "other"},
	} {
		if err := store.UpsertSeatTrade(billboard.SeatTradeRecord{
			SeatID:            row.seatID,
			TradeDate:         "20260515",
			FullCode:          "bj920580",
			Side:              "buy",
			Rank:              1,
			SourceReportName:  billboard.ReportBuyDetails,
			SourceRowID:       row.name + "-row",
			SourcePayloadHash: row.name + "-hash",
		}); err != nil {
			t.Fatalf("upsert %s seat trade: %v", row.name, err)
		}
	}
	for _, report := range []string{billboard.ReportBuyDetails, billboard.ReportSellDetails} {
		if err := store.UpsertSyncStatus(billboard.SyncStatusRecord{
			TradeDate:   "20260515",
			ReportName:  report,
			Status:      billboard.SyncStatusPassed,
			RowCount:    1,
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("upsert status: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/market/billboard/seat?keyword=机构专用&limit=1", nil)
	rec := httptest.NewRecorder()
	handleMarketBillboardSeat(rec, req)

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
	if len(payload.Data.Items) != 1 || payload.Data.Items[0]["seat_name"] != "机构专用" {
		t.Fatalf("unexpected items: %#v", payload.Data.Items)
	}
	if payload.Data.Freshness.Coverage != billboard.CoverageComplete || payload.Data.Freshness.Watermark != "20260515" {
		t.Fatalf("unexpected freshness: %#v", payload.Data.Freshness)
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
