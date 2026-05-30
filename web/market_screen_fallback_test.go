package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/protocol"
)

func TestHandleMarketScreenFallsBackToDailyKlineCloseSnapshot(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	originalClient := client
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
		client = originalClient
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil
	client = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 9340, High: 9400, Low: 9300, Close: 9370, Volume: 594549, Amount: 554217472000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9360, High: 9420, Low: 9300, Close: 9380, Volume: 614950, Amount: 575654656000},
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 11200, High: 11300, Low: 11100, Close: 11200, Volume: 100000, Amount: 112000000000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 11200, High: 11400, Low: 11100, Close: 11300, Volume: 120000, Amount: 135600000000},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/market/screen?limit=10", nil)
	rec := httptest.NewRecorder()

	handleMarketScreen(rec, req)

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Count       int                      `json:"count"`
			Status      string                   `json:"status"`
			DataSource  string                   `json:"data_source"`
			TradingDate string                   `json:"trading_date"`
			List        []map[string]interface{} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, rec.Body.String())
	}
	if payload.Code != 0 {
		t.Fatalf("code = %d, want 0", payload.Code)
	}
	if payload.Data.Count != 2 {
		t.Fatalf("count = %d, want 2; body=%s", payload.Data.Count, rec.Body.String())
	}
	if payload.Data.Status != "closed_snapshot" {
		t.Fatalf("status = %q, want closed_snapshot", payload.Data.Status)
	}
	if payload.Data.DataSource != "daily_kline" {
		t.Fatalf("data_source = %q, want daily_kline", payload.Data.DataSource)
	}
	if payload.Data.TradingDate != "20260429" {
		t.Fatalf("trading_date = %q, want 20260429", payload.Data.TradingDate)
	}
	if payload.Data.List[0]["code"] != "sz000001" {
		t.Fatalf("first code = %v, want sz000001 sorted by change_pct desc", payload.Data.List[0]["code"])
	}
}

func TestHandleMarketScreenExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9360, High: 9420, Low: 9300, Close: 9380, Volume: 614950, Amount: 575654656000},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/market/screen?trading_date=20260428&limit=10", nil)
	rec := httptest.NewRecorder()

	handleMarketScreen(rec, req)

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Count       int                      `json:"count"`
			TradingDate string                   `json:"trading_date"`
			List        []map[string]interface{} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, rec.Body.String())
	}
	if payload.Code != 0 {
		t.Fatalf("code = %d, want 0", payload.Code)
	}
	if payload.Data.TradingDate != "20260428" {
		t.Fatalf("trading_date = %q, want requested exact date 20260428", payload.Data.TradingDate)
	}
	if payload.Data.Count != 0 || len(payload.Data.List) != 0 {
		t.Fatalf("exact date fell back to another day: count=%d list=%v body=%s", payload.Data.Count, payload.Data.List, rec.Body.String())
	}
}

func TestMarketScreenCloseTicksUsesMaterializedSnapshot(t *testing.T) {
	originalDir := databaseDir
	defer func() {
		databaseDir = originalDir
	}()

	tmp := t.TempDir()
	databaseDir = tmp

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 9300, High: 9400, Low: 9200, Close: 9300, Volume: 100, Amount: 930000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9300, High: 9500, Low: 9300, Close: 9500, Volume: 200, Amount: 1900000},
	})

	ticks, tradingDate, ok := loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), "20260429")
	if !ok || len(ticks) != 1 || tradingDate != "20260429" {
		t.Fatalf("first load = len %d date %q ok %v", len(ticks), tradingDate, ok)
	}
	if _, err := os.Stat(filepath.Join(tmp, "market_snapshot.db")); err != nil {
		t.Fatalf("expected materialized snapshot db: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(tmp, "kline")); err != nil {
		t.Fatalf("remove kline dir: %v", err)
	}

	ticks, tradingDate, ok = loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), "20260429")
	if !ok || len(ticks) != 1 || tradingDate != "20260429" {
		t.Fatalf("materialized load = len %d date %q ok %v", len(ticks), tradingDate, ok)
	}
	if ticks[0].Code != "sh600000" || ticks[0].Last != 9.5 {
		t.Fatalf("materialized tick = %#v", ticks[0])
	}
}

func TestMarketScreenCloseTicksRebuildsZeroAmountMaterializedSnapshot(t *testing.T) {
	originalDir := databaseDir
	defer func() {
		databaseDir = originalDir
	}()

	tmp := t.TempDir()
	databaseDir = tmp

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 9300, High: 9400, Low: 9200, Close: 9300, Volume: 100, Amount: 930000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9300, High: 9500, Low: 9300, Close: 9500, Volume: 200, Amount: 1900000},
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 10000, High: 10100, Low: 9900, Close: 10000, Volume: 100, Amount: 1000000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 10000, High: 11000, Low: 10000, Close: 11000, Volume: 300, Amount: 3300000},
	})
	mustCreateZeroAmountMarketSnapshot(t, "20260429")

	ticks, tradingDate, ok := loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), "20260429")
	if !ok || len(ticks) != 2 || tradingDate != "20260429" {
		t.Fatalf("rebuilt load = len %d date %q ok %v", len(ticks), tradingDate, ok)
	}
	var totalAmount float64
	for _, tick := range ticks {
		totalAmount += tick.Amount
	}
	if totalAmount == 0 {
		t.Fatalf("rebuilt ticks still have zero amount: %#v", ticks)
	}

	db, err := openMarketScreenSnapshotDB(true)
	if err != nil {
		t.Fatalf("open snapshot db: %v", err)
	}
	defer db.Close()
	var rows int
	var storedAmount float64
	if err := db.QueryRow(`SELECT COUNT(*), SUM(amount) FROM daily_market_snapshot WHERE trading_date = '20260429' AND asset_type = 'stock'`).Scan(&rows, &storedAmount); err != nil {
		t.Fatalf("query rebuilt snapshot: %v", err)
	}
	if rows != 2 || storedAmount == 0 {
		t.Fatalf("stored rebuilt snapshot rows=%d amount=%v", rows, storedAmount)
	}
}

func TestMarketScreenCloseTicksRebuildsRoundedLimitFlagSnapshot(t *testing.T) {
	originalDir := databaseDir
	defer func() {
		databaseDir = originalDir
	}()

	tmp := t.TempDir()
	databaseDir = tmp

	codesPath := filepath.Join(tmp, "codes.db")
	mustCreateMarketScreenCodesDB(t, codesPath)
	mustInsertMarketScreenCode(t, codesPath, "粤电力A", "000539", "sz")
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000539.db"), "sz000539", []marketScreenKlineFixture{
		{At: time.Date(2026, 5, 27, 15, 0, 0, 0, time.Local), Open: 7340, High: 7340, Low: 7340, Close: 7340, Volume: 1000, Amount: 7340000},
		{At: time.Date(2026, 5, 28, 15, 0, 0, 0, time.Local), Open: 7900, High: 8070, Low: 7730, Close: 8070, Volume: 1495433, Amount: 1198943232000},
	})
	mustCreateRoundedLimitFlagMarketSnapshot(t, "20260528")

	ticks, tradingDate, ok := loadMarketScreenCloseTicks(string(collectorpkg.AssetTypeStock), "20260528")
	if !ok || len(ticks) != 1 || tradingDate != "20260528" {
		t.Fatalf("rebuilt load = len %d date %q ok %v", len(ticks), tradingDate, ok)
	}
	if ticks[0].Code != "sz000539" || ticks[0].IsLimitUp {
		t.Fatalf("rounded stale limit flag was not rebuilt: %#v", ticks[0])
	}
}

func TestMarketScreenUsesTodayTickerBeforeDailyKline(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		{
			Code:       "sh600000",
			Name:       "浦发银行",
			Exchange:   "sh",
			AssetType:  collectorpkg.AssetTypeStock,
			PreClose:   collectorpkg.PriceMilli(9000),
			Open:       collectorpkg.PriceMilli(9100),
			High:       collectorpkg.PriceMilli(9600),
			Low:        collectorpkg.PriceMilli(9000),
			Last:       collectorpkg.PriceMilli(9500),
			VolumeHand: 100,
			AmountYuan: 950000,
		},
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketScreenRequest(httptest.NewRequest(http.MethodGet, "/api/market/screen?limit=10", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketScreenTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["data_source"] != "ticker" {
		t.Fatalf("data_source = %v, want ticker", resp["data_source"])
	}
	if resp["trading_date"] != "20260429" {
		t.Fatalf("trading_date = %v, want 20260429", resp["trading_date"])
	}
	if resp["count"] != 1 {
		t.Fatalf("count = %v, want 1; resp=%#v", resp["count"], resp)
	}
}

func TestMarketScreenTickerPaginationReturnsMetadata(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "浦发银行", 13000),
		marketScreenTestQuote("sz000001", "平安银行", 12000),
		marketScreenTestQuote("sz000002", "万科A", 11000),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000", "sz000001", "sz000002"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketScreenRequest(httptest.NewRequest(http.MethodGet, "/api/market/screen?limit=2&page=2", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketScreenTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["count"] != 1 {
		t.Fatalf("count = %v, want page count 1; resp=%#v", resp["count"], resp)
	}
	if resp["total"] != 3 {
		t.Fatalf("total = %v, want 3; resp=%#v", resp["total"], resp)
	}
	if resp["page"] != 2 || resp["page_size"] != 2 || resp["total_pages"] != 2 {
		t.Fatalf("pagination metadata mismatch: %#v", resp)
	}
	if resp["has_next"] != false || resp["has_prev"] != true {
		t.Fatalf("pagination booleans mismatch: %#v", resp)
	}
	list := resp["list"].([]map[string]interface{})
	if list[0]["code"] != "sz000002" {
		t.Fatalf("second page first code = %v, want sz000002", list[0]["code"])
	}
}

func TestMarketScreenTickerExcludeST(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "ST测试", 13000),
		marketScreenTestQuote("sz000001", "平安银行", 12000),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000", "sz000001"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketScreenRequest(httptest.NewRequest(http.MethodGet, "/api/market/screen?exclude_st=true&limit=10", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketScreenTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["count"] != 1 || resp["total"] != 1 {
		t.Fatalf("count/total = %v/%v, want 1/1; resp=%#v", resp["count"], resp["total"], resp)
	}
	list := resp["list"].([]map[string]interface{})
	if list[0]["code"] != "sz000001" {
		t.Fatalf("remaining code = %v, want sz000001", list[0]["code"])
	}
}

func TestMarketScreenTickerFiltersChangePctRange(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "浦发银行", 13000),
		marketScreenTestQuote("sz000001", "平安银行", 12000),
		marketScreenTestQuote("sz000002", "万科A", 11000),
		marketScreenTestQuote("sz000003", "回撤测试", 9400),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000", "sz000001", "sz000002", "sz000003"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketScreenRequest(httptest.NewRequest(http.MethodGet, "/api/market/screen?min_change_pct=15&max_change_pct=25&limit=10", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketScreenTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["count"] != 1 || resp["total"] != 1 {
		t.Fatalf("count/total = %v/%v, want 1/1; resp=%#v", resp["count"], resp["total"], resp)
	}
	list := resp["list"].([]map[string]interface{})
	if list[0]["code"] != "sz000001" {
		t.Fatalf("range filter code = %v, want sz000001", list[0]["code"])
	}

	req, err = parseMarketScreenRequest(httptest.NewRequest(http.MethodGet, "/api/market/screen?max_change_pct=-5&limit=10", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok = buildMarketScreenTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["count"] != 1 || resp["total"] != 1 {
		t.Fatalf("downside count/total = %v/%v, want 1/1; resp=%#v", resp["count"], resp["total"], resp)
	}
	list = resp["list"].([]map[string]interface{})
	if list[0]["code"] != "sz000003" {
		t.Fatalf("downside filter code = %v, want sz000003", list[0]["code"])
	}
}

func TestMarketScreenLimitThresholdTreatsBJStocksAs30Percent(t *testing.T) {
	if got := marketScreenLimitThreshold("bj920725", "族兴新材"); got != 30 {
		t.Fatalf("marketScreenLimitThreshold(bj920725) = %v, want 30", got)
	}
	if got := marketScreenLimitUp(13.58, "bj920725", "族兴新材"); got {
		t.Fatalf("marketScreenLimitUp(13.58, bj920725) = %v, want false", got)
	}
}

func TestMarketScreenCloseSnapshotUsesRawPriceForLimitDetection(t *testing.T) {
	code := marketScreenCodeRow{
		fullCode:  "sz000539",
		name:      "粤电力A",
		exchange:  "sz",
		assetType: string(collectorpkg.AssetTypeStock),
	}
	latest := struct {
		date   int64
		open   collectorpkg.PriceMilli
		high   collectorpkg.PriceMilli
		low    collectorpkg.PriceMilli
		close  collectorpkg.PriceMilli
		volume int64
		amount collectorpkg.PriceMilli
	}{
		date:   time.Date(2026, 5, 28, 15, 0, 0, 0, time.Local).Unix(),
		open:   7900,
		high:   8070,
		low:    7730,
		close:  8070,
		volume: 1495433,
		amount: 1198943232000,
	}
	previous := struct {
		date   int64
		open   collectorpkg.PriceMilli
		high   collectorpkg.PriceMilli
		low    collectorpkg.PriceMilli
		close  collectorpkg.PriceMilli
		volume int64
		amount collectorpkg.PriceMilli
	}{
		date:  time.Date(2026, 5, 27, 15, 0, 0, 0, time.Local).Unix(),
		close: 7340,
	}

	snapshot := buildMarketScreenCloseSnapshot(code, latest, previous, true)
	if snapshot.tick.PctChange != 9.95 {
		t.Fatalf("pct_change = %v, want rounded display value 9.95", snapshot.tick.PctChange)
	}
	if snapshot.tick.IsLimitUp {
		t.Fatalf("rounded display pct must not make raw 9.9455%% move a limit-up: %#v", snapshot.tick)
	}
}

func TestMarketScreenQuoteSnapshotUsesQuotePrevClose(t *testing.T) {
	originalDir := databaseDir
	defer func() {
		databaseDir = originalDir
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))

	fetcher := func(codes ...string) (protocol.QuotesResp, error) {
		return protocol.QuotesResp{
			&protocol.Quote{
				Code: "600000",
				K: protocol.K{
					Last:  protocol.Price(11000),
					Open:  protocol.Price(0),
					High:  protocol.Price(0),
					Low:   protocol.Price(0),
					Close: protocol.Price(0),
				},
				TotalHand: 0,
			},
			&protocol.Quote{
				Code: "000001",
				K: protocol.K{
					Last:  protocol.Price(9720),
					Open:  protocol.Price(9800),
					High:  protocol.Price(10030),
					Low:   protocol.Price(9710),
					Close: protocol.Price(9900),
				},
				TotalHand: 20158,
				Amount:    19885918,
			},
		}, nil
	}

	ticks, _, _, ok := loadMarketScreenQuoteSnapshot(marketScreenRequest{
		sortBy:    "change_pct",
		order:     "asc",
		assetType: string(collectorpkg.AssetTypeStock),
		limit:     10,
	}, fetcher)
	if !ok || len(ticks) != 1 {
		t.Fatalf("quote snapshot count = %d ok=%v, want 1 true", len(ticks), ok)
	}
	if ticks[0].PctChange != 1.85 {
		t.Fatalf("pct_change = %v, want 1.85", ticks[0].PctChange)
	}
	if ticks[0].IsLimitDown {
		t.Fatalf("quote snapshot marked limit down: %#v", ticks[0])
	}

	_, _, _, ok = loadMarketScreenQuoteSnapshot(marketScreenRequest{
		filter:    "limit_down",
		sortBy:    "change_pct",
		order:     "asc",
		assetType: string(collectorpkg.AssetTypeStock),
		limit:     10,
	}, fetcher)
	if ok {
		t.Fatalf("limit_down filter should not include quote snapshot with positive change")
	}
}

func TestMarketStatsExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9360, High: 9420, Low: 9300, Close: 9380, Volume: 614950, Amount: 575654656000},
	})

	payload := callMarketStatsHandler(t, "/api/market-stats?trading_date=20260428")
	data := payload["data"].(map[string]interface{})
	summary := data["summary"].(map[string]interface{})
	stock := summary["stock"].(map[string]interface{})

	if data["trading_date"] != "20260428" {
		t.Fatalf("trading_date = %v, want requested exact date 20260428", data["trading_date"])
	}
	if stock["total"].(float64) != 0 {
		t.Fatalf("exact date fell back to another day: stock.total=%v data=%#v", stock["total"], data)
	}
}

func TestMarketStatsUsesTodayTickerBeforeDailyKline(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		{
			Code:       "sh600000",
			Name:       "浦发银行",
			Exchange:   "sh",
			AssetType:  collectorpkg.AssetTypeStock,
			PreClose:   collectorpkg.PriceMilli(9000),
			Open:       collectorpkg.PriceMilli(9100),
			High:       collectorpkg.PriceMilli(9600),
			Low:        collectorpkg.PriceMilli(9000),
			Last:       collectorpkg.PriceMilli(9500),
			VolumeHand: 100,
			AmountYuan: 950000,
		},
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketStatsRequest(httptest.NewRequest(http.MethodGet, "/api/market-stats", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketStatsTickerResponse(req, ts)
	if !ok {
		t.Fatalf("ticker response not selected for current trading day")
	}
	if resp["data_source"] != "ticker" {
		t.Fatalf("data_source = %v, want ticker", resp["data_source"])
	}
	if resp["trading_date"] != "20260429" {
		t.Fatalf("trading_date = %v, want 20260429", resp["trading_date"])
	}
	summary := resp["summary"].(map[string]interface{})
	stock := summary["stock"].(map[string]interface{})
	if stock["total"] != 1 {
		t.Fatalf("stock.total = %v, want 1; resp=%#v", stock["total"], resp)
	}
}

func TestMarketLimitStatsClassifiesLimitBoards(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	quotes := []collectorpkg.QuoteSnapshot{
		marketScreenTestQuoteOHLC("sh600010", "一字涨停", 11000, 11000, 11000, 11000),
		marketScreenTestQuoteOHLC("sh600011", "T字涨停", 11000, 11000, 10000, 11000),
		marketScreenTestQuoteOHLC("sh600012", "换手涨停", 10000, 11000, 10000, 11000),
		marketScreenTestQuoteOHLC("sh600013", "地天板", 9000, 11000, 9000, 11000),
		marketScreenTestQuoteOHLC("sh600014", "涨停炸板", 10000, 11000, 10000, 10500),
		marketScreenTestQuoteOHLC("sh600015", "一字跌停", 9000, 9000, 9000, 9000),
		marketScreenTestQuoteOHLC("sh600016", "倒T跌停", 9000, 10000, 9000, 9000),
		marketScreenTestQuoteOHLC("sh600017", "换手跌停", 10000, 10000, 9000, 9000),
		marketScreenTestQuoteOHLC("sh600018", "天地板", 11000, 11000, 9000, 9000),
		marketScreenTestQuoteOHLC("sh600019", "跌停撬板", 10000, 10000, 9000, 9500),
		marketScreenTestQuoteOHLC("sh600020", "ST涨停", 10500, 10500, 10500, 10500),
		marketScreenTestQuoteOHLC("sh600021", "ST跌停", 9500, 9500, 9500, 9500),
	}
	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: quotes}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{
		"sh600010", "sh600011", "sh600012", "sh600013", "sh600014",
		"sh600015", "sh600016", "sh600017", "sh600018", "sh600019",
		"sh600020", "sh600021",
	}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	resp, ok := buildMarketLimitStatsTickerResponse(marketLimitStatsRequest{}, ts)
	if !ok {
		t.Fatalf("ticker limit-stats response not selected")
	}
	limitUp := resp["limit_up"].(map[string]interface{})
	if limitUp["total"] != 5 || limitUp["one_line"] != 2 || limitUp["t_board"] != 1 || limitUp["turnover_board"] != 2 {
		t.Fatalf("limit_up classification mismatch: %#v", limitUp)
	}
	if limitUp["floor_sky"] != 1 || limitUp["broken"] != 2 {
		t.Fatalf("limit_up event mismatch: %#v", limitUp)
	}
	limitDown := resp["limit_down"].(map[string]interface{})
	if limitDown["total"] != 5 || limitDown["one_line"] != 2 || limitDown["t_board"] != 1 || limitDown["turnover_board"] != 2 {
		t.Fatalf("limit_down classification mismatch: %#v", limitDown)
	}
	if limitDown["sky_floor"] != 1 || limitDown["broken"] != 2 {
		t.Fatalf("limit_down event mismatch: %#v", limitDown)
	}
	byStockClass, ok := resp["by_stock_class"].(map[string]interface{})
	if !ok {
		t.Fatalf("by_stock_class missing: %#v", resp)
	}
	nonST := byStockClass["non_st"].(map[string]interface{})
	nonSTLimitUp := nonST["limit_up"].(map[string]interface{})
	if nonSTLimitUp["total"] != 4 || nonSTLimitUp["one_line"] != 1 || nonSTLimitUp["turnover_board"] != 2 {
		t.Fatalf("non_st.limit_up mismatch: %#v", nonSTLimitUp)
	}
	nonSTLimitDown := nonST["limit_down"].(map[string]interface{})
	if nonSTLimitDown["total"] != 4 || nonSTLimitDown["one_line"] != 1 || nonSTLimitDown["turnover_board"] != 2 {
		t.Fatalf("non_st.limit_down mismatch: %#v", nonSTLimitDown)
	}
	st := byStockClass["st"].(map[string]interface{})
	stLimitUp := st["limit_up"].(map[string]interface{})
	if stLimitUp["total"] != 1 || stLimitUp["one_line"] != 1 || stLimitUp["turnover_board"] != 0 {
		t.Fatalf("st.limit_up mismatch: %#v", stLimitUp)
	}
	stLimitDown := st["limit_down"].(map[string]interface{})
	if stLimitDown["total"] != 1 || stLimitDown["one_line"] != 1 || stLimitDown["turnover_board"] != 0 {
		t.Fatalf("st.limit_down mismatch: %#v", stLimitDown)
	}
}

func TestMarketLimitStatsFallsBackToDailyKlineCloseSnapshot(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260428", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260429", 11000, 11000, 11000, 11000),
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260428", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260429", 9000, 9000, 9000, 9000),
	})

	payload := callMarketScreenHandler(t, "/api/market/limit-stats", handleMarketLimitStats)
	data := payload["data"].(map[string]interface{})
	if data["status"] != "closed_snapshot" || data["data_source"] != "daily_kline" {
		t.Fatalf("limit-stats fallback metadata = %#v", data)
	}
	if data["trading_date"] != "20260429" {
		t.Fatalf("trading_date = %v, want 20260429", data["trading_date"])
	}
	limitUp := data["limit_up"].(map[string]interface{})
	if limitUp["total"].(float64) != 1 || limitUp["one_line"].(float64) != 1 {
		t.Fatalf("limit_up = %#v, want one-line total 1", limitUp)
	}
	limitDown := data["limit_down"].(map[string]interface{})
	if limitDown["total"].(float64) != 1 || limitDown["one_line"].(float64) != 1 {
		t.Fatalf("limit_down = %#v, want one-line total 1", limitDown)
	}
}

func TestMarketLimitStatsExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260429", 11000, 11000, 11000, 11000),
	})

	payload := callMarketScreenHandler(t, "/api/market/limit-stats?trading_date=20260428", handleMarketLimitStats)
	data := payload["data"].(map[string]interface{})
	if data["trading_date"] != "20260428" {
		t.Fatalf("trading_date = %v, want exact 20260428", data["trading_date"])
	}
	if data["status"] != "empty" {
		t.Fatalf("status = %v, want empty; data=%#v", data["status"], data)
	}
	limitUp := data["limit_up"].(map[string]interface{})
	if limitUp["total"].(float64) != 0 {
		t.Fatalf("exact date fell back: limit_up.total=%v data=%#v", limitUp["total"], data)
	}
}

func TestMarketLimitStatsSkipsStaleTicker(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	currentNow := time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	marketScreenNow = func() time.Time {
		return currentNow
	}

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuoteOHLC("sh600000", "浦发银行", 10000, 11000, 10000, 11000),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	currentNow = time.Date(2026, 4, 29, 16, 0, 0, 0, time.Local)
	req := marketLimitStatsRequest{}
	if resp, ok := buildMarketLimitStatsTickerResponse(req, ts); ok {
		t.Fatalf("stale ticker should not be selected: %#v", resp)
	}
}

func TestMarketLimitUpTiersUsesDailyKlineStreaks(t *testing.T) {
	originalDir := databaseDir
	defer func() {
		databaseDir = originalDir
	}()

	tmp := t.TempDir()
	databaseDir = tmp

	codesPath := filepath.Join(tmp, "codes.db")
	mustCreateMarketScreenCodesDB(t, codesPath)
	mustInsertMarketScreenCode(t, codesPath, "ST示例", "600001", "sh")

	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260424", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260427", 11000, 11000, 11000, 11000),
		marketLimitUpTierKlineFixture("20260428", 12100, 12100, 12100, 12100),
		marketLimitUpTierKlineFixture("20260429", 13310, 13310, 13310, 13310),
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260427", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260428", 11000, 11000, 11000, 11000),
		marketLimitUpTierKlineFixture("20260429", 12100, 12100, 12100, 12100),
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600001.db"), "sh600001", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260427", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260428", 10500, 10500, 10500, 10500),
		marketLimitUpTierKlineFixture("20260429", 11025, 11025, 11025, 11025),
	})

	payload := callMarketScreenHandler(t, "/api/market/limit-up/tiers?trading_date=20260429&min_streak=2", handleMarketLimitUpTiers)
	data := payload["data"].(map[string]interface{})
	if _, ok := data["data_source"]; ok {
		t.Fatalf("limit-up tiers response should not include data_source: %#v", data)
	}
	if data["trading_date"] != "20260429" || data["stock_class"] != "non_st" {
		t.Fatalf("metadata mismatch: %#v", data)
	}
	if data["total"].(float64) != 2 || data["highest_streak"].(float64) != 3 {
		t.Fatalf("tier summary mismatch: %#v", data)
	}
	tiers := data["tiers"].([]interface{})
	if len(tiers) != 2 {
		t.Fatalf("tier count = %d, want 2; data=%#v", len(tiers), data)
	}
	firstTier := tiers[0].(map[string]interface{})
	if firstTier["streak"].(float64) != 3 || firstTier["count"].(float64) != 1 {
		t.Fatalf("first tier mismatch: %#v", firstTier)
	}
	firstStocks := firstTier["stocks"].([]interface{})
	firstStock := firstStocks[0].(map[string]interface{})
	if firstStock["code"] != "sh600000" || firstStock["first_limit_date"] != "20260427" || firstStock["board_type"] != "one_line" {
		t.Fatalf("first tier stock mismatch: %#v", firstStock)
	}

	payload = callMarketScreenHandler(t, "/api/market/limit-up/tiers?trading_date=20260429&stock_class=st&min_streak=2", handleMarketLimitUpTiers)
	data = payload["data"].(map[string]interface{})
	if data["stock_class"] != "st" || data["total"].(float64) != 1 {
		t.Fatalf("st tier summary mismatch: %#v", data)
	}
	tiers = data["tiers"].([]interface{})
	stStocks := tiers[0].(map[string]interface{})["stocks"].([]interface{})
	stStock := stStocks[0].(map[string]interface{})
	if stStock["code"] != "sh600001" || stStock["is_st"] != true {
		t.Fatalf("st stock mismatch: %#v", stStock)
	}
}

func TestMarketLimitUpTiersUsesTickerForToday(t *testing.T) {
	originalDir := databaseDir
	originalNow := marketScreenNow
	defer func() {
		databaseDir = originalDir
		marketScreenNow = originalNow
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	marketScreenNow = func() time.Time {
		return time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	}

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		marketLimitUpTierKlineFixture("20260424", 10000, 10000, 10000, 10000),
		marketLimitUpTierKlineFixture("20260427", 11000, 11000, 11000, 11000),
		marketLimitUpTierKlineFixture("20260428", 12100, 12100, 12100, 12100),
	})

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuoteOHLC("sh600000", "浦发银行", 13310, 13310, 12100, 13310),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	req, err := parseMarketLimitUpTiersRequest(httptest.NewRequest(http.MethodGet, "/api/market/limit-up/tiers?min_streak=2", nil))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	resp, ok := buildMarketLimitUpTiersResponse(req, ts)
	if !ok {
		t.Fatalf("ticker limit-up tiers response not selected")
	}
	if _, ok := resp["data_source"]; ok {
		t.Fatalf("ticker limit-up tiers response should not include data_source: %#v", resp)
	}
	if resp["trading_date"] != "20260429" || resp["total"] != 1 || resp["highest_streak"] != 3 {
		t.Fatalf("ticker tier summary mismatch: %#v", resp)
	}
	tiers := resp["tiers"].([]marketLimitUpTier)
	stock := tiers[0].Stocks[0]
	if stock.Code != "sh600000" || stock.Streak != 3 || stock.FirstLimitDate != "20260427" {
		t.Fatalf("ticker tier stock mismatch: %#v", stock)
	}
	if stock.LimitFirstSeen == nil || *stock.LimitFirstSeen != "10:00:00" {
		t.Fatalf("ticker limit_first_seen mismatch: %#v", stock)
	}
	if stock.LimitBreakCount == nil || *stock.LimitBreakCount != 0 {
		t.Fatalf("ticker limit_break_count mismatch: %#v", stock)
	}
}

func TestBlockRankingAndStocksFallbackToDailyKlineCloseSnapshot(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 9340, High: 9400, Low: 9300, Close: 9370, Volume: 594549, Amount: 554217472000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9360, High: 9420, Low: 9300, Close: 9380, Volume: 614950, Amount: 575654656000},
	})
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 28, 15, 0, 0, 0, time.Local), Open: 11200, High: 11300, Low: 11100, Close: 11200, Volume: 100000, Amount: 112000000000},
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 11200, High: 11400, Low: 11100, Close: 11300, Volume: 120000, Amount: 135600000000},
	})
	mustCreateMarketScreenBlockDB(t, filepath.Join(tmp, "block", "blocks.db"), []string{"600000", "000001"})

	ranking := callMarketScreenHandler(t, "/api/block/ranking?source=block_gn.dat&block_type=concept&limit=10", handleBlockRanking)
	rankingData := ranking["data"].(map[string]interface{})
	if rankingData["status"] != "closed_snapshot" || rankingData["data_source"] != "daily_kline" {
		t.Fatalf("block ranking fallback metadata = %#v", rankingData)
	}
	if rankingData["trading_date"] != "20260429" {
		t.Fatalf("block ranking trading_date = %v, want 20260429", rankingData["trading_date"])
	}
	if rankingData["count"].(float64) != 1 {
		t.Fatalf("block ranking count = %v, want 1; data=%#v", rankingData["count"], rankingData)
	}

	stocks := callMarketScreenHandler(t, "/api/block/stocks?source=block_gn.dat&block_type=concept&name=复盘测试&limit=10", handleBlockStocks)
	stocksData := stocks["data"].(map[string]interface{})
	if stocksData["status"] != "closed_snapshot" || stocksData["data_source"] != "daily_kline" {
		t.Fatalf("block stocks fallback metadata = %#v", stocksData)
	}
	if stocksData["trading_date"] != "20260429" {
		t.Fatalf("block stocks trading_date = %v, want 20260429", stocksData["trading_date"])
	}
	if stocksData["count"].(float64) != 2 {
		t.Fatalf("block stocks count = %v, want 2; data=%#v", stocksData["count"], stocksData)
	}
}

func TestBlockRankingAndStocksExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", []marketScreenKlineFixture{
		{At: time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local), Open: 9360, High: 9420, Low: 9300, Close: 9380, Volume: 614950, Amount: 575654656000},
	})
	mustCreateMarketScreenBlockDB(t, filepath.Join(tmp, "block", "blocks.db"), []string{"600000"})

	ranking := callMarketScreenHandler(t, "/api/block/ranking?source=block_gn.dat&block_type=concept&trading_date=20260428&limit=10", handleBlockRanking)
	rankingData := ranking["data"].(map[string]interface{})
	if rankingData["trading_date"] != "20260428" {
		t.Fatalf("block ranking trading_date = %v, want exact 20260428", rankingData["trading_date"])
	}
	if rankingData["count"].(float64) != 0 {
		t.Fatalf("block ranking exact date fell back: count=%v data=%#v", rankingData["count"], rankingData)
	}

	stocks := callMarketScreenHandler(t, "/api/block/stocks?source=block_gn.dat&block_type=concept&name=复盘测试&trading_date=20260428&limit=10", handleBlockStocks)
	stocksData := stocks["data"].(map[string]interface{})
	if stocksData["trading_date"] != "20260428" {
		t.Fatalf("block stocks trading_date = %v, want exact 20260428", stocksData["trading_date"])
	}
	if stocksData["count"].(float64) != 0 {
		t.Fatalf("block stocks exact date fell back: count=%v data=%#v", stocksData["count"], stocksData)
	}
}

func TestMarketSignalFallsBackToDailyKlineCloseSnapshot(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	latest := time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local)
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", marketScreenSignalKlines(latest))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", marketScreenSignalKlines(latest))

	payload := callMarketScreenHandler(t, "/api/market/signal?type=all", handleMarketSignal)
	data := payload["data"].(map[string]interface{})

	if data["status"] != "closed_snapshot" || data["data_source"] != "daily_kline" {
		t.Fatalf("signal fallback metadata = %#v", data)
	}
	if data["trading_date"] != "20260429" {
		t.Fatalf("signal trading_date = %v, want 20260429", data["trading_date"])
	}
	if data["count"].(float64) != 4 {
		t.Fatalf("signal count = %v, want 4; data=%#v", data["count"], data)
	}
}

func TestMarketSignalExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	latest := time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local)
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", marketScreenSignalKlines(latest))

	payload := callMarketScreenHandler(t, "/api/market/signal?type=all&trading_date=20260331", handleMarketSignal)
	data := payload["data"].(map[string]interface{})

	if data["trading_date"] != "20260331" {
		t.Fatalf("signal trading_date = %v, want exact 20260331", data["trading_date"])
	}
	if data["count"].(float64) != 0 {
		t.Fatalf("signal exact date fell back: count=%v data=%#v", data["count"], data)
	}
}

func TestMarketSignalCheckFallsBackToDailyKlineCloseSnapshot(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	latest := time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local)
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", marketScreenSignalKlines(latest))
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sz000001.db"), "sz000001", marketScreenSignalKlines(latest))

	payload := callMarketScreenHandler(t, "/api/market/signal/check?full_codes=sh600000,sz000001&signal_types=new_high,volume_spike&mode=full", handleMarketSignalCheck)
	data := payload["data"].(map[string]interface{})

	if data["status"] != "closed_snapshot" || data["data_source"] != "daily_kline" {
		t.Fatalf("signal check fallback metadata = %#v", data)
	}
	if data["trading_date"] != "20260429" {
		t.Fatalf("signal check trading_date = %v, want 20260429", data["trading_date"])
	}
	if data["count"].(float64) != 4 {
		t.Fatalf("signal check count = %v, want 4; data=%#v", data["count"], data)
	}
}

func TestMarketSignalCheckExactTradingDateDoesNotFallback(t *testing.T) {
	originalDir := databaseDir
	originalRuntime := collectorRuntime
	defer func() {
		databaseDir = originalDir
		collectorRuntime = originalRuntime
	}()

	tmp := t.TempDir()
	databaseDir = tmp
	collectorRuntime = nil

	mustCreateMarketScreenCodesDB(t, filepath.Join(tmp, "codes.db"))
	latest := time.Date(2026, 4, 29, 15, 0, 0, 0, time.Local)
	mustCreateMarketScreenKlineDB(t, filepath.Join(tmp, "kline", "sh600000.db"), "sh600000", marketScreenSignalKlines(latest))

	payload := callMarketScreenHandler(t, "/api/market/signal/check?full_codes=sh600000&signal_types=new_high,volume_spike&mode=hits_only&trading_date=20260331", handleMarketSignalCheck)
	data := payload["data"].(map[string]interface{})

	if data["trading_date"] != "20260331" {
		t.Fatalf("signal check trading_date = %v, want exact 20260331", data["trading_date"])
	}
	if data["count"].(float64) != 0 {
		t.Fatalf("signal check exact date fell back: count=%v data=%#v", data["count"], data)
	}
}

type marketScreenQuoteProvider struct {
	quotes []collectorpkg.QuoteSnapshot
}

func (p *marketScreenQuoteProvider) Quotes(ctx context.Context, codes []string) ([]collectorpkg.QuoteSnapshot, error) {
	return p.quotes, nil
}

func marketScreenTestQuote(code, name string, last collectorpkg.PriceMilli) collectorpkg.QuoteSnapshot {
	return marketScreenTestQuoteOHLC(code, name, collectorpkg.PriceMilli(10000), last, collectorpkg.PriceMilli(10000), last)
}

func marketScreenTestQuoteOHLC(code, name string, open, high, low, last collectorpkg.PriceMilli) collectorpkg.QuoteSnapshot {
	return collectorpkg.QuoteSnapshot{
		Code:       code,
		Name:       name,
		Exchange:   code[:2],
		AssetType:  collectorpkg.AssetTypeStock,
		PreClose:   collectorpkg.PriceMilli(10000),
		Open:       open,
		High:       high,
		Low:        low,
		Last:       last,
		VolumeHand: 100,
		AmountYuan: 100000,
	}
}

func waitForMarketScreenTicker(t *testing.T, ts *collectorpkg.TickerService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !ts.UpdatedAt().IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ticker did not publish initial snapshot")
}

func callMarketStatsHandler(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	handleGetMarketStats(rec, req)
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v\nbody=%s", path, err, rec.Body.String())
	}
	if payload["code"].(float64) != 0 {
		t.Fatalf("%s returned error payload: %s", path, rec.Body.String())
	}
	return payload
}

func callMarketScreenHandler(t *testing.T, path string, handler http.HandlerFunc) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	handler(rec, req)
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v\nbody=%s", path, err, rec.Body.String())
	}
	if payload["code"].(float64) != 0 {
		t.Fatalf("%s returned error payload: %s", path, rec.Body.String())
	}
	return payload
}

type marketScreenKlineFixture struct {
	At     time.Time
	Open   int64
	High   int64
	Low    int64
	Close  int64
	Volume int64
	Amount int64
}

func mustCreateMarketScreenCodesDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open codes db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE codes (ID INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, Name TEXT NULL, Code TEXT NULL, Exchange TEXT NULL, Multiple INTEGER NULL, Decimal INTEGER NULL, LastPrice REAL NULL, EditDate INTEGER NULL, InDate INTEGER NULL)`); err != nil {
		t.Fatalf("create codes table: %v", err)
	}
	rows := []struct {
		name     string
		code     string
		exchange string
	}{
		{name: "浦发银行", code: "600000", exchange: "sh"},
		{name: "平安银行", code: "000001", exchange: "sz"},
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO codes(Name, Code, Exchange) VALUES(?, ?, ?)`, row.name, row.code, row.exchange); err != nil {
			t.Fatalf("insert code %s%s: %v", row.exchange, row.code, err)
		}
	}
}

func mustInsertMarketScreenCode(t *testing.T, path, name, code, exchange string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open codes db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO codes(Name, Code, Exchange) VALUES(?, ?, ?)`, name, code, exchange); err != nil {
		t.Fatalf("insert code %s%s: %v", exchange, code, err)
	}
}

func mustCreateMarketScreenKlineDB(t *testing.T, path, code string, rows []marketScreenKlineFixture) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir kline dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open kline db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE DayKline (Code TEXT NOT NULL, Date INTEGER NOT NULL, Open INTEGER NOT NULL, High INTEGER NOT NULL, Low INTEGER NOT NULL, Close INTEGER NOT NULL, Volume INTEGER NOT NULL, Amount INTEGER NOT NULL, InDate INTEGER NULL)`); err != nil {
		t.Fatalf("create DayKline table: %v", err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO DayKline(Code, Date, Open, High, Low, Close, Volume, Amount) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, code, row.At.Unix(), row.Open, row.High, row.Low, row.Close, row.Volume, row.Amount); err != nil {
			t.Fatalf("insert kline %s: %v", code, err)
		}
	}
}

func mustCreateZeroAmountMarketSnapshot(t *testing.T, tradingDate string) {
	t.Helper()
	db, err := openMarketScreenSnapshotDB(false)
	if err != nil {
		t.Fatalf("open snapshot db: %v", err)
	}
	defer db.Close()
	if err := ensureMarketScreenSnapshotSchema(db); err != nil {
		t.Fatalf("ensure snapshot schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO daily_market_snapshot(trading_date, full_code, name, exchange, asset_type, open_price, high_price, low_price, last_price, pre_close, change_pct, price_change, volume, amount, amplitude, is_limit_up, is_limit_down, updated_at) VALUES(?, 'sh600000', '浦发银行', 'sh', 'stock', 9.3, 9.3, 9.3, 9.3, 9.3, 0, 0, 0, 0, 0, 0, 0, ?)`, tradingDate, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert zero snapshot: %v", err)
	}
}

func mustCreateRoundedLimitFlagMarketSnapshot(t *testing.T, tradingDate string) {
	t.Helper()
	db, err := openMarketScreenSnapshotDB(false)
	if err != nil {
		t.Fatalf("open snapshot db: %v", err)
	}
	defer db.Close()
	if err := ensureMarketScreenSnapshotSchema(db); err != nil {
		t.Fatalf("ensure snapshot schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO daily_market_snapshot(trading_date, full_code, name, exchange, asset_type, open_price, high_price, low_price, last_price, pre_close, change_pct, price_change, volume, amount, amplitude, is_limit_up, is_limit_down, updated_at) VALUES(?, 'sz000539', '粤电力A', 'sz', 'stock', 7.9, 8.07, 7.73, 8.07, 7.34, 9.95, 0.73, 1495433, 1198943232, 4.63, 1, 0, ?)`, tradingDate, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert rounded limit snapshot: %v", err)
	}
}

func marketLimitUpTierKlineFixture(date string, open, high, low, close int64) marketScreenKlineFixture {
	at, err := time.ParseInLocation("20060102", date, time.Local)
	if err != nil {
		panic(err)
	}
	return marketScreenKlineFixture{
		At:     time.Date(at.Year(), at.Month(), at.Day(), 15, 0, 0, 0, time.Local),
		Open:   open,
		High:   high,
		Low:    low,
		Close:  close,
		Volume: 100,
		Amount: close * 100,
	}
}

func marketScreenSignalKlines(latest time.Time) []marketScreenKlineFixture {
	rows := make([]marketScreenKlineFixture, 0, 26)
	start := latest.AddDate(0, 0, -25)
	for i := 0; i < 25; i++ {
		rows = append(rows, marketScreenKlineFixture{
			At:     start.AddDate(0, 0, i),
			Open:   10000,
			High:   11000,
			Low:    9000,
			Close:  10000 + int64(i),
			Volume: 100,
			Amount: 100000000,
		})
	}
	rows = append(rows, marketScreenKlineFixture{
		At:     latest,
		Open:   12000,
		High:   13000,
		Low:    11900,
		Close:  12500,
		Volume: 1000,
		Amount: 1250000000,
	})
	return rows
}

func mustCreateMarketScreenBlockDB(t *testing.T, path string, codes []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir block dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open block db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE block_group (ID INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, Name TEXT NOT NULL, BlockType TEXT NOT NULL, Source TEXT NOT NULL, StockCount INTEGER NOT NULL, UpdatedAt DATETIME NOT NULL)`); err != nil {
		t.Fatalf("create block_group: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE block_member (ID INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, Source TEXT NOT NULL, BlockName TEXT NOT NULL, BlockType TEXT NOT NULL, Code TEXT NOT NULL, UpdatedAt DATETIME NOT NULL)`); err != nil {
		t.Fatalf("create block_member: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO block_group(Name, BlockType, Source, StockCount, UpdatedAt) VALUES('复盘测试', 'concept', 'block_gn.dat', ?, ?)`, len(codes), time.Now()); err != nil {
		t.Fatalf("insert block_group: %v", err)
	}
	for _, code := range codes {
		if _, err := db.Exec(`INSERT INTO block_member(Source, BlockName, BlockType, Code, UpdatedAt) VALUES('block_gn.dat', '复盘测试', 'concept', ?, ?)`, code, time.Now()); err != nil {
			t.Fatalf("insert block_member: %v", err)
		}
	}
}
