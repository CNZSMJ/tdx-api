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
)

func TestHandleMarketScreenFallsBackToDailyKlineCloseSnapshot(t *testing.T) {
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

type marketScreenQuoteProvider struct {
	quotes []collectorpkg.QuoteSnapshot
}

func (p *marketScreenQuoteProvider) Quotes(ctx context.Context, codes []string) ([]collectorpkg.QuoteSnapshot, error) {
	return p.quotes, nil
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
