package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	tdx "github.com/injoyai/tdx"
)

func TestHandleTradingInstrumentMetricsUsesLocalDailyBars(t *testing.T) {
	originalDir := databaseDir
	originalClient := client
	originalCodes := tdx.DefaultCodes
	defer func() {
		databaseDir = originalDir
		client = originalClient
		tdx.DefaultCodes = originalCodes
	}()
	databaseDir = t.TempDir()
	client = nil
	tdx.DefaultCodes = nil

	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	mustCreateTradingMetricDailyKlineDB(t, filepath.Join(databaseDir, "kline", "sz300394.db"), "sz300394", asOf, []int64{10000, 12000, 14000, 16000, 18000, 20000}, []int64{100000, 100000, 100000, 100000, 200000, 200000})
	mustCreateTradingMetricDailyKlineDB(t, filepath.Join(databaseDir, "kline", "sz399006.db"), "sz399006", asOf, []int64{100000, 104000, 108000, 112000, 116000, 120000}, []int64{1000000, 1000000, 1000000, 1000000, 1000000, 1000000})

	body := bytes.NewBufferString(`{
		"market":"CN",
		"as_of_trade_date":"2026-05-26",
		"full_codes":["sz300394"],
		"metric_codes":["PCT_5D","AMOUNT_TREND_5D","VS_INDEX_PP"]
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/instrument-metrics", body)
	handleTradingInstrumentMetrics(rec, req)

	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Items []struct {
				FullCode  string `json:"full_code"`
				Symbol    string `json:"symbol"`
				Benchmark struct {
					IndexCode string `json:"index_code"`
				} `json:"benchmark"`
				Metrics map[string]struct {
					Value        any    `json:"value"`
					Availability string `json:"availability"`
				} `json:"metrics"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if payload.Code != 0 || payload.Message != "success" {
		t.Fatalf("unexpected envelope: %+v body=%s", payload, rec.Body.String())
	}
	item := payload.Data.Items[0]
	if item.FullCode != "sz300394" || item.Symbol != "300394.SZ" || item.Benchmark.IndexCode != "399006.SZ" {
		t.Fatalf("unexpected item identity: %+v", item)
	}
	if item.Metrics["PCT_5D"].Value != float64(100) {
		t.Fatalf("PCT_5D = %#v, want 100", item.Metrics["PCT_5D"].Value)
	}
	if item.Metrics["AMOUNT_TREND_5D"].Value != "EXPANDING" {
		t.Fatalf("AMOUNT_TREND_5D = %#v, want EXPANDING", item.Metrics["AMOUNT_TREND_5D"].Value)
	}
	if item.Metrics["VS_INDEX_PP"].Value != float64(80) {
		t.Fatalf("VS_INDEX_PP = %#v, want 80", item.Metrics["VS_INDEX_PP"].Value)
	}
}

func TestHandleTradingInstrumentMetricsUsesLocalAuctionHistory(t *testing.T) {
	originalDir := databaseDir
	originalClient := client
	originalCodes := tdx.DefaultCodes
	defer func() {
		databaseDir = originalDir
		client = originalClient
		tdx.DefaultCodes = originalCodes
	}()
	databaseDir = t.TempDir()
	client = nil
	tdx.DefaultCodes = nil

	mustCreateTradingMetricTradeHistoryDB(t, filepath.Join(databaseDir, "trade", "sh600584.db"), "sh600584", []tradingMetricTradeFixture{
		{date: "20260519", clock: "09:25:00", price: 10000, volumeHand: 10},
		{date: "20260519", clock: "09:30:00", price: 10000, volumeHand: 9999},
		{date: "20260520", clock: "09:25:00", price: 10000, volumeHand: 20},
		{date: "20260520", clock: "09:30:00", price: 10000, volumeHand: 9999},
		{date: "20260521", clock: "09:25:00", price: 10000, volumeHand: 30},
		{date: "20260521", clock: "09:30:00", price: 10000, volumeHand: 9999},
		{date: "20260522", clock: "09:25:00", price: 10000, volumeHand: 40},
		{date: "20260522", clock: "09:30:00", price: 10000, volumeHand: 9999},
		{date: "20260525", clock: "09:25:00", price: 10000, volumeHand: 50},
		{date: "20260525", clock: "09:30:00", price: 10000, volumeHand: 9999},
		{date: "20260526", clock: "09:25:00", price: 10000, volumeHand: 990},
	})

	body := bytes.NewBufferString(`{
		"market":"CN",
		"as_of_trade_date":"2026-05-26",
		"full_codes":["sh600584"],
		"metric_codes":["AVG_AUCTION_AMOUNT_5D"]
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/instrument-metrics", body)
	handleTradingInstrumentMetrics(rec, req)

	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Items []struct {
				Metrics map[string]struct {
					Value                 float64 `json:"value"`
					Precision             string  `json:"precision"`
					Availability          string  `json:"availability"`
					CompletedSessionsUsed int     `json:"completed_sessions_used"`
				} `json:"metrics"`
				MissingMetrics []string `json:"missing_metrics"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if payload.Code != 0 || payload.Message != "success" {
		t.Fatalf("unexpected envelope: %+v body=%s", payload, rec.Body.String())
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("items length = %d, want 1", len(payload.Data.Items))
	}
	metric := payload.Data.Items[0].Metrics["AVG_AUCTION_AMOUNT_5D"]
	if metric.Value != 30000 {
		t.Fatalf("AVG_AUCTION_AMOUNT_5D = %#v, want 30000", metric.Value)
	}
	if metric.Precision != "AUCTION_HISTORY" || metric.Availability != "AVAILABLE" || metric.CompletedSessionsUsed != 5 {
		t.Fatalf("unexpected auction metric metadata: %+v", metric)
	}
	if len(payload.Data.Items[0].MissingMetrics) != 0 {
		t.Fatalf("missing_metrics = %+v, want empty", payload.Data.Items[0].MissingMetrics)
	}
}

func TestHandleTradingInstrumentMetricsRejectsUnknownMetricCode(t *testing.T) {
	originalClient := client
	originalCodes := tdx.DefaultCodes
	defer func() {
		client = originalClient
		tdx.DefaultCodes = originalCodes
	}()
	client = nil
	tdx.DefaultCodes = nil

	body := bytes.NewBufferString(`{
		"market":"CN",
		"as_of_trade_date":"2026-05-26",
		"full_codes":["sz300394"],
		"metric_codes":["BAD_METRIC_CODE"]
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/instrument-metrics", body)
	handleTradingInstrumentMetrics(rec, req)

	var payload Response
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if payload.Code != -1 || !strings.Contains(payload.Message, "unknown metric_code: BAD_METRIC_CODE") {
		t.Fatalf("unexpected error envelope: %+v", payload)
	}
}

func BenchmarkHandleTradingInstrumentMetricsLocalDailyBars50Instruments(b *testing.B) {
	originalDir := databaseDir
	originalClient := client
	originalCodes := tdx.DefaultCodes
	defer func() {
		databaseDir = originalDir
		client = originalClient
		tdx.DefaultCodes = originalCodes
	}()
	databaseDir = b.TempDir()
	client = nil
	tdx.DefaultCodes = nil

	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	fullCodes := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		fullCode := fmt.Sprintf("sz300%03d", i)
		fullCodes = append(fullCodes, fullCode)
		mustCreateTradingMetricDailyKlineDB(b, filepath.Join(databaseDir, "kline", fullCode+".db"), fullCode, asOf, []int64{6000, 6500, 7000, 7500, 8000, 8500, 9000, 9500, 10000, 10500, 11000, 11500, 12000, 13000, 10000, 12000, 14000, 16000, 18000, 20000}, []int64{100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 100000, 150000, 150000, 200000, 200000})
	}
	mustCreateTradingMetricDailyKlineDB(b, filepath.Join(databaseDir, "kline", "sz399006.db"), "sz399006", asOf, []int64{100000, 101000, 102000, 103000, 104000, 105000, 106000, 107000, 108000, 109000, 110000, 111000, 112000, 113000, 100000, 104000, 108000, 112000, 116000, 120000}, []int64{1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000, 1000000})

	bodyBytes, err := json.Marshal(tradingInstrumentMetricsRequest{
		Market:        "CN",
		AsOfTradeDate: "2026-05-26",
		FullCodes:     fullCodes,
		MetricCodes:   []string{"PCT_5D", "MAX_PCT_20D", "AMOUNT_TREND_5D", "AVG_AUCTION_AMOUNT_5D", "VS_INDEX_PP"},
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/trading/instrument-metrics", bytes.NewReader(bodyBytes))
		handleTradingInstrumentMetrics(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
		var payload Response
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			b.Fatal(err)
		}
		if payload.Code != 0 {
			b.Fatalf("payload = %+v", payload)
		}
	}
}

func mustCreateTradingMetricDailyKlineDB(t testing.TB, path, code string, asOf time.Time, closes []int64, amounts []int64) {
	t.Helper()
	if len(closes) != len(amounts) {
		t.Fatalf("fixture closes/amounts length mismatch")
	}
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
	start := asOf.AddDate(0, 0, -len(closes))
	for i, close := range closes {
		at := start.AddDate(0, 0, i)
		if _, err := db.Exec(`INSERT INTO DayKline(Code, Date, Open, High, Low, Close, Volume, Amount) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, code, at.Unix(), close, close, close, close, int64(100+i), amounts[i]); err != nil {
			t.Fatalf("insert DayKline %s: %v", code, err)
		}
	}
}

type tradingMetricTradeFixture struct {
	date       string
	clock      string
	price      int64
	volumeHand int
}

func mustCreateTradingMetricTradeHistoryDB(t testing.TB, path, code string, rows []tradingMetricTradeFixture) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir trade dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open trade db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE TradeHistory (Code TEXT NOT NULL, TradeDate TEXT NOT NULL, TradeTime INTEGER NOT NULL, Seq INTEGER NOT NULL, Price INTEGER NOT NULL, VolumeHand INTEGER NOT NULL, Number INTEGER NOT NULL, StatusCode INTEGER NOT NULL, Side TEXT NULL, InDate INTEGER NULL)`); err != nil {
		t.Fatalf("create TradeHistory table: %v", err)
	}
	for i, row := range rows {
		at, err := time.ParseInLocation("20060102 15:04:05", row.date+" "+row.clock, time.Local)
		if err != nil {
			t.Fatalf("parse trade fixture time: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO TradeHistory(Code, TradeDate, TradeTime, Seq, Price, VolumeHand, Number, StatusCode, Side) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, code, row.date, at.Unix(), i+1, row.price, row.volumeHand, 1, 2, ""); err != nil {
			t.Fatalf("insert TradeHistory %s %s: %v", row.date, row.clock, err)
		}
	}
}
