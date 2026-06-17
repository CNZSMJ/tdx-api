package main

import (
	"testing"
	"time"
)

func TestResolveTradingDayProjectsBeyondWorkdayCoverage(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	coverageEnd := time.Date(2026, 6, 4, 0, 0, 0, 0, loc)
	day := time.Date(2026, 6, 5, 9, 20, 0, 0, loc)

	ok, err := resolveTradingDayWithCoverage(day, coverageEnd, func(time.Time) bool {
		t.Fatal("historical calendar should not be used beyond coverage")
		return false
	})
	if err != nil {
		t.Fatalf("resolve trading day: %v", err)
	}
	if !ok {
		t.Fatalf("2026-06-05 should project as a trading day")
	}
}

func TestResolveTradingDayUsesHistoricalWithinCoverage(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	coverageEnd := time.Date(2026, 6, 5, 0, 0, 0, 0, loc)
	day := time.Date(2026, 6, 5, 9, 20, 0, 0, loc)
	called := false

	ok, err := resolveTradingDayWithCoverage(day, coverageEnd, func(got time.Time) bool {
		called = true
		return false
	})
	if err != nil {
		t.Fatalf("resolve trading day: %v", err)
	}
	if !called {
		t.Fatalf("historical calendar should be used within coverage")
	}
	if ok {
		t.Fatalf("historical calendar result should be preserved")
	}
}
