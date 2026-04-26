package lifecycle

import "testing"

func TestValidatePriceMilliContractCoversStageOneSourceFields(t *testing.T) {
	report, err := ValidatePriceMilliContract()
	if err != nil {
		t.Fatalf("price unit contract: %v", err)
	}
	required := []string{
		"TradeHistoryRow.Price",
		"TradeBarRow.Open",
		"TradeBarRow.High",
		"TradeBarRow.Low",
		"TradeBarRow.Close",
		"TradeBarRow.Amount",
		"TradeLiveRow.Price",
		"MinuteLiveRow.Price",
		"QuoteSnapshotRow.Last",
		"QuoteSnapshotRow.PreClose",
		"QuoteSnapshotRow.Open",
		"QuoteSnapshotRow.High",
		"QuoteSnapshotRow.Low",
		"OrderHistoryRow.Price",
	}
	for _, field := range required {
		if report.Fields[field] != "PriceMilli" {
			t.Fatalf("%s unit = %q, want PriceMilli; report=%+v", field, report.Fields[field], report.Fields)
		}
	}
}
