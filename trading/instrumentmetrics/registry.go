package instrumentmetrics

type requirements struct {
	dailyBars          int
	benchmarkDailyBars int
	auctionSessions    int
}

type calculator interface {
	code() MetricCode
	requirements() requirements
	calculate(data metricData) Metric
}

var defaultCalculators = map[MetricCode]calculator{
	MetricPCT5D:              pct5DCalculator{},
	MetricMaxPCT20D:          maxPCT20DCalculator{},
	MetricAmountTrend5D:      amountTrend5DCalculator{},
	MetricAvgAuctionAmount5D: avgAuctionAmount5DCalculator{},
	MetricVSIndexPP:          vsIndexPPCalculator{},
}

func ValidateMetricCodes(codes []MetricCode) error {
	_, _, err := resolveCalculators(codes)
	return err
}

func resolveCalculators(codes []MetricCode) ([]calculator, requirements, error) {
	seen := make(map[MetricCode]struct{}, len(codes))
	out := make([]calculator, 0, len(codes))
	var req requirements
	for _, code := range codes {
		if _, ok := seen[code]; ok {
			continue
		}
		calc, ok := defaultCalculators[code]
		if !ok {
			return nil, requirements{}, &InvalidArgumentError{Message: "unknown metric_code: " + string(code)}
		}
		seen[code] = struct{}{}
		out = append(out, calc)
		calcReq := calc.requirements()
		req.dailyBars = maxInt(req.dailyBars, calcReq.dailyBars)
		req.benchmarkDailyBars = maxInt(req.benchmarkDailyBars, calcReq.benchmarkDailyBars)
		req.auctionSessions = maxInt(req.auctionSessions, calcReq.auctionSessions)
	}
	return out, req, nil
}
