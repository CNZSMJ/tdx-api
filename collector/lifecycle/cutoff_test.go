package lifecycle

import (
	"path/filepath"
	"testing"
	"time"
)

func TestComputeHotCutoffUsesLatestCompletedTradingDay(t *testing.T) {
	loc := mustShanghai(t)
	days := parseLifecycleDays(t, loc, "20260420", "20260421", "20260422", "20260423", "20260424")

	beforeClose := time.Date(2026, 4, 24, 14, 59, 0, 0, loc)
	before, err := ComputeHotCutoff(days, beforeClose, 3)
	if err != nil {
		t.Fatalf("cutoff before close: %v", err)
	}
	if before.EffectiveTradingDay != "20260423" || before.HotCutoffTradeDate != "20260421" {
		t.Fatalf("before close cutoff = %+v, want effective=20260423 cutoff=20260421", before)
	}

	afterClose := time.Date(2026, 4, 24, 15, 30, 0, 0, loc)
	after, err := ComputeHotCutoff(days, afterClose, 3)
	if err != nil {
		t.Fatalf("cutoff after close: %v", err)
	}
	if after.EffectiveTradingDay != "20260424" || after.HotCutoffTradeDate != "20260422" {
		t.Fatalf("after close cutoff = %+v, want effective=20260424 cutoff=20260422", after)
	}

	weekend := time.Date(2026, 4, 25, 10, 0, 0, 0, loc)
	weekendResult, err := ComputeHotCutoff(days, weekend, 3)
	if err != nil {
		t.Fatalf("cutoff weekend: %v", err)
	}
	if weekendResult.EffectiveTradingDay != "20260424" || weekendResult.HotCutoffTradeDate != "20260422" {
		t.Fatalf("weekend cutoff = %+v, want effective=20260424 cutoff=20260422", weekendResult)
	}
}

func TestComputeHotCutoffFromWorkdayDB(t *testing.T) {
	loc := mustShanghai(t)
	dbPath := filepath.Join(t.TempDir(), "workday.db")
	writeWorkdayFixture(t, dbPath, loc, "20260420", "20260421", "20260422", "20260423", "20260424")

	got, err := ComputeHotCutoffFromWorkdayDB(dbPath, time.Date(2026, 4, 24, 15, 1, 0, 0, loc), 3)
	if err != nil {
		t.Fatalf("cutoff from db: %v", err)
	}
	if got.WorkdaySource != dbPath || got.EffectiveTradingDay != "20260424" || got.HotCutoffTradeDate != "20260422" {
		t.Fatalf("cutoff from db = %+v", got)
	}
}

func TestValidateWorkdayFreshnessRequiresCalendarExplanation(t *testing.T) {
	loc := mustShanghai(t)
	now := time.Date(2026, 2, 22, 10, 0, 0, 0, loc)
	tradingDays := parseLifecycleDays(t, loc, "20260213")
	knownNonTrading := parseLifecycleDateSet(t, loc,
		"20260214", "20260215", "20260216", "20260217",
		"20260218", "20260219", "20260220", "20260221",
	)

	if err := ValidateWorkdayFreshness(WorkdayFreshnessInput{
		TradingDays:         tradingDays,
		KnownNonTradingDays: knownNonTrading,
		Now:                 now,
		MaxStaleCalendarDay: 7,
	}); err != nil {
		t.Fatalf("known non-trading holiday should explain stale latest day: %v", err)
	}

	delete(knownNonTrading, "20260220")
	if err := ValidateWorkdayFreshness(WorkdayFreshnessInput{
		TradingDays:         tradingDays,
		KnownNonTradingDays: knownNonTrading,
		Now:                 now,
		MaxStaleCalendarDay: 7,
	}); err == nil {
		t.Fatalf("missing non-trading explanation should reject stale workday db")
	}
}
