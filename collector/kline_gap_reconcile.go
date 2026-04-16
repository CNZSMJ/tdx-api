package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type KlineGapReconcileOptions struct {
	AssetType  AssetType
	Instrument string
	Period     KlinePeriod
	StartDate  string
	EndDate    string
	Limit      int
	DryRun     bool
}

type KlineGapReconcileTask struct {
	AssetType  AssetType   `json:"asset_type"`
	Instrument string      `json:"instrument"`
	Period     KlinePeriod `json:"period"`
	Date       string      `json:"date"`
}

type KlineGapReconcileReport struct {
	DryRun            bool                    `json:"dry_run"`
	Scanned           int                     `json:"scanned"`
	MatchedGaps       int                     `json:"matched_gaps"`
	Planned           int                     `json:"planned"`
	Executed          int                     `json:"executed"`
	Succeeded         int                     `json:"succeeded"`
	Failed            int                     `json:"failed"`
	RemainingOpenGaps int                     `json:"remaining_open_gaps"`
	StartedAt         time.Time               `json:"started_at"`
	CompletedAt       time.Time               `json:"completed_at"`
	Touched           []string                `json:"touched_tables"`
	Tasks             []KlineGapReconcileTask `json:"tasks"`
	Errors            []string                `json:"errors,omitempty"`
}

type klineGapReconcileDateRange struct {
	start string
	end   string
}

func (s *KlineService) ReconcileCollectGaps(ctx context.Context, opts KlineGapReconcileOptions) (*KlineGapReconcileReport, error) {
	report := &KlineGapReconcileReport{
		DryRun:    opts.DryRun,
		StartedAt: time.Now(),
		Touched:   []string{"collector_gap", "collector_cursor", "kline/*.db"},
		Tasks:     make([]KlineGapReconcileTask, 0, 32),
		Errors:    make([]string, 0, 8),
	}
	defer func() {
		report.CompletedAt = time.Now()
	}()

	dateRange, err := normalizeKlineGapReconcileDateRange(opts.StartDate, opts.EndDate)
	if err != nil {
		return nil, err
	}

	gaps, err := s.store.ListOpenCollectGaps("kline", "", "", "")
	if err != nil {
		return nil, err
	}
	report.Scanned = len(gaps)

	lookup, err := s.cleanupTradingDayLookup(ctx, gaps, KlineGapCleanupOptions{
		AssetType:  opts.AssetType,
		Instrument: opts.Instrument,
		Period:     opts.Period,
	})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, 32)
	stopPlanning := false
	for i := range gaps {
		if stopPlanning {
			break
		}
		gap := gaps[i]
		taskDates, matched, err := s.planGapReconcileDates(ctx, lookup, gap, opts, dateRange)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		report.MatchedGaps++

		for _, date := range taskDates {
			task := KlineGapReconcileTask{
				AssetType:  AssetType(gap.AssetType),
				Instrument: gap.Instrument,
				Period:     KlinePeriod(gap.Period),
				Date:       date,
			}
			key := fmt.Sprintf("%s|%s|%s|%s", task.AssetType, task.Instrument, task.Period, task.Date)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			report.Tasks = append(report.Tasks, task)
			if opts.Limit > 0 && len(report.Tasks) >= opts.Limit {
				stopPlanning = true
				break
			}
		}
	}
	report.Planned = len(report.Tasks)
	if opts.DryRun {
		report.RemainingOpenGaps, err = s.countMatchingOpenCollectGaps(ctx, lookup, dateRange, opts)
		if err != nil {
			return nil, err
		}
		return report, nil
	}

	for _, task := range report.Tasks {
		report.Executed++
		ok, err := s.reconcileGapTask(ctx, task)
		if err != nil {
			if isFatalCtxError(err) {
				return nil, err
			}
			report.Failed++
			report.Errors = append(report.Errors, fmt.Sprintf("%s/%s/%s: %v", task.Instrument, task.Period, task.Date, err))
			continue
		}
		if ok {
			report.Succeeded++
			continue
		}
		report.Failed++
		report.Errors = append(report.Errors, fmt.Sprintf("%s/%s/%s: gap remains open after provider replay and live-cache fallback", task.Instrument, task.Period, task.Date))
	}

	report.RemainingOpenGaps, err = s.countMatchingOpenCollectGaps(ctx, lookup, dateRange, opts)
	if err != nil {
		return nil, err
	}
	return report, nil
}

func (s *KlineService) reconcileGapTask(ctx context.Context, task KlineGapReconcileTask) (bool, error) {
	query := KlineCollectQuery{
		Code:      task.Instrument,
		AssetType: task.AssetType,
		Period:    task.Period,
	}
	if err := s.ReconcileDate(ctx, query, task.Date); err != nil {
		return false, err
	}
	open, err := s.hasOpenGapTask(query, task.Date)
	if err != nil {
		return false, err
	}
	if !open {
		return true, nil
	}
	if _, err := s.ReconcileDateFromLiveCache(ctx, query, task.Date); err != nil {
		return false, err
	}
	open, err = s.hasOpenGapTask(query, task.Date)
	if err != nil {
		return false, err
	}
	return !open, nil
}

func (s *KlineService) hasOpenGapTask(query KlineCollectQuery, date string) (bool, error) {
	gaps, err := s.store.ListOpenCollectGaps("kline", string(query.AssetType), query.Code, string(query.Period))
	if err != nil {
		return false, err
	}
	target, err := parseTradeCursor(date)
	if err != nil {
		return false, err
	}
	targetDay := normalizeTradingDay(target)
	for _, gap := range gaps {
		startUnix, err := strconv.ParseInt(gap.StartKey, 10, 64)
		if err != nil {
			return false, err
		}
		endUnix, err := strconv.ParseInt(gap.EndKey, 10, 64)
		if err != nil {
			return false, err
		}
		startDay := normalizeTradingDay(time.Unix(startUnix, 0))
		endDay := normalizeTradingDay(time.Unix(endUnix, 0))
		if !targetDay.Before(startDay) && !targetDay.After(endDay) {
			return true, nil
		}
	}
	return false, nil
}

func normalizeKlineGapReconcileDateRange(startRaw, endRaw string) (klineGapReconcileDateRange, error) {
	start := strings.TrimSpace(startRaw)
	end := strings.TrimSpace(endRaw)
	if start == "" && end == "" {
		return klineGapReconcileDateRange{}, nil
	}
	if start == "" {
		start = end
	}
	if end == "" {
		end = start
	}
	if _, err := parseTradeCursor(start); err != nil {
		return klineGapReconcileDateRange{}, fmt.Errorf("invalid start date %q: %w", start, err)
	}
	if _, err := parseTradeCursor(end); err != nil {
		return klineGapReconcileDateRange{}, fmt.Errorf("invalid end date %q: %w", end, err)
	}
	if end < start {
		return klineGapReconcileDateRange{}, fmt.Errorf("invalid date range: %s > %s", start, end)
	}
	return klineGapReconcileDateRange{start: start, end: end}, nil
}

func (r klineGapReconcileDateRange) contains(date string) bool {
	if r.start == "" && r.end == "" {
		return true
	}
	return date >= r.start && date <= r.end
}

func (r klineGapReconcileDateRange) overlaps(start, end time.Time) bool {
	if r.start == "" && r.end == "" {
		return true
	}
	startDate := normalizeTradingDay(start).Format("20060102")
	endDate := normalizeTradingDay(end).Format("20060102")
	return !(endDate < r.start || startDate > r.end)
}

func (s *KlineService) planGapReconcileDates(ctx context.Context, lookup tradingDayLookup, gap CollectGapRecord, opts KlineGapReconcileOptions, dateRange klineGapReconcileDateRange) ([]string, bool, error) {
	if !klineGapMatchesCleanupFilter(gap, KlineGapCleanupOptions{
		AssetType:  opts.AssetType,
		Instrument: opts.Instrument,
		Period:     opts.Period,
	}) {
		return nil, false, nil
	}

	startUnix, err := strconv.ParseInt(gap.StartKey, 10, 64)
	if err != nil {
		return nil, false, err
	}
	endUnix, err := strconv.ParseInt(gap.EndKey, 10, 64)
	if err != nil {
		return nil, false, err
	}
	start, end, ok, err := s.normalizeKlineGapWindowWithLookup(ctx, lookup, KlinePeriod(gap.Period), time.Unix(startUnix, 0), time.Unix(endUnix, 0))
	if err != nil {
		return nil, false, err
	}
	if !ok || !dateRange.overlaps(start, end) {
		return nil, false, nil
	}

	dates, err := tradingDatesBetween(ctx, lookup, start, end, dateRange)
	if err != nil {
		return nil, false, err
	}
	return dates, true, nil
}

func tradingDatesBetween(ctx context.Context, lookup tradingDayLookup, start, end time.Time, dateRange klineGapReconcileDateRange) ([]string, error) {
	dates := make([]string, 0, 8)
	for day := normalizeTradingDay(start); !day.After(normalizeTradingDay(end)); day = day.AddDate(0, 0, 1) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		trading, err := lookup(ctx, day)
		if err != nil {
			return nil, err
		}
		if !trading {
			continue
		}
		date := day.Format("20060102")
		if !dateRange.contains(date) {
			continue
		}
		dates = append(dates, date)
	}
	return dates, nil
}

func (s *KlineService) countMatchingOpenCollectGaps(ctx context.Context, lookup tradingDayLookup, dateRange klineGapReconcileDateRange, opts KlineGapReconcileOptions) (int, error) {
	gaps, err := s.store.ListOpenCollectGaps("kline", "", "", "")
	if err != nil {
		return 0, err
	}

	count := 0
	for i := range gaps {
		_, matched, err := s.planGapReconcileDates(ctx, lookup, gaps[i], opts, dateRange)
		if err != nil {
			return 0, err
		}
		if matched {
			count++
		}
	}
	return count, nil
}
