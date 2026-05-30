package instrumentmetrics

import "strings"

func inferBenchmark(instrument Instrument) Benchmark {
	fullCode := strings.ToLower(strings.TrimSpace(instrument.FullCode))
	symbol := strings.ToUpper(strings.TrimSpace(instrument.Symbol))
	if strings.HasPrefix(fullCode, "sz300") || strings.HasPrefix(fullCode, "sz301") || strings.HasPrefix(symbol, "300") || strings.HasPrefix(symbol, "301") {
		return Benchmark{Mode: "AUTO_BY_BOARD", FullCode: "sz399006", IndexCode: "399006.SZ", IndexName: "创业板指"}
	}
	if strings.HasPrefix(fullCode, "sz") || strings.HasSuffix(symbol, ".SZ") {
		return Benchmark{Mode: "AUTO_BY_BOARD", FullCode: "sz399001", IndexCode: "399001.SZ", IndexName: "深证成指"}
	}
	if strings.HasPrefix(fullCode, "sh688") || strings.HasPrefix(fullCode, "sh689") || strings.HasPrefix(symbol, "688") || strings.HasPrefix(symbol, "689") {
		return Benchmark{Mode: "AUTO_BY_BOARD", FullCode: "sh000688", IndexCode: "000688.SH", IndexName: "科创50"}
	}
	if strings.HasPrefix(fullCode, "sh") || strings.HasSuffix(symbol, ".SH") {
		return Benchmark{Mode: "AUTO_BY_BOARD", FullCode: "sh000001", IndexCode: "000001.SH", IndexName: "上证指数"}
	}
	return Benchmark{Mode: "UNAVAILABLE"}
}

func benchmarkInstrument(benchmark Benchmark) Instrument {
	exchange := ""
	if strings.HasPrefix(benchmark.FullCode, "sz") {
		exchange = "SZ"
	} else if strings.HasPrefix(benchmark.FullCode, "sh") {
		exchange = "SH"
	}
	return Instrument{
		FullCode:  benchmark.FullCode,
		Symbol:    benchmark.IndexCode,
		Name:      benchmark.IndexName,
		Exchange:  exchange,
		AssetType: "index",
	}
}
