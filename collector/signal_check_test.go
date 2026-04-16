package collector

import (
	"testing"
	"time"
)

func TestEvaluateSignalItemsSupportsTargetedChecks(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 31, 0, 0, time.Local)
	tick := &StockTick{
		Code:      "sh600000",
		Name:      "浦发银行",
		Last:      10.2,
		High:      10.5,
		Low:       9.9,
		Volume:    5000,
		PctChange: 3.2,
	}

	rows := []KlinePublishRow{
		{
			Code:   "sh600000",
			Date:   now.Unix(),
			Open:   priceMilli(10.0),
			High:   priceMilli(10.2),
			Low:    priceMilli(9.9),
			Close:  priceMilli(10.1),
			Volume: 1200,
		},
	}
	for i := 1; i <= 25; i++ {
		rows = append(rows, KlinePublishRow{
			Code:   "sh600000",
			Date:   now.AddDate(0, 0, -i).Unix(),
			Open:   priceMilli(9.8),
			High:   priceMilli(10.0),
			Low:    priceMilli(9.6),
			Close:  priceMilli(9.9),
			Volume: 1000,
		})
	}

	items := evaluateSignalItems(tick, rows, 20, 5, now, 2.0)
	if len(items) != 2 {
		t.Fatalf("signal item count = %d, want 2", len(items))
	}

	signalTypes := make(map[string]bool, len(items))
	for _, item := range items {
		signalTypes[item.SignalType] = true
	}
	if !signalTypes["new_high"] {
		t.Fatalf("expected new_high hit, got %#v", items)
	}
	if !signalTypes["volume_spike"] {
		t.Fatalf("expected volume_spike hit, got %#v", items)
	}
}

func TestSignalServiceCheckCodesWorksWithAttachedTicker(t *testing.T) {
	service := NewSignalService(SignalConfig{
		Now:          func() time.Time { return time.Date(2026, 4, 15, 14, 31, 0, 0, time.Local) },
		KlineBaseDir: t.TempDir(),
	})
	service.AttachTicker(&TickerService{})

	items, err := service.CheckCodes([]string{"sh600000"}, []string{"new_high"})
	if err != nil {
		t.Fatalf("CheckCodes returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items without ticker/cache data, got %#v", items)
	}
}

func priceMilli(value float64) PriceMilli {
	return PriceMilli(value * 1000)
}
