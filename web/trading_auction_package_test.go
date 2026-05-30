package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/protocol"
)

func TestHandleTradingAuctionPackageReturnsFactsOnly(t *testing.T) {
	originalDir := databaseDir
	originalClient := client
	originalQuoteFetcher := tradingAuctionQuoteFetcher
	originalNow := tradingAuctionNow
	defer func() {
		databaseDir = originalDir
		client = originalClient
		tradingAuctionQuoteFetcher = originalQuoteFetcher
		tradingAuctionNow = originalNow
	}()
	databaseDir = t.TempDir()
	client = nil
	tradingAuctionNow = func() time.Time {
		return time.Date(2026, 5, 26, 9, 26, 0, 0, time.Local)
	}

	codesPath := filepath.Join(databaseDir, "codes.db")
	mustCreateMarketScreenCodesDB(t, codesPath)
	mustInsertMarketScreenCode(t, codesPath, "天孚通信", "300394", "sz")
	mustInsertMarketScreenCode(t, codesPath, "示例一字", "600001", "sh")
	mustInsertMarketScreenCode(t, codesPath, "ST示例", "000002", "sz")

	mustCreateTradingMetricDailyKlineDB(t, filepath.Join(databaseDir, "kline", "sz300394.db"), "sz300394", time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local), []int64{120000}, []int64{100000})
	mustCreateTradingMetricTradeHistoryDB(t, filepath.Join(databaseDir, "trade", "sz300394.db"), "sz300394", []tradingMetricTradeFixture{
		{date: "20260519", clock: "09:25:00", price: 10000, volumeHand: 10},
		{date: "20260520", clock: "09:25:00", price: 10000, volumeHand: 20},
		{date: "20260521", clock: "09:25:00", price: 10000, volumeHand: 30},
		{date: "20260522", clock: "09:25:00", price: 10000, volumeHand: 40},
		{date: "20260525", clock: "09:25:00", price: 10000, volumeHand: 50},
		{date: "20260526", clock: "09:25:00", price: 123450, volumeHand: 10000},
	})

	installBlockMemberRuntime(t, map[string][]collectorpkg.BlockInfo{
		"block_gn.dat": {
			{Name: "机器人", BlockType: collectorpkg.BlockTypeConcept, Source: "block_gn.dat", Codes: []string{"sz300394", "sh600001"}},
		},
	})
	blockID := tradingAuctionBlockID(collectorpkg.BlockGroupRecord{
		Source:    "block_gn.dat",
		BlockType: string(collectorpkg.BlockTypeConcept),
		Name:      "机器人",
	})

	tradingAuctionQuoteFetcher = func(codes ...string) (protocol.QuotesResp, error) {
		resp := make(protocol.QuotesResp, 0, len(codes))
		for _, code := range codes {
			switch strings.ToLower(code) {
			case "sz300394":
				resp = append(resp, testTradingAuctionQuote(protocol.ExchangeSZ, "300394", 120000, 123450, 123450, 123450000, 123450, 1000))
			case "sh600001":
				resp = append(resp, testTradingAuctionQuote(protocol.ExchangeSH, "600001", 10000, 11000, 11000, 90000000, 11000, 2000))
			case "sz000002":
				resp = append(resp, testTradingAuctionQuote(protocol.ExchangeSZ, "000002", 10000, 10500, 10500, 5000000, 10500, 3000))
			case "sh600000":
				resp = append(resp, testTradingAuctionQuote(protocol.ExchangeSH, "600000", 10000, 10000, 10000, 1000000, 10000, 4000))
			case "sz000001":
				resp = append(resp, testTradingAuctionQuote(protocol.ExchangeSZ, "000001", 10000, 9800, 9800, 2000000, 9800, 5000))
			default:
				return nil, fmt.Errorf("unexpected quote code %s", code)
			}
		}
		return resp, nil
	}

	body := bytes.NewBufferString(fmt.Sprintf(`{
		"market":"CN",
		"trade_date":"2026-05-26",
		"auction_phase":"FINAL",
		"snapshot_time":"09:26:00",
		"planned_full_codes":["sz300394"],
		"yd_limit_up_full_codes":["sh600001"],
		"watched_block_ids":["%s"],
		"include_auction_born_candidates":false,
		"candidate_block_types":["concept"],
		"candidate_limit":5,
		"low_open_threshold_pct":-5
	}`, blockID))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/auction-package", body)
	handleTradingAuctionPackage(rec, req)

	raw := rec.Body.String()
	for _, forbidden := range []string{"auction_result", "hard_veto", "trade_plan", "tradable", "above_expectation", "below_expectation"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response contains forbidden trading judgment field %q: %s", forbidden, raw)
		}
	}
	for _, obsolete := range []string{"yizi_count", "all_market_snapshot"} {
		if strings.Contains(raw, obsolete) {
			t.Fatalf("response contains obsolete market field %q: %s", obsolete, raw)
		}
	}

	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TradeDate    string `json:"trade_date"`
			AuctionPhase string `json:"auction_phase"`
			SnapshotTime string `json:"snapshot_time"`
			AuctionMkt   struct {
				LimitUp      map[string]any `json:"limit_up"`
				ByStockClass map[string]struct {
					LimitUp map[string]any `json:"limit_up"`
				} `json:"by_stock_class"`
				MissingFields []string `json:"missing_fields"`
			} `json:"auction_mkt"`
			PlannedNames []struct {
				FullCode                       string   `json:"full_code"`
				Symbol                         string   `json:"symbol"`
				AuctionPrice                   float64  `json:"auction_price"`
				PrevClose                      float64  `json:"prev_close"`
				AuctionPct                     float64  `json:"auction_pct"`
				AuctionAmount                  float64  `json:"auction_amount"`
				AvgAuctionAmount5D             float64  `json:"avg_auction_amount_5d"`
				AvgAuctionAmount5DAvailability string   `json:"avg_auction_amount_5d_availability"`
				AmountVsAvgAuction5D           float64  `json:"amount_vs_avg_auction_5d"`
				IsLimitUpOpen                  bool     `json:"is_limit_up_open"`
				Precision                      string   `json:"precision"`
				Availability                   string   `json:"availability"`
				MissingFields                  []string `json:"missing_fields"`
			} `json:"planned_names"`
			BlockMembers []struct {
				BlockID   string   `json:"block_id"`
				Name      string   `json:"name"`
				BlockType string   `json:"block_type"`
				Source    string   `json:"source"`
				Members   []string `json:"members"`
			} `json:"block_members"`
			AuctionBornCandidates []struct {
				BlockID           string   `json:"block_id"`
				Theme             string   `json:"theme"`
				BlockType         string   `json:"block_type"`
				SymbolCount       int      `json:"symbol_count"`
				OneLineCount      int      `json:"one_line_count"`
				FrontlineSymbols  []string `json:"frontline_symbols"`
				GroupBehavior     string   `json:"group_behavior"`
				FrontlineBehavior string   `json:"frontline_behavior"`
			} `json:"auction_born_candidates"`
			Failures []map[string]any `json:"failures"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, raw)
	}
	if payload.Code != 0 || payload.Message != "success" {
		t.Fatalf("unexpected envelope: %+v body=%s", payload, raw)
	}
	if payload.Data.TradeDate != "2026-05-26" || payload.Data.AuctionPhase != "FINAL" || payload.Data.SnapshotTime != "09:26:00" {
		t.Fatalf("unexpected package identity: %+v", payload.Data)
	}
	if len(payload.Data.PlannedNames) != 1 {
		t.Fatalf("planned_names length = %d, want 1", len(payload.Data.PlannedNames))
	}
	planned := payload.Data.PlannedNames[0]
	if planned.FullCode != "sz300394" || planned.Symbol != "300394.SZ" {
		t.Fatalf("unexpected planned identity: %+v", planned)
	}
	if planned.AuctionPrice != 123.45 || planned.PrevClose != 120 || planned.AuctionPct != 2.88 {
		t.Fatalf("unexpected planned auction price fields: %+v", planned)
	}
	if planned.AuctionAmount != 123450000 {
		t.Fatalf("auction_amount = %v, want 123450000 from opening auction", planned.AuctionAmount)
	}
	if planned.AvgAuctionAmount5D != 30000 || planned.AvgAuctionAmount5DAvailability != "AVAILABLE" {
		t.Fatalf("unexpected avg auction fields: %+v", planned)
	}
	if planned.AmountVsAvgAuction5D != 4115 {
		t.Fatalf("amount_vs_avg_auction_5d = %v, want 4115", planned.AmountVsAvgAuction5D)
	}
	if planned.Precision != "QUOTE_SNAPSHOT" || planned.Availability != "AVAILABLE" || len(planned.MissingFields) != 1 || planned.MissingFields[0] != "seal_volume" {
		t.Fatalf("unexpected planned availability metadata: %+v", planned)
	}
	if _, ok := payload.Data.AuctionMkt.LimitUp["one_line"]; !ok {
		t.Fatalf("auction_mkt.limit_up should reuse one_line field: %+v", payload.Data.AuctionMkt.LimitUp)
	}
	for _, stockClass := range []string{"non_st", "st"} {
		limitUp := payload.Data.AuctionMkt.ByStockClass[stockClass].LimitUp
		if _, ok := limitUp["one_line"]; !ok {
			t.Fatalf("auction_mkt.by_stock_class.%s.limit_up should reuse one_line field: %+v", stockClass, limitUp)
		}
	}
	if len(payload.Data.BlockMembers) != 1 || payload.Data.BlockMembers[0].BlockID != blockID || payload.Data.BlockMembers[0].Name != "机器人" {
		t.Fatalf("unexpected block members: %+v", payload.Data.BlockMembers)
	}
	if len(payload.Data.Failures) != 0 {
		t.Fatalf("failures = %+v, want empty", payload.Data.Failures)
	}
}

func TestHandleTradingAuctionPackageReportsMissingExplicitCodes(t *testing.T) {
	originalDir := databaseDir
	originalQuoteFetcher := tradingAuctionQuoteFetcher
	defer func() {
		databaseDir = originalDir
		tradingAuctionQuoteFetcher = originalQuoteFetcher
	}()
	databaseDir = t.TempDir()
	mustCreateMarketScreenCodesDB(t, filepath.Join(databaseDir, "codes.db"))
	tradingAuctionQuoteFetcher = func(codes ...string) (protocol.QuotesResp, error) {
		return protocol.QuotesResp{}, nil
	}

	body := bytes.NewBufferString(`{
		"market":"CN",
		"trade_date":"2026-05-26",
		"auction_phase":"FINAL",
		"snapshot_time":"09:26:00",
		"planned_full_codes":["sz300394"]
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/trading/auction-package", body)
	handleTradingAuctionPackage(rec, req)

	var payload struct {
		Code int `json:"code"`
		Data struct {
			PlannedNames []map[string]any `json:"planned_names"`
			Failures     []struct {
				FullCode string `json:"full_code"`
				Message  string `json:"message"`
			} `json:"failures"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if payload.Code != 0 {
		t.Fatalf("unexpected envelope: %s", rec.Body.String())
	}
	if len(payload.Data.PlannedNames) != 0 {
		t.Fatalf("planned_names = %+v, want empty", payload.Data.PlannedNames)
	}
	if len(payload.Data.Failures) != 1 || payload.Data.Failures[0].FullCode != "sz300394" {
		t.Fatalf("failures = %+v, want explicit missing sz300394", payload.Data.Failures)
	}
}

func testTradingAuctionQuote(exchange protocol.Exchange, code string, prevClose, open, price protocol.Price, amount float64, bid1 protocol.Price, bid1Volume int) *protocol.Quote {
	return &protocol.Quote{
		Exchange:  exchange,
		Code:      code,
		TotalHand: int(amount / price.Float64() / 100),
		Amount:    amount,
		K: protocol.K{
			Last:  prevClose,
			Open:  open,
			High:  open,
			Low:   open,
			Close: price,
		},
		BuyLevel: protocol.PriceLevels{
			{Buy: true, Price: bid1, Number: bid1Volume},
		},
	}
}
