package instrumentmetrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceCalculatesDailyMetricsAndDeduplicatesBenchmarks(t *testing.T) {
	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	loader := &fakeLoader{
		daily: map[string][]DailyBar{
			"sz300394": testDailyBars(asOf, []float64{6, 6.5, 7, 7.5, 8, 8.5, 9, 9.5, 10, 10.5, 11, 11.5, 12, 13, 10, 12, 14, 16, 18, 20}, []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 150, 150, 200, 200}),
			"sz300750": testDailyBars(asOf, []float64{20, 20.5, 21, 21.5, 22, 22.5, 23, 23.5, 24, 24.5, 25, 25.5, 26, 26.5, 27, 27.5, 28, 28.5, 29, 29.5}, []float64{200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200}),
			"sz399006": testDailyBars(asOf, []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 100, 104, 108, 112, 116, 120}, []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000}),
		},
	}
	service := NewService(loader)

	resp, err := service.Calculate(context.Background(), Request{
		Market:        "CN",
		AsOfTradeDate: asOf,
		Instruments: []Instrument{
			{FullCode: "sz300394", Symbol: "300394.SZ", Name: "示例科技", Exchange: "SZ", AssetType: "stock"},
			{FullCode: "sz300750", Symbol: "300750.SZ", Name: "示例股份", Exchange: "SZ", AssetType: "stock"},
		},
		MetricCodes: []MetricCode{MetricPCT5D, MetricMaxPCT20D, MetricAmountTrend5D, MetricVSIndexPP},
	})
	if err != nil {
		t.Fatalf("calculate metrics: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(resp.Items))
	}
	if loader.dailyCalls["sz399006"] != 1 {
		t.Fatalf("benchmark daily load calls = %d, want 1", loader.dailyCalls["sz399006"])
	}

	first := resp.Items[0]
	if first.Benchmark.IndexCode != "399006.SZ" {
		t.Fatalf("benchmark = %#v, want 399006.SZ", first.Benchmark)
	}
	if got := first.Metrics[MetricPCT5D].Value; got != 100.0 {
		t.Fatalf("PCT_5D = %#v, want 100.0", got)
	}
	if got := first.Metrics[MetricAmountTrend5D].Value; got != "EXPANDING" {
		t.Fatalf("AMOUNT_TREND_5D = %#v, want EXPANDING", got)
	}
	if got := first.Metrics[MetricMaxPCT20D].Value; got != 316.67 {
		t.Fatalf("MAX_PCT_20D = %#v, want 316.67", got)
	}
	if got := first.Metrics[MetricVSIndexPP].Value; got != 80.0 {
		t.Fatalf("VS_INDEX_PP = %#v, want 80.0", got)
	}
}

func TestServiceMarksAuctionAmountMissingWithoutBlockingOtherMetrics(t *testing.T) {
	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	loader := &fakeLoader{
		daily: map[string][]DailyBar{
			"sh600000": testDailyBars(asOf, []float64{10, 11, 12, 13, 14, 15}, []float64{100, 100, 100, 100, 100, 100}),
		},
	}
	service := NewService(loader)

	resp, err := service.Calculate(context.Background(), Request{
		Market:        "CN",
		AsOfTradeDate: asOf,
		Instruments:   []Instrument{{FullCode: "sh600000", Symbol: "600000.SH", Name: "浦发银行", Exchange: "SH", AssetType: "stock"}},
		MetricCodes:   []MetricCode{MetricPCT5D, MetricAvgAuctionAmount5D},
	})
	if err != nil {
		t.Fatalf("calculate metrics: %v", err)
	}
	item := resp.Items[0]
	if got := item.Metrics[MetricPCT5D].Value; got != 50.0 {
		t.Fatalf("PCT_5D = %#v, want 50.0", got)
	}
	auction := item.Metrics[MetricAvgAuctionAmount5D]
	if auction.Availability != AvailabilityMissing || auction.Precision != PrecisionUnavailable || auction.Value != nil {
		t.Fatalf("auction metric = %#v, want missing unavailable nil value", auction)
	}
	if len(item.MissingMetrics) != 1 || item.MissingMetrics[0] != MetricAvgAuctionAmount5D {
		t.Fatalf("missing_metrics = %#v, want AVG_AUCTION_AMOUNT_5D", item.MissingMetrics)
	}
}

func TestServiceRejectsUnknownMetric(t *testing.T) {
	service := NewService(&fakeLoader{})
	_, err := service.Calculate(context.Background(), Request{
		Market:        "CN",
		AsOfTradeDate: time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local),
		Instruments:   []Instrument{{FullCode: "sh600000", Symbol: "600000.SH"}},
			MetricCodes:   []MetricCode{"BAD_METRIC_CODE"},
	})
	if err == nil {
		t.Fatalf("expected unknown metric error")
	}
	var invalid *InvalidArgumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %T %v, want InvalidArgumentError", err, err)
	}
}

type fakeLoader struct {
	daily        map[string][]DailyBar
	auction      map[string][]AuctionAmount
	dailyCalls   map[string]int
	auctionCalls map[string]int
}

func (l *fakeLoader) LoadDailyBars(_ context.Context, instrument Instrument, _ time.Time, _ int) ([]DailyBar, error) {
	if l.dailyCalls == nil {
		l.dailyCalls = map[string]int{}
	}
	l.dailyCalls[instrument.FullCode]++
	return l.daily[instrument.FullCode], nil
}

func (l *fakeLoader) LoadAuctionAmounts(_ context.Context, instrument Instrument, _ time.Time, _ int) ([]AuctionAmount, error) {
	if l.auctionCalls == nil {
		l.auctionCalls = map[string]int{}
	}
	l.auctionCalls[instrument.FullCode]++
	return l.auction[instrument.FullCode], nil
}

func testDailyBars(asOf time.Time, closes []float64, amounts []float64) []DailyBar {
	out := make([]DailyBar, 0, len(closes))
	start := asOf.AddDate(0, 0, -len(closes))
	for i, close := range closes {
		low := close
		if i == 0 {
			low = close * 0.8
		}
		out = append(out, DailyBar{
			Date:   start.AddDate(0, 0, i),
			Open:   close,
			High:   close,
			Low:    low,
			Close:  close,
			Amount: amounts[i],
		})
	}
	return out
}
