package main

import (
	"testing"
	"time"

	tdx "github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestBuildInstrumentReferenceMarksMissingFields(t *testing.T) {
	model := &tdx.CodeModel{
		Code:     "600000",
		Name:     "浦发银行",
		Exchange: "sh",
		Multiple: 100,
		Decimal:  2,
	}
	finance := &protocol.FinanceInfo{
		IpoDate:   19991110,
		Industry:  42,
		Zongguben: 1000,
	}

	ref := buildInstrumentReference(model, finance, "", "股份制商业银行", "银行", "", "active")

	if ref.FullCode != "sh600000" {
		t.Fatalf("full_code = %s, want sh600000", ref.FullCode)
	}
	if ref.ListingDate != "1999-11-10" {
		t.Fatalf("listing_date = %s, want 1999-11-10", ref.ListingDate)
	}
	if ref.IndustryCode != "42" {
		t.Fatalf("industry_code = %s, want 42", ref.IndustryCode)
	}
	if ref.TickSize != 0.01 {
		t.Fatalf("tick_size = %v, want 0.01", ref.TickSize)
	}
	if !containsString(ref.MissingFields, "issuer_name") {
		t.Fatalf("missing_fields = %#v, want issuer_name", ref.MissingFields)
	}
	if !containsString(ref.MissingFields, "subindustry_name") {
		t.Fatalf("missing_fields = %#v, want subindustry_name", ref.MissingFields)
	}
}

func TestFilterIntradayBarRowsDoesNotFallback(t *testing.T) {
	rows := []historyBarRow{
		{Time: time.Date(2026, 4, 14, 9, 31, 0, 0, time.Local), Open: 10.0, High: 10.1, Low: 9.9, Close: 10.0},
	}
	_, err := filterIntradayBarRows(rows, time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local))
	if err == nil {
		t.Fatalf("expected exact-date error when requested trading_date has no rows")
	}
}

func TestBuildAdjustmentFactorItemsFiltersRange(t *testing.T) {
	items := buildAdjustmentFactorItems([]*extend.THSFactor{
		{Date: time.Date(2025, 6, 20, 15, 0, 0, 0, time.Local).Unix(), QFactor: 0.9821, HFactor: 1.0182},
		{Date: time.Date(2026, 1, 10, 15, 0, 0, 0, time.Local).Unix(), QFactor: 0.9733, HFactor: 1.0274},
	}, time.Date(2025, 12, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local))

	if len(items) != 1 {
		t.Fatalf("adjustment factor count = %d, want 1", len(items))
	}
	if items[0].ExDate != "2026-01-10" {
		t.Fatalf("ex_date = %s, want 2026-01-10", items[0].ExDate)
	}
	if items[0].ForwardFactor != 0.9733 {
		t.Fatalf("forward_factor = %v, want 0.9733", items[0].ForwardFactor)
	}
}

func TestInferQuoteStatusMarksInferredSource(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 30, 0, 0, time.Local)
	quote := &protocol.Quote{
		K: protocol.K{
			Last:  protocol.Price(10000),
			Open:  protocol.Price(10000),
			High:  protocol.Price(11000),
			Low:   protocol.Price(10000),
			Close: protocol.Price(11000),
		},
		TotalHand: 1200,
	}

	status := inferQuoteStatus("sh600000", "浦发银行", quote, now)
	if status.Source != "inferred_from_quote" {
		t.Fatalf("status source = %s, want inferred_from_quote", status.Source)
	}
	if !status.IsLimitUp {
		t.Fatalf("expected limit-up inference, got %#v", status)
	}
	if status.IsHalted {
		t.Fatalf("expected active quote, got %#v", status)
	}
}

func TestBuildSignalCheckPayloadFullMode(t *testing.T) {
	payload := buildSignalCheckPayload(
		[]string{"sh600000"},
		[]string{"new_high", "volume_spike"},
		"full",
		[]collectorpkg.SignalItem{{Code: "sh600000", Name: "浦发银行", SignalType: "new_high"}},
		time.Date(2026, 4, 15, 14, 31, 0, 0, time.Local),
	)

	items := payload["items"].([]map[string]interface{})
	if len(items) != 2 {
		t.Fatalf("item count = %d, want 2", len(items))
	}
	if items[0]["matched"] != true {
		t.Fatalf("first item matched = %v, want true", items[0]["matched"])
	}
	if items[1]["matched"] != false {
		t.Fatalf("second item matched = %v, want false", items[1]["matched"])
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
