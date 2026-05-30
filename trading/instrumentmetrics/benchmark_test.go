package instrumentmetrics

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkServiceCalculate50InstrumentsFiveMetrics(b *testing.B) {
	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	loader := &fakeLoader{daily: map[string][]DailyBar{}}
	instruments := make([]Instrument, 0, 50)
	for i := 0; i < 50; i++ {
		fullCode := fmt.Sprintf("sz300%03d", i)
		symbol := fmt.Sprintf("300%03d.SZ", i)
		loader.daily[fullCode] = testDailyBars(asOf, []float64{6, 6.5, 7, 7.5, 8, 8.5, 9, 9.5, 10, 10.5, 11, 11.5, 12, 13, 10, 12, 14, 16, 18, 20}, []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 150, 150, 200, 200})
		instruments = append(instruments, Instrument{FullCode: fullCode, Symbol: symbol, Name: symbol, Exchange: "SZ", AssetType: "stock"})
	}
	loader.daily["sz399006"] = testDailyBars(asOf, []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 100, 104, 108, 112, 116, 120}, []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000})

	service := NewService(loader)
	req := Request{
		Market:        "CN",
		AsOfTradeDate: asOf,
		Instruments:   instruments,
		MetricCodes:   []MetricCode{MetricPCT5D, MetricMaxPCT20D, MetricAmountTrend5D, MetricAvgAuctionAmount5D, MetricVSIndexPP},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := service.Calculate(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
		if len(resp.Items) != len(instruments) {
			b.Fatalf("items len = %d, want %d", len(resp.Items), len(instruments))
		}
	}
}
