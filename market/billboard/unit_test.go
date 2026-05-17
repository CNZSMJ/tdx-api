package billboard

import (
	"encoding/json"
	"testing"
)

func TestNormalizeSecuCodeAcceptsSHSZBJAStocks(t *testing.T) {
	tests := []struct {
		secucode string
		fullCode string
		exchange string
		code     string
	}{
		{secucode: "600000.SH", fullCode: "sh600000", exchange: "sh", code: "600000"},
		{secucode: "000001.SZ", fullCode: "sz000001", exchange: "sz", code: "000001"},
		{secucode: "920118.BJ", fullCode: "bj920118", exchange: "bj", code: "920118"},
	}

	for _, tt := range tests {
		got, ok := NormalizeSecuCode(tt.secucode)
		if !ok {
			t.Fatalf("NormalizeSecuCode(%q) rejected supported stock", tt.secucode)
		}
		if got.FullCode != tt.fullCode || got.Exchange != tt.exchange || got.Code != tt.code || got.AssetType != "stock" {
			t.Fatalf("NormalizeSecuCode(%q) = %#v, want full_code=%s exchange=%s code=%s stock", tt.secucode, got, tt.fullCode, tt.exchange, tt.code)
		}
	}
}

func TestNormalizeSecuCodeRejectsUnsupportedAssets(t *testing.T) {
	for _, secucode := range []string{"510300.SH", "159915.SZ", "000001.SH", "113000.SH", "600000.HK", "600000"} {
		if got, ok := NormalizeSecuCode(secucode); ok {
			t.Fatalf("NormalizeSecuCode(%q) = %#v, want rejected", secucode, got)
		}
	}
}

func TestMoneyMilliUsesFieldLevelUnitContract(t *testing.T) {
	cases := []struct {
		report string
		field  string
		value  any
		want   int64
	}{
		{ReportDailyDetails, "BILLBOARD_SELL_AMT", 69639811.66, 69639811660},
		{ReportBuyDetails, "BUY", 20443206, 20443206000},
		{ReportOrganizationTradeDetails, "FREECAP", json.Number("44.66"), 4466000000000},
	}

	for _, tt := range cases {
		got, err := NormalizeMilli(tt.report, tt.field, tt.value)
		if err != nil {
			t.Fatalf("NormalizeMilli(%s,%s): %v", tt.report, tt.field, err)
		}
		if got != tt.want {
			t.Fatalf("NormalizeMilli(%s,%s,%v) = %d, want %d", tt.report, tt.field, tt.value, got, tt.want)
		}
	}
}

func TestUnitContractSeparatesPercentAndRatio(t *testing.T) {
	if got := UnitForField(ReportDailyDetails, "TURNOVERRATE"); got != UnitPercentValue {
		t.Fatalf("TURNOVERRATE unit = %s, want percent value", got)
	}
	if got := UnitForField(ReportBuyDetails, "TOTAL_BUYRIO"); got != UnitRatioFraction {
		t.Fatalf("TOTAL_BUYRIO unit = %s, want ratio fraction", got)
	}
}
