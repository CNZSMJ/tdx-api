package main

import (
	"fmt"
	"time"
)

var officialExchangeClosures = map[int]map[string]struct{}{
	2025: closureSet(
		"2025-01-01",
		"2025-01-28", "2025-01-29", "2025-01-30", "2025-01-31",
		"2025-02-01", "2025-02-02", "2025-02-03", "2025-02-04",
		"2025-04-04",
		"2025-05-01", "2025-05-02", "2025-05-03", "2025-05-04", "2025-05-05",
		"2025-05-31", "2025-06-01", "2025-06-02",
		"2025-10-01", "2025-10-02", "2025-10-03", "2025-10-04",
		"2025-10-05", "2025-10-06", "2025-10-07", "2025-10-08",
	),
	2026: closureSet(
		"2026-01-01", "2026-01-02", "2026-01-03",
		"2026-02-15", "2026-02-16", "2026-02-17", "2026-02-18", "2026-02-19",
		"2026-02-20", "2026-02-21", "2026-02-22", "2026-02-23",
		"2026-04-04", "2026-04-05", "2026-04-06",
		"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04", "2026-05-05",
		"2026-06-19", "2026-06-20", "2026-06-21",
		"2026-09-25", "2026-09-26", "2026-09-27",
		"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04",
		"2026-10-05", "2026-10-06", "2026-10-07",
	),
}

func closureSet(dates ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(dates))
	for _, date := range dates {
		set[date] = struct{}{}
	}
	return set
}

func normalizeCalendarDay(t time.Time) time.Time {
	year, month, day := t.In(time.Local).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func projectedTradingDay(day time.Time) (bool, bool) {
	day = normalizeCalendarDay(day)
	closures, ok := officialExchangeClosures[day.Year()]
	if !ok {
		return false, false
	}
	switch day.Weekday() {
	case time.Saturday, time.Sunday:
		return false, true
	}
	_, closed := closures[day.Format("2006-01-02")]
	return !closed, true
}

func resolveTradingDayWithCoverage(day, coverageEnd time.Time, historical func(time.Time) bool) (bool, error) {
	day = normalizeCalendarDay(day)
	coverageEnd = normalizeCalendarDay(coverageEnd)
	if !coverageEnd.IsZero() && !day.After(coverageEnd) {
		return historical(day), nil
	}
	if projected, ok := projectedTradingDay(day); ok {
		return projected, nil
	}
	if coverageEnd.IsZero() {
		return false, fmt.Errorf("交易日历未初始化，且未配置 %d 年官方休市安排", day.Year())
	}
	return false, fmt.Errorf("交易日历仅覆盖至 %s，且未配置 %d 年官方休市安排", coverageEnd.Format("2006-01-02"), day.Year())
}

func resolveTradingDay(day time.Time) (bool, error) {
	if manager == nil || manager.Workday == nil {
		return false, fmt.Errorf("交易日模块未初始化")
	}
	return resolveTradingDayWithCoverage(day, manager.Workday.Latest(), manager.Workday.Is)
}
