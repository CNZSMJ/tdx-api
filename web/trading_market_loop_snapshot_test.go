package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func TestTradingMarketLoopSnapshotUsesLiveTickerOnly(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	now := time.Date(2026, 4, 29, 10, 0, 5, 0, time.Local)
	marketScreenNow = func() time.Time { return now }

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "浦发银行", 10900),
		marketScreenTestQuoteOHLC("sz000001", "平安银行", 10000, 11000, 9700, 9700),
		marketScreenTestQuote("sz000002", "万科A", 11200),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000", "sz000001", "sz000002"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	resp, err := buildTradingMarketLoopSnapshot(tradingMarketLoopSnapshotRequest{
		Market:              "CN",
		TradeDate:           "2026-04-29",
		SnapshotTime:        "10:00:05",
		PlannedFullCodes:    []string{"sh600000"},
		HoldingFullCodes:    []string{"sz000001"},
		WatchFullCodes:      []string{"sz000002"},
		CandidateBlockTypes: []string{"concept"},
		RankingLimit:        5,
		ScreenLimit:         1,
		MaxStalenessSeconds: 15,
	}, ts)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if resp.SourcePolicy.DataSource != "ticker" || resp.SourcePolicy.ProviderStatus != "live_tick" || resp.SourcePolicy.FallbackAllowed {
		t.Fatalf("unexpected source policy: %#v", resp.SourcePolicy)
	}
	if resp.SourcePolicy.TickerUpdatedAt == "" {
		t.Fatalf("ticker_updated_at should be set")
	}
	if resp.TradeDate != "2026-04-29" || resp.SnapshotTime != "10:00:05" {
		t.Fatalf("unexpected snapshot identity: %#v", resp)
	}
	if len(resp.PlannedInstruments) != 1 || resp.PlannedInstruments[0]["full_code"] != "sh600000" {
		t.Fatalf("planned instruments not resolved from tick snapshot: %#v", resp.PlannedInstruments)
	}
	if resp.PlannedInstruments[0]["avg_trade_price"] == nil || resp.PlannedInstruments[0]["pullback_from_high_pp"] == nil {
		t.Fatalf("planned instruments should include intraday risk facts: %#v", resp.PlannedInstruments[0])
	}
	if len(resp.Screens.TopAmount.List) != 1 || resp.Screens.TopAmount.List[0]["amount"] == nil {
		t.Fatalf("top amount should be returned from current ticks: %#v", resp.Screens.TopAmount)
	}
	if len(resp.Screens.TopGainers.List) != 1 || resp.Screens.TopGainers.List[0]["full_code"] != "sz000002" {
		t.Fatalf("top gainers should be built from current ticks: %#v", resp.Screens.TopGainers)
	}
	if len(resp.Screens.ActiveRisk.List) != 1 || resp.Screens.ActiveRisk.List[0]["full_code"] != "sz000001" {
		t.Fatalf("active risk should include large losers or high pullbacks: %#v", resp.Screens.ActiveRisk)
	}
	raw, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		t.Fatalf("marshal response: %v", marshalErr)
	}
	for _, forbidden := range []string{"daily_kline", "quote_snapshot", "closed_snapshot", `"data_source":"quote"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("market-loop snapshot must not expose fallback marker %q: %s", forbidden, string(raw))
		}
	}
}

func TestTradingMarketLoopSnapshotRejectsStaleTicker(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	marketScreenNow = func() time.Time { return now }

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "浦发银行", 10900),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	now = now.Add(20 * time.Second)
	_, err := buildTradingMarketLoopSnapshot(tradingMarketLoopSnapshotRequest{
		Market:              "CN",
		TradeDate:           "2026-04-29",
		SnapshotTime:        "10:00:20",
		MaxStalenessSeconds: 10,
		ScreenLimit:         5,
		RankingLimit:        5,
	}, ts)
	if err == nil || err.ErrorCode != "LIVE_TICKER_STALE" {
		t.Fatalf("error = %#v, want LIVE_TICKER_STALE", err)
	}
}

func TestTradingMarketLoopSnapshotRejectsDateMismatch(t *testing.T) {
	originalNow := marketScreenNow
	defer func() {
		marketScreenNow = originalNow
	}()
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)
	marketScreenNow = func() time.Time { return now }

	ts := collectorpkg.NewTickerService(&marketScreenQuoteProvider{quotes: []collectorpkg.QuoteSnapshot{
		marketScreenTestQuote("sh600000", "浦发银行", 10900),
	}}, nil, collectorpkg.TickerConfig{
		Interval: time.Hour,
		Now:      marketScreenNow,
	})
	ts.Start([]string{"sh600000"}, nil)
	defer ts.Stop()
	waitForMarketScreenTicker(t, ts)

	_, err := buildTradingMarketLoopSnapshot(tradingMarketLoopSnapshotRequest{
		Market:              "CN",
		TradeDate:           "2026-04-30",
		SnapshotTime:        "10:00:00",
		MaxStalenessSeconds: 10,
		ScreenLimit:         5,
		RankingLimit:        5,
	}, ts)
	if err == nil || err.ErrorCode != "LIVE_TICKER_DATE_MISMATCH" {
		t.Fatalf("error = %#v, want LIVE_TICKER_DATE_MISMATCH", err)
	}
}

func TestHandleTradingMarketLoopSnapshotWritesStructuredError(t *testing.T) {
	originalRuntime := collectorRuntime
	defer func() {
		collectorRuntime = originalRuntime
	}()
	collectorRuntime = nil

	body := bytes.NewBufferString(`{
		"market":"CN",
		"trade_date":"2026-04-29",
		"snapshot_time":"10:00:00",
		"max_staleness_seconds":10,
		"screen_limit":5,
		"ranking_limit":5
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/market-loop-snapshot", body)
	handleTradingMarketLoopSnapshot(rec, req)

	var payload struct {
		Code  int `json:"code"`
		Error struct {
			ErrorCode string `json:"error_code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, rec.Body.String())
	}
	if payload.Code == 0 || payload.Error.ErrorCode != "LIVE_TICKER_NOT_READY" || !payload.Error.Retryable {
		t.Fatalf("unexpected error payload: %#v body=%s", payload, rec.Body.String())
	}
}
