package instrumentmetrics

type avgAuctionAmount5DCalculator struct{}

func (avgAuctionAmount5DCalculator) code() MetricCode { return MetricAvgAuctionAmount5D }

func (avgAuctionAmount5DCalculator) requirements() requirements {
	return requirements{auctionSessions: 5}
}

func (avgAuctionAmount5DCalculator) calculate(data metricData) Metric {
	amounts := lastAuctionAmounts(data.auctionAmounts, 5)
	if len(amounts) == 0 {
		return missingMetric("CNY", 5, 0, PrecisionUnavailable, "auction_history_unavailable")
	}
	sum := 0.0
	for _, item := range amounts {
		sum += item.Amount
	}
	availability := AvailabilityAvailable
	if len(amounts) < 5 {
		availability = AvailabilityPartial
	}
	return Metric{
		Value:                 round2(sum / float64(len(amounts))),
		Unit:                  "CNY",
		LookbackSessions:      5,
		CompletedSessionsUsed: len(amounts),
		Precision:             PrecisionAuctionHistory,
		Availability:          availability,
	}
}

func lastAuctionAmounts(amounts []AuctionAmount, count int) []AuctionAmount {
	if count <= 0 || len(amounts) <= count {
		return amounts
	}
	return amounts[len(amounts)-count:]
}
