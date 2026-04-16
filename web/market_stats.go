package main

import (
	"fmt"
	"strings"

	collectorpkg "github.com/injoyai/tdx/collector"
)

func parseMarketStatsAssetType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(collectorpkg.AssetTypeStock):
		return string(collectorpkg.AssetTypeStock), nil
	case string(collectorpkg.AssetTypeETF):
		return string(collectorpkg.AssetTypeETF), nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("asset_type 参数无效，应为 stock、etf 或 all")
	}
}

func buildMarketStatsData(ticks []collectorpkg.StockTick, assetType string) map[string]interface{} {
	byExchange := map[string]collectorpkg.AssetOverview{
		"sh": {},
		"sz": {},
		"bj": {},
	}
	var stockSummary collectorpkg.AssetOverview
	var etfSummary collectorpkg.AssetOverview

	add := func(dst *collectorpkg.AssetOverview, tick collectorpkg.StockTick, countLimits bool) {
		dst.Total++
		switch {
		case tick.PctChange > 0:
			dst.Up++
		case tick.PctChange < 0:
			dst.Down++
		default:
			dst.Flat++
		}
		dst.TotalAmount += tick.Amount
		dst.TotalVolume += tick.Volume
		if countLimits {
			if tick.IsLimitUp {
				dst.LimitUp++
			}
			if tick.IsLimitDown {
				dst.LimitDown++
			}
		}
	}

	matchesAssetType := func(tick collectorpkg.StockTick) bool {
		switch assetType {
		case string(collectorpkg.AssetTypeETF):
			return tick.AssetType == string(collectorpkg.AssetTypeETF)
		case "all":
			return tick.AssetType == string(collectorpkg.AssetTypeStock) || tick.AssetType == string(collectorpkg.AssetTypeETF)
		default:
			return tick.AssetType == string(collectorpkg.AssetTypeStock)
		}
	}

	for _, tick := range ticks {
		switch tick.AssetType {
		case string(collectorpkg.AssetTypeStock):
			add(&stockSummary, tick, true)
		case string(collectorpkg.AssetTypeETF):
			add(&etfSummary, tick, false)
		}

		if !matchesAssetType(tick) {
			continue
		}
		exchange := strings.ToLower(strings.TrimSpace(tick.Exchange))
		bucket, ok := byExchange[exchange]
		if !ok {
			bucket = collectorpkg.AssetOverview{}
		}
		add(&bucket, tick, tick.AssetType == string(collectorpkg.AssetTypeStock))
		byExchange[exchange] = bucket
	}

	finalize := func(overview collectorpkg.AssetOverview) map[string]interface{} {
		if overview.Total > 0 {
			overview.UpRatio = float64(int(float64(overview.Up)/float64(overview.Total)*10000+0.5)) / 100
		}
		return map[string]interface{}{
			"total":        overview.Total,
			"up":           overview.Up,
			"down":         overview.Down,
			"flat":         overview.Flat,
			"up_ratio":     overview.UpRatio,
			"limit_up":     overview.LimitUp,
			"limit_down":   overview.LimitDown,
			"total_amount": overview.TotalAmount,
			"total_volume": overview.TotalVolume,
		}
	}

	return map[string]interface{}{
		"asset_type": assetType,
		"sh":         finalize(byExchange["sh"]),
		"sz":         finalize(byExchange["sz"]),
		"bj":         finalize(byExchange["bj"]),
		"summary": map[string]interface{}{
			"stock": finalize(stockSummary),
			"etf":   finalize(etfSummary),
		},
	}
}
