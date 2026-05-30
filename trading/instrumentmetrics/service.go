package instrumentmetrics

import (
	"context"
	"sort"
	"strings"
	"time"
)

type Service struct {
	loader Loader
}

type metricData struct {
	dailyBars      []DailyBar
	benchmarkBars  []DailyBar
	auctionAmounts []AuctionAmount
}

func NewService(loader Loader) *Service {
	return &Service{loader: loader}
}

func (s *Service) Calculate(ctx context.Context, req Request) (Response, error) {
	if s.loader == nil {
		return Response{}, &InvalidArgumentError{Message: "loader is required"}
	}
	if strings.ToUpper(strings.TrimSpace(req.Market)) != "CN" {
		return Response{}, &InvalidArgumentError{Message: "market only supports CN"}
	}
	if req.AsOfTradeDate.IsZero() {
		return Response{}, &InvalidArgumentError{Message: "as_of_trade_date is required"}
	}
	if len(req.Instruments) == 0 {
		return Response{}, &InvalidArgumentError{Message: "full_codes is required"}
	}
	if len(req.MetricCodes) == 0 {
		return Response{}, &InvalidArgumentError{Message: "metric_codes is required"}
	}
	calculators, reqs, err := resolveCalculators(req.MetricCodes)
	if err != nil {
		return Response{}, err
	}

	asOf := tradingDateStart(req.AsOfTradeDate)
	resp := Response{
		Market:        "CN",
		AsOfTradeDate: asOf.Format("2006-01-02"),
		Items:         make([]Item, 0, len(req.Instruments)),
		Failures:      []Error{},
	}
	dailyCache := map[string][]DailyBar{}
	auctionCache := map[string][]AuctionAmount{}
	benchmarkCache := map[string][]DailyBar{}

	for _, instrument := range req.Instruments {
		item := Item{
			FullCode:       instrument.FullCode,
			Symbol:         instrument.Symbol,
			Name:           instrument.Name,
			Exchange:       instrument.Exchange,
			AssetType:      instrument.AssetType,
			Benchmark:      inferBenchmark(instrument),
			Metrics:        map[MetricCode]Metric{},
			MissingMetrics: []MetricCode{},
		}

		dailyBars, err := s.loadDailyBars(ctx, dailyCache, instrument, asOf, reqs.dailyBars)
		if err != nil {
			resp.Failures = append(resp.Failures, Error{FullCode: instrument.FullCode, Symbol: instrument.Symbol, Message: err.Error()})
		}
		if resp.CompletedThrough == "" && len(dailyBars) > 0 {
			resp.CompletedThrough = dailyBars[len(dailyBars)-1].Date.Format("2006-01-02")
		}

		var auctionAmounts []AuctionAmount
		if reqs.auctionSessions > 0 {
			auctionAmounts, err = s.loadAuctionAmounts(ctx, auctionCache, instrument, asOf, reqs.auctionSessions)
			if err != nil {
				resp.Failures = append(resp.Failures, Error{FullCode: instrument.FullCode, Symbol: instrument.Symbol, Message: err.Error()})
			}
		}

		var benchmarkBars []DailyBar
		if reqs.benchmarkDailyBars > 0 && item.Benchmark.FullCode != "" {
			benchmarkBars, err = s.loadBenchmarkBars(ctx, benchmarkCache, item.Benchmark, asOf, reqs.benchmarkDailyBars)
			if err != nil {
				resp.Failures = append(resp.Failures, Error{FullCode: item.Benchmark.FullCode, Symbol: item.Benchmark.IndexCode, Message: err.Error()})
			}
		}

		data := metricData{
			dailyBars:      dailyBars,
			benchmarkBars:  benchmarkBars,
			auctionAmounts: auctionAmounts,
		}
		for _, calc := range calculators {
			metric := calc.calculate(data)
			item.Metrics[calc.code()] = metric
			if metric.Availability == AvailabilityMissing {
				item.MissingMetrics = append(item.MissingMetrics, calc.code())
			}
		}
		resp.Items = append(resp.Items, item)
	}
	resp.Count = len(resp.Items)
	return resp, nil
}

func (s *Service) loadDailyBars(ctx context.Context, cache map[string][]DailyBar, instrument Instrument, before time.Time, count int) ([]DailyBar, error) {
	if count <= 0 {
		return nil, nil
	}
	key := strings.ToLower(strings.TrimSpace(instrument.FullCode))
	if bars, ok := cache[key]; ok {
		return bars, nil
	}
	bars, err := s.loader.LoadDailyBars(ctx, instrument, before, count)
	if err != nil {
		return nil, err
	}
	bars = normalizeDailyBars(bars, before, count)
	cache[key] = bars
	return bars, nil
}

func (s *Service) loadBenchmarkBars(ctx context.Context, cache map[string][]DailyBar, benchmark Benchmark, before time.Time, count int) ([]DailyBar, error) {
	key := strings.ToLower(strings.TrimSpace(benchmark.FullCode))
	if bars, ok := cache[key]; ok {
		return bars, nil
	}
	bars, err := s.loader.LoadDailyBars(ctx, benchmarkInstrument(benchmark), before, count)
	if err != nil {
		return nil, err
	}
	bars = normalizeDailyBars(bars, before, count)
	cache[key] = bars
	return bars, nil
}

func (s *Service) loadAuctionAmounts(ctx context.Context, cache map[string][]AuctionAmount, instrument Instrument, before time.Time, count int) ([]AuctionAmount, error) {
	key := strings.ToLower(strings.TrimSpace(instrument.FullCode))
	if amounts, ok := cache[key]; ok {
		return amounts, nil
	}
	amounts, err := s.loader.LoadAuctionAmounts(ctx, instrument, before, count)
	if err != nil {
		return nil, err
	}
	amounts = normalizeAuctionAmounts(amounts, before, count)
	cache[key] = amounts
	return amounts, nil
}

func normalizeDailyBars(bars []DailyBar, before time.Time, count int) []DailyBar {
	filtered := make([]DailyBar, 0, len(bars))
	cutoff := tradingDateStart(before)
	for _, bar := range bars {
		if bar.Date.IsZero() || !tradingDateStart(bar.Date).Before(cutoff) {
			continue
		}
		filtered = append(filtered, bar)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date.Before(filtered[j].Date)
	})
	if count > 0 && len(filtered) > count {
		filtered = filtered[len(filtered)-count:]
	}
	return filtered
}

func normalizeAuctionAmounts(amounts []AuctionAmount, before time.Time, count int) []AuctionAmount {
	filtered := make([]AuctionAmount, 0, len(amounts))
	cutoff := tradingDateStart(before)
	for _, item := range amounts {
		if item.Date.IsZero() || !tradingDateStart(item.Date).Before(cutoff) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date.Before(filtered[j].Date)
	})
	if count > 0 && len(filtered) > count {
		filtered = filtered[len(filtered)-count:]
	}
	return filtered
}

func missingMetric(unit string, lookback, used int, precision Precision, reason string) Metric {
	return Metric{
		Value:                 nil,
		Unit:                  unit,
		LookbackSessions:      lookback,
		CompletedSessionsUsed: used,
		Precision:             precision,
		Availability:          AvailabilityMissing,
		MissingReason:         reason,
	}
}

func tradingDateStart(day time.Time) time.Time {
	local := day.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}
