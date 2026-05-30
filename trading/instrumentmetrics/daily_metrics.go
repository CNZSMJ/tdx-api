package instrumentmetrics

import "math"

type pct5DCalculator struct{}

func (pct5DCalculator) code() MetricCode { return MetricPCT5D }

func (pct5DCalculator) requirements() requirements {
	return requirements{dailyBars: 6}
}

func (pct5DCalculator) calculate(data metricData) Metric {
	return calculatePCT5D(data.dailyBars)
}

type maxPCT20DCalculator struct{}

func (maxPCT20DCalculator) code() MetricCode { return MetricMaxPCT20D }

func (maxPCT20DCalculator) requirements() requirements {
	return requirements{dailyBars: 20}
}

func (maxPCT20DCalculator) calculate(data metricData) Metric {
	bars := lastDailyBars(data.dailyBars, 20)
	if len(bars) < 2 {
		return missingMetric("pct", 20, len(bars), PrecisionUnavailable, "insufficient_daily_bars")
	}
	minLow := 0.0
	maxPct := 0.0
	for _, bar := range bars {
		if bar.Low > 0 && (minLow == 0 || bar.Low < minLow) {
			minLow = bar.Low
		}
		if minLow > 0 && bar.High > 0 {
			pct := (bar.High/minLow - 1) * 100
			if pct > maxPct {
				maxPct = pct
			}
		}
	}
	availability := AvailabilityAvailable
	if len(bars) < 20 {
		availability = AvailabilityPartial
	}
	return Metric{
		Value:                 round2(maxPct),
		Unit:                  "pct",
		LookbackSessions:      20,
		CompletedSessionsUsed: len(bars),
		Precision:             PrecisionDailyBar,
		Availability:          availability,
	}
}

type amountTrend5DCalculator struct{}

func (amountTrend5DCalculator) code() MetricCode { return MetricAmountTrend5D }

func (amountTrend5DCalculator) requirements() requirements {
	return requirements{dailyBars: 5}
}

func (amountTrend5DCalculator) calculate(data metricData) Metric {
	bars := lastDailyBars(data.dailyBars, 5)
	if len(bars) < 5 {
		return missingMetric("enum", 5, len(bars), PrecisionUnavailable, "insufficient_daily_bars")
	}
	prevAvg := averageDailyAmount(bars[:3])
	latestAvg := averageDailyAmount(bars[3:])
	if prevAvg <= 0 {
		return missingMetric("enum", 5, len(bars), PrecisionUnavailable, "invalid_previous_amount")
	}
	ratio := latestAvg / prevAvg
	value := "STABLE"
	if ratio <= 0.8 {
		value = "SHRINKING"
	} else if ratio >= 1.2 {
		value = "EXPANDING"
	}
	return Metric{
		Value:                 value,
		Unit:                  "enum",
		LookbackSessions:      5,
		CompletedSessionsUsed: 5,
		Precision:             PrecisionDailyBar,
		Availability:          AvailabilityAvailable,
	}
}

type vsIndexPPCalculator struct{}

func (vsIndexPPCalculator) code() MetricCode { return MetricVSIndexPP }

func (vsIndexPPCalculator) requirements() requirements {
	return requirements{dailyBars: 6, benchmarkDailyBars: 6}
}

func (vsIndexPPCalculator) calculate(data metricData) Metric {
	instrumentPct := calculatePCT5D(data.dailyBars)
	indexPct := calculatePCT5D(data.benchmarkBars)
	used := minInt(instrumentPct.CompletedSessionsUsed, indexPct.CompletedSessionsUsed)
	if instrumentPct.Availability != AvailabilityAvailable {
		return missingMetric("pp", 5, used, PrecisionUnavailable, "insufficient_daily_bars")
	}
	if indexPct.Availability != AvailabilityAvailable {
		return missingMetric("pp", 5, used, PrecisionUnavailable, "insufficient_benchmark_bars")
	}
	return Metric{
		Value:                 round2(instrumentPct.Value.(float64) - indexPct.Value.(float64)),
		Unit:                  "pp",
		LookbackSessions:      5,
		CompletedSessionsUsed: 5,
		Precision:             PrecisionDailyBar,
		Availability:          AvailabilityAvailable,
	}
}

func calculatePCT5D(bars []DailyBar) Metric {
	bars = lastDailyBars(bars, 6)
	if len(bars) < 6 {
		return missingMetric("pct", 5, maxInt(0, len(bars)-1), PrecisionUnavailable, "insufficient_daily_bars")
	}
	start := bars[0].Close
	end := bars[len(bars)-1].Close
	if start <= 0 {
		return missingMetric("pct", 5, 5, PrecisionUnavailable, "invalid_start_close")
	}
	return Metric{
		Value:                 round2((end/start - 1) * 100),
		Unit:                  "pct",
		LookbackSessions:      5,
		CompletedSessionsUsed: 5,
		Precision:             PrecisionDailyBar,
		Availability:          AvailabilityAvailable,
	}
}

func lastDailyBars(bars []DailyBar, count int) []DailyBar {
	if count <= 0 || len(bars) <= count {
		return bars
	}
	return bars[len(bars)-count:]
}

func averageDailyAmount(bars []DailyBar) float64 {
	if len(bars) == 0 {
		return 0
	}
	sum := 0.0
	for _, bar := range bars {
		sum += bar.Amount
	}
	return sum / float64(len(bars))
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
