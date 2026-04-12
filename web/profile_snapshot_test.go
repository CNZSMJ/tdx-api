package main

import (
	"testing"

	"github.com/injoyai/tdx/profinance"
	"github.com/injoyai/tdx/protocol"
)

func TestBuildStockProfileSections(t *testing.T) {
	finance := &protocol.FinanceInfo{
		UpdatedDate:     20260403,
		Zongguben:       274006894.53125,
		Liutongguben:    168084707.03125,
		Meigujingzichan: 13.769,
		Jinglirun:       5192232812.5,
		Zhuyingshouru:   20331036250,
		Jingzichan:      39183347500,
	}
	proSnapshot := &profinance.Snapshot{
		Code:              "600000",
		ReportDate:        "20251231",
		BookValuePerShare: 13.88,
		TotalShares:       274006894.53125,
		FloatAShares:      168084707.03125,
		NetProfitTTM:      6123456789,
		RevenueTTMYuan:    24567890123,
		WeightedROE:       12.6,
		SourceReportFile:  "gpcw20251231.zip",
	}

	fundamentals, valuation := buildStockProfileSections(80.76, "realtime_quote", finance, proSnapshot)

	if got := fundamentals["finance_updated_date"]; got != "20260403" {
		t.Fatalf("finance_updated_date = %v, want 20260403", got)
	}
	if got := fundamentals["report_date"]; got != "20251231" {
		t.Fatalf("report_date = %v, want 20251231", got)
	}
	if got := fundamentals["net_profit_ttm"].(float64); got != 6123456789 {
		t.Fatalf("net_profit_ttm = %v, want 6123456789", got)
	}
	if got := valuation["market_cap_total"].(float64); got <= 0 {
		t.Fatalf("market_cap_total = %v, want > 0", got)
	}
	if got := valuation["market_cap_float"].(float64); got <= 0 {
		t.Fatalf("market_cap_float = %v, want > 0", got)
	}
	if got := valuation["pb_mrq"].(float64); got <= 0 {
		t.Fatalf("pb_mrq = %v, want > 0", got)
	}
	if got := valuation["pe_ttm"].(float64); got <= 0 {
		t.Fatalf("pe_ttm = %v, want > 0", got)
	}
	if got := valuation["ps_ttm"].(float64); got <= 0 {
		t.Fatalf("ps_ttm = %v, want > 0", got)
	}
	if got := valuation["source"]; got != "tdx_raw_finance+tdx_professional_finance" {
		t.Fatalf("source = %v, want tdx_raw_finance+tdx_professional_finance", got)
	}
}

func TestBuildStockProfileSectionsRequiresRealtimeQuoteForValuation(t *testing.T) {
	finance := &protocol.FinanceInfo{
		UpdatedDate:     20260403,
		Zongguben:       274006894.53125,
		Liutongguben:    168084707.03125,
		Meigujingzichan: 13.769,
	}
	proSnapshot := &profinance.Snapshot{
		Code:              "600000",
		ReportDate:        "20251231",
		BookValuePerShare: 13.88,
		TotalShares:       274006894.53125,
		FloatAShares:      168084707.03125,
		NetProfitTTM:      6123456789,
		RevenueTTMYuan:    24567890123,
	}

	_, valuation := buildStockProfileSections(80.76, "code_cache_last_price", finance, proSnapshot)

	if got := valuation["available"]; got != false {
		t.Fatalf("available = %v, want false", got)
	}
	if got := valuation["reason"]; got != "realtime_quote_required" {
		t.Fatalf("reason = %v, want realtime_quote_required", got)
	}
	if _, ok := valuation["market_cap_total"]; ok {
		t.Fatalf("market_cap_total should be absent when realtime quote is unavailable")
	}
	if _, ok := valuation["pb_mrq"]; ok {
		t.Fatalf("pb_mrq should be absent when realtime quote is unavailable")
	}
}

func TestValidateInvestmentGradeStockSnapshot(t *testing.T) {
	validQuote := map[string]interface{}{
		"available": true,
		"source":    "realtime_quote",
	}
	validValuation := map[string]interface{}{
		"available": true,
	}
	if err := validateInvestmentGradeStockSnapshot(validQuote, validValuation); err != nil {
		t.Fatalf("expected valid investment-grade snapshot, got error: %v", err)
	}

	invalidQuote := map[string]interface{}{
		"available": false,
		"reason":    "realtime_quote_unavailable",
	}
	if err := validateInvestmentGradeStockSnapshot(invalidQuote, validValuation); err == nil {
		t.Fatalf("expected quote validation error")
	}

	invalidValuation := map[string]interface{}{
		"available": false,
		"reason":    "realtime_quote_required",
	}
	if err := validateInvestmentGradeStockSnapshot(validQuote, invalidValuation); err == nil {
		t.Fatalf("expected valuation validation error")
	}
}
