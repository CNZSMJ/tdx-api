package lifecycle

import "testing"

func TestStageOneParquetSchemasAndMappings(t *testing.T) {
	schemas := StageOneParquetSchemas()
	requiredTables := []string{"TradeHistory", "TradeMinute1Bar", "TradeLive", "MinuteLive", "QuoteSnapshot", "OrderHistory"}
	for _, table := range requiredTables {
		schema, ok := schemas[table]
		if !ok {
			t.Fatalf("missing schema for %s", table)
		}
		if schema.SchemaVersion != 1 || len(schema.Columns) == 0 {
			t.Fatalf("invalid schema for %s: %+v", table, schema)
		}
		if schema.StableKeys == nil || len(schema.StableKeys) == 0 {
			t.Fatalf("missing stable keys for %s", table)
		}
	}

	tests := map[string]string{
		"TradeHistory.Code":         "code",
		"TradeHistory.TradeDate":    "trade_date",
		"TradeHistory.TradeTime":    "trade_time",
		"TradeHistory.Price":        "price_milli",
		"TradeHistory.StatusCode":   "status_code",
		"TradeBarRow.BucketTime":    "bucket_time",
		"TradeBarRow.Open":          "open_milli",
		"TradeBarRow.Amount":        "amount_milli",
		"QuoteSnapshot.CaptureTime": "capture_time",
		"QuoteSnapshot.Last":        "last_milli",
		"OrderHistory.Price":        "price_milli",
	}
	for key, want := range tests {
		table, col := splitMappingKey(key)
		got, ok := SQLiteToParquetColumn(table, col)
		if !ok || got != want {
			t.Fatalf("%s maps to %q ok=%v, want %q", key, got, ok, want)
		}
	}
}
