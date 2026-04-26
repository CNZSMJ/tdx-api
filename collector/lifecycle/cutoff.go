package lifecycle

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

const DefaultHotRetentionTradingDays = 180
const DefaultWorkdayMaxStaleCalendarDays = 7

type CutoffResult struct {
	EvaluatedAt           time.Time
	EffectiveTradingDay   string
	HotCutoffTradeDate    string
	RetentionTradingDays  int
	CompletedTradingDays  int
	WorkdaySource         string
	WorkdayDataSourceNote string
}

type WorkdayFreshnessInput struct {
	TradingDays         []time.Time
	KnownNonTradingDays map[string]bool
	Now                 time.Time
	MaxStaleCalendarDay int
}

func ComputeHotCutoff(tradingDays []time.Time, now time.Time, retentionTradingDays int) (CutoffResult, error) {
	loc, err := shanghaiLocation()
	if err != nil {
		return CutoffResult{}, err
	}
	if now.IsZero() {
		now = time.Now().In(loc)
	}
	if retentionTradingDays <= 0 {
		retentionTradingDays = DefaultHotRetentionTradingDays
	}

	days := normalizeTradingDays(tradingDays, loc)
	if len(days) == 0 {
		return CutoffResult{}, errors.New("no trading days available")
	}

	limit := latestCompletedDateLimit(now.In(loc), loc)
	completed := make([]time.Time, 0, len(days))
	for _, day := range days {
		if !dateAfter(day, limit, loc) {
			completed = append(completed, day)
		}
	}
	if len(completed) == 0 {
		return CutoffResult{}, fmt.Errorf("no completed trading day at or before %s", limit.Format("20060102"))
	}
	cutoffIndex := len(completed) - retentionTradingDays
	if cutoffIndex < 0 {
		cutoffIndex = 0
	}
	effective := completed[len(completed)-1]
	cutoff := completed[cutoffIndex]
	return CutoffResult{
		EvaluatedAt:           now.In(loc),
		EffectiveTradingDay:   effective.Format("20060102"),
		HotCutoffTradeDate:    cutoff.Format("20060102"),
		RetentionTradingDays:  retentionTradingDays,
		CompletedTradingDays:  len(completed),
		WorkdayDataSourceNote: "local workday calendar",
	}, nil
}

func ComputeHotCutoffFromWorkdayDB(path string, now time.Time, retentionTradingDays int) (CutoffResult, error) {
	days, err := LoadTradingDaysFromWorkdayDB(path)
	if err != nil {
		return CutoffResult{}, err
	}
	result, err := ComputeHotCutoff(days, now, retentionTradingDays)
	if err != nil {
		return CutoffResult{}, err
	}
	result.WorkdaySource = path
	return result, nil
}

func LoadTradingDaysFromWorkdayDB(path string) ([]time.Time, error) {
	if path == "" {
		return nil, errors.New("workday db path is required")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT date FROM workday WHERE date IS NOT NULL AND date != '' ORDER BY date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	loc, err := shanghaiLocation()
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		day, err := time.ParseInLocation("20060102", text, loc)
		if err != nil {
			return nil, fmt.Errorf("parse workday %q: %w", text, err)
		}
		out = append(out, day)
	}
	return out, rows.Err()
}

func ValidateWorkdayFreshness(input WorkdayFreshnessInput) error {
	loc, err := shanghaiLocation()
	if err != nil {
		return err
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().In(loc)
	}
	maxStale := input.MaxStaleCalendarDay
	if maxStale <= 0 {
		maxStale = DefaultWorkdayMaxStaleCalendarDays
	}
	days := normalizeTradingDays(input.TradingDays, loc)
	if len(days) == 0 {
		return errors.New("workday freshness guard requires at least one trading day")
	}

	limit := latestCompletedDateLimit(now.In(loc), loc)
	var latest time.Time
	for _, day := range days {
		if !dateAfter(day, limit, loc) {
			latest = day
		}
	}
	if latest.IsZero() {
		return fmt.Errorf("no completed trading day at or before %s", limit.Format("20060102"))
	}
	ageDays := int(dayStart(now, loc).Sub(dayStart(latest, loc)).Hours() / 24)
	if ageDays <= maxStale {
		return nil
	}

	for day := dayStart(latest, loc).AddDate(0, 0, 1); day.Before(dayStart(now, loc)); day = day.AddDate(0, 0, 1) {
		key := day.Format("20060102")
		if !input.KnownNonTradingDays[key] {
			return fmt.Errorf("workday db stale: latest=%s age_days=%d missing non-trading explanation for %s", latest.Format("20060102"), ageDays, key)
		}
	}
	return nil
}

func normalizeTradingDays(tradingDays []time.Time, loc *time.Location) []time.Time {
	seen := make(map[string]time.Time, len(tradingDays))
	for _, day := range tradingDays {
		if day.IsZero() {
			continue
		}
		normalized := dayStart(day, loc)
		seen[normalized.Format("20060102")] = normalized
	}
	out := make([]time.Time, 0, len(seen))
	for _, day := range seen {
		out = append(out, day)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func latestCompletedDateLimit(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	limit := dayStart(local, loc)
	closeTime := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, loc)
	if local.Before(closeTime) {
		limit = limit.AddDate(0, 0, -1)
	}
	return limit
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func dateAfter(left, right time.Time, loc *time.Location) bool {
	return dayStart(left, loc).After(dayStart(right, loc))
}

func shanghaiLocation() (*time.Location, error) {
	return time.LoadLocation("Asia/Shanghai")
}
