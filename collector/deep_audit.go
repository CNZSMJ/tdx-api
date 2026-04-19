package collector

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (r *Runtime) ResolveDeepAuditDates(ctx context.Context, startDate, endDate string, backlogOnly bool, limit int) ([]string, error) {
	startDate = strings.TrimSpace(startDate)
	endDate = strings.TrimSpace(endDate)
	if startDate == "" && endDate == "" && backlogOnly {
		var err error
		startDate, endDate, err = r.resolveDeepAuditBacklogWindow()
		if err != nil {
			return nil, err
		}
	}
	if startDate == "" && endDate == "" {
		return nil, nil
	}
	if startDate == "" {
		startDate = endDate
	}
	if endDate == "" {
		endDate = startDate
	}

	start, err := parseTradeCursor(startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid deep audit start date %q: %w", startDate, err)
	}
	end, err := parseTradeCursor(endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid deep audit end date %q: %w", endDate, err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("invalid deep audit range: %s > %s", startDate, endDate)
	}

	items, err := r.provider.TradingDays(ctx, TradingDayQuery{
		Start: start.AddDate(0, 0, -1),
		End:   end.AddDate(0, 0, 1),
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Time.Equal(items[j].Time) {
			return items[i].Date < items[j].Date
		}
		return items[i].Time.Before(items[j].Time)
	})

	seen := make(map[string]struct{}, len(items))
	dates := make([]string, 0, len(items))
	for _, item := range items {
		if item.Date == "" || item.Date < startDate || item.Date > endDate {
			continue
		}
		if _, ok := seen[item.Date]; ok {
			continue
		}
		seen[item.Date] = struct{}{}
		dates = append(dates, item.Date)
	}
	if limit > 0 && len(dates) > limit {
		dates = dates[:limit]
	}
	return dates, nil
}

func (r *Runtime) resolveDeepAuditBacklogWindow() (string, string, error) {
	if r == nil || r.store == nil {
		return "", "", nil
	}
	gaps, err := r.store.ListOpenCollectGaps("kline", "", "", "")
	if err != nil {
		return "", "", err
	}
	startDate := ""
	endDate := ""
	for _, gap := range gaps {
		startUnix, err := strconv.ParseInt(strings.TrimSpace(gap.StartKey), 10, 64)
		if err != nil {
			return "", "", err
		}
		endUnix, err := strconv.ParseInt(strings.TrimSpace(gap.EndKey), 10, 64)
		if err != nil {
			return "", "", err
		}
		start := normalizeTradingDay(time.Unix(startUnix, 0)).Format("20060102")
		end := normalizeTradingDay(time.Unix(endUnix, 0)).Format("20060102")
		if startDate == "" || start < startDate {
			startDate = start
		}
		if endDate == "" || end > endDate {
			endDate = end
		}
	}
	return startDate, endDate, nil
}
