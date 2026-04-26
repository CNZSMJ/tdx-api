package lifecycle

import "strings"

type ParquetColumn struct {
	Name     string
	Type     string
	Nullable bool
	Unit     string
}

type ParquetSchema struct {
	TableName     string
	SchemaVersion int
	Columns       []ParquetColumn
	StableKeys    []string
}

func StageOneParquetSchemas() map[string]ParquetSchema {
	return map[string]ParquetSchema{
		"TradeHistory": schema("TradeHistory", []string{"code", "trade_date", "trade_time", "seq"}, []ParquetColumn{
			col("schema_version", "int32", false, ""),
			col("code", "string", false, ""),
			col("trade_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
			col("trade_time", "int64(unix_seconds)", false, "Asia/Shanghai"),
			col("seq", "int32", false, ""),
			col("price_milli", "int64", false, "PriceMilli"),
			col("volume_hand", "int32", false, ""),
			col("number", "int32", false, ""),
			col("status_code", "int32", false, ""),
			col("side", "string", true, ""),
		}),
		"TradeMinute1Bar":  tradeBarSchema("TradeMinute1Bar"),
		"TradeMinute5Bar":  tradeBarSchema("TradeMinute5Bar"),
		"TradeMinute15Bar": tradeBarSchema("TradeMinute15Bar"),
		"TradeMinute30Bar": tradeBarSchema("TradeMinute30Bar"),
		"TradeMinute60Bar": tradeBarSchema("TradeMinute60Bar"),
		"TradeLive": schema("TradeLive", []string{"code", "trade_date", "trade_time", "seq"}, []ParquetColumn{
			col("schema_version", "int32", false, ""),
			col("code", "string", false, ""),
			col("trade_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
			col("trade_time", "int64(unix_seconds)", false, "Asia/Shanghai"),
			col("seq", "int32", false, ""),
			col("price_milli", "int64", false, "PriceMilli"),
			col("volume_hand", "int32", false, ""),
			col("number", "int32", false, ""),
			col("status_code", "int32", false, ""),
			col("side", "string", true, ""),
		}),
		"MinuteLive": schema("MinuteLive", []string{"code", "trade_date", "clock"}, []ParquetColumn{
			col("schema_version", "int32", false, ""),
			col("code", "string", false, ""),
			col("trade_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
			col("clock", "string(HH:MM)", false, "Asia/Shanghai"),
			col("price_milli", "int64", false, "PriceMilli"),
			col("number", "int32", false, ""),
		}),
		"QuoteSnapshot": schema("QuoteSnapshot", []string{"code", "capture_time"}, []ParquetColumn{
			col("schema_version", "int32", false, ""),
			col("code", "string", false, ""),
			col("capture_time", "int64(unix_seconds)", false, "Asia/Shanghai"),
			col("capture_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
			col("last_milli", "int64", false, "PriceMilli"),
			col("pre_close_milli", "int64", false, "PriceMilli"),
			col("open_milli", "int64", false, "PriceMilli"),
			col("high_milli", "int64", false, "PriceMilli"),
			col("low_milli", "int64", false, "PriceMilli"),
			col("volume_hand", "int64", false, ""),
			col("amount_yuan", "double", false, "CNY"),
		}),
		"OrderHistory": schema("OrderHistory", []string{"code", "trade_date", "seq"}, []ParquetColumn{
			col("schema_version", "int32", false, ""),
			col("code", "string", false, ""),
			col("trade_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
			col("seq", "int32", false, ""),
			col("price_milli", "int64", false, "PriceMilli"),
			col("buy_sell_delta", "int32", false, ""),
			col("volume", "int32", false, ""),
		}),
	}
}

func tradeBarSchema(table string) ParquetSchema {
	return schema(table, []string{"code", "trade_date", "bucket_time"}, []ParquetColumn{
		col("schema_version", "int32", false, ""),
		col("code", "string", false, ""),
		col("trade_date", "string(YYYYMMDD)", false, "Asia/Shanghai"),
		col("bucket_time", "int64(unix_seconds)", false, "Asia/Shanghai"),
		col("open_milli", "int64", false, "PriceMilli"),
		col("high_milli", "int64", false, "PriceMilli"),
		col("low_milli", "int64", false, "PriceMilli"),
		col("close_milli", "int64", false, "PriceMilli"),
		col("volume_hand", "int64", false, ""),
		col("amount_milli", "int64", false, "PriceMilli"),
	})
}

func schema(table string, stableKeys []string, columns []ParquetColumn) ParquetSchema {
	return ParquetSchema{TableName: table, SchemaVersion: 1, StableKeys: stableKeys, Columns: columns}
}

func col(name, typ string, nullable bool, unit string) ParquetColumn {
	return ParquetColumn{Name: name, Type: typ, Nullable: nullable, Unit: unit}
}

func SQLiteToParquetColumn(table, sqliteColumn string) (string, bool) {
	mapping := sqliteToParquetMappings()
	if cols, ok := mapping[table]; ok {
		value, exists := cols[sqliteColumn]
		return value, exists
	}
	return "", false
}

func sqliteToParquetMappings() map[string]map[string]string {
	baseTrade := map[string]string{
		"Code":       "code",
		"TradeDate":  "trade_date",
		"TradeTime":  "trade_time",
		"Seq":        "seq",
		"Price":      "price_milli",
		"VolumeHand": "volume_hand",
		"Number":     "number",
		"StatusCode": "status_code",
		"Side":       "side",
	}
	bar := map[string]string{
		"Code":       "code",
		"TradeDate":  "trade_date",
		"BucketTime": "bucket_time",
		"Open":       "open_milli",
		"High":       "high_milli",
		"Low":        "low_milli",
		"Close":      "close_milli",
		"VolumeHand": "volume_hand",
		"Amount":     "amount_milli",
	}
	return map[string]map[string]string{
		"TradeHistory":     baseTrade,
		"TradeLive":        baseTrade,
		"TradeBarRow":      bar,
		"TradeMinute1Bar":  bar,
		"TradeMinute5Bar":  bar,
		"TradeMinute15Bar": bar,
		"TradeMinute30Bar": bar,
		"TradeMinute60Bar": bar,
		"MinuteLive": {
			"Code":      "code",
			"TradeDate": "trade_date",
			"Clock":     "clock",
			"Price":     "price_milli",
			"Number":    "number",
		},
		"QuoteSnapshot": {
			"Code":        "code",
			"CaptureTime": "capture_time",
			"Last":        "last_milli",
			"PreClose":    "pre_close_milli",
			"Open":        "open_milli",
			"High":        "high_milli",
			"Low":         "low_milli",
			"VolumeHand":  "volume_hand",
			"AmountYuan":  "amount_yuan",
		},
		"OrderHistory": {
			"Code":         "code",
			"TradeDate":    "trade_date",
			"Seq":          "seq",
			"Price":        "price_milli",
			"BuySellDelta": "buy_sell_delta",
			"Volume":       "volume",
		},
	}
}

func splitMappingKey(key string) (string, string) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
