package collector

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type KlineGapCleanupOptions struct {
	AssetType  AssetType
	Instrument string
	Period     KlinePeriod
	Limit      int
	DryRun     bool
}

type KlineGapCleanupAction struct {
	GapID          int64       `json:"gap_id"`
	AssetType      AssetType   `json:"asset_type"`
	Instrument     string      `json:"instrument"`
	Period         KlinePeriod `json:"period"`
	Action         string      `json:"action"`
	BeforeStatus   string      `json:"before_status"`
	BeforeStartKey string      `json:"before_start_key"`
	BeforeEndKey   string      `json:"before_end_key"`
	AfterStatus    string      `json:"after_status"`
	AfterStartKey  string      `json:"after_start_key,omitempty"`
	AfterEndKey    string      `json:"after_end_key,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

type KlineGapCleanupReport struct {
	DryRun      bool                    `json:"dry_run"`
	Scanned     int                     `json:"scanned"`
	Matched     int                     `json:"matched"`
	Closed      int                     `json:"closed"`
	Updated     int                     `json:"updated"`
	Unchanged   int                     `json:"unchanged"`
	StartedAt   time.Time               `json:"started_at"`
	CompletedAt time.Time               `json:"completed_at"`
	Touched     []string                `json:"touched_tables"`
	Actions     []KlineGapCleanupAction `json:"actions"`
}

func (s *KlineService) CleanupCollectGaps(ctx context.Context, opts KlineGapCleanupOptions) (*KlineGapCleanupReport, error) {
	report := &KlineGapCleanupReport{
		DryRun:    opts.DryRun,
		StartedAt: time.Now(),
		Touched:   []string{"collector_gap"},
		Actions:   make([]KlineGapCleanupAction, 0, 32),
	}
	defer func() {
		report.CompletedAt = time.Now()
	}()

	gaps, err := s.store.ListOpenCollectGaps("kline", "", "", "")
	if err != nil {
		return nil, err
	}
	lookup, err := s.cleanupTradingDayLookup(ctx, gaps, opts)
	if err != nil {
		return nil, err
	}

	for i := range gaps {
		gap := gaps[i]
		report.Scanned++
		if !klineGapMatchesCleanupFilter(gap, opts) {
			continue
		}
		if opts.Limit > 0 && report.Matched >= opts.Limit {
			break
		}
		report.Matched++

		action, changed, err := s.planCollectGapCleanup(ctx, lookup, &gap)
		if err != nil {
			return nil, err
		}
		if !changed {
			report.Unchanged++
			continue
		}

		report.Actions = append(report.Actions, action)
		switch action.Action {
		case "close":
			report.Closed++
		case "update":
			report.Updated++
		}

		if opts.DryRun {
			continue
		}
		if err := s.applyCollectGapCleanup(&gap, action); err != nil {
			return nil, err
		}
	}

	return report, nil
}

func (s *KlineService) cleanupTradingDayLookup(ctx context.Context, gaps []CollectGapRecord, opts KlineGapCleanupOptions) (tradingDayLookup, error) {
	lookup := s.provider.IsTradingDay

	var (
		minDay time.Time
		maxDay time.Time
		found  bool
	)
	matched := 0
	for i := range gaps {
		gap := gaps[i]
		if !klineGapMatchesCleanupFilter(gap, opts) {
			continue
		}
		if opts.Limit > 0 && matched >= opts.Limit {
			break
		}
		matched++

		startUnix, err := strconv.ParseInt(gap.StartKey, 10, 64)
		if err != nil {
			return nil, err
		}
		endUnix, err := strconv.ParseInt(gap.EndKey, 10, 64)
		if err != nil {
			return nil, err
		}

		startDay := normalizeTradingDay(time.Unix(startUnix, 0))
		endDay := normalizeTradingDay(time.Unix(endUnix, 0))
		if !found || startDay.Before(minDay) {
			minDay = startDay
		}
		if !found || endDay.After(maxDay) {
			maxDay = endDay
		}
		found = true
	}
	if !found {
		return lookup, nil
	}

	calendarStart := minDay.AddDate(-1, 0, 0)
	calendarEnd := maxDay.AddDate(1, 0, 0)
	items, err := s.provider.TradingDays(ctx, TradingDayQuery{
		Start: calendarStart,
		End:   calendarEnd,
	})
	if err != nil {
		return nil, err
	}
	tradingDays := make(map[string]struct{}, len(items))
	for _, item := range items {
		tradingDays[normalizeTradingDay(item.Time).Format("20060102")] = struct{}{}
	}

	return func(ctx context.Context, day time.Time) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		_, ok := tradingDays[normalizeTradingDay(day).Format("20060102")]
		return ok, nil
	}, nil
}

func klineGapMatchesCleanupFilter(gap CollectGapRecord, opts KlineGapCleanupOptions) bool {
	if opts.AssetType != "" && opts.AssetType != AssetTypeUnknown && gap.AssetType != string(opts.AssetType) {
		return false
	}
	if strings.TrimSpace(opts.Instrument) != "" && gap.Instrument != strings.TrimSpace(opts.Instrument) {
		return false
	}
	if opts.Period != "" && gap.Period != string(opts.Period) {
		return false
	}
	return true
}

func (s *KlineService) planCollectGapCleanup(ctx context.Context, lookup tradingDayLookup, gap *CollectGapRecord) (KlineGapCleanupAction, bool, error) {
	action := KlineGapCleanupAction{
		GapID:          gap.ID,
		AssetType:      AssetType(gap.AssetType),
		Instrument:     gap.Instrument,
		Period:         KlinePeriod(gap.Period),
		BeforeStatus:   gap.Status,
		BeforeStartKey: gap.StartKey,
		BeforeEndKey:   gap.EndKey,
		AfterStatus:    gap.Status,
	}

	startUnix, err := strconv.ParseInt(gap.StartKey, 10, 64)
	if err != nil {
		return action, false, err
	}
	endUnix, err := strconv.ParseInt(gap.EndKey, 10, 64)
	if err != nil {
		return action, false, err
	}

	start, end, ok, err := s.normalizeKlineGapWindowWithLookup(ctx, lookup, KlinePeriod(gap.Period), time.Unix(startUnix, 0), time.Unix(endUnix, 0))
	if err != nil {
		return action, false, err
	}
	if !ok {
		action.Action = "close"
		action.AfterStatus = "closed"
		action.Reason = appendGapReason(gap.Reason, "closed by kline gap cleanup")
		return action, true, nil
	}

	startKey := strconv.FormatInt(start.Unix(), 10)
	endKey := strconv.FormatInt(end.Unix(), 10)
	action.AfterStartKey = startKey
	action.AfterEndKey = endKey
	if gap.StartKey == startKey && gap.EndKey == endKey {
		return action, false, nil
	}

	action.Action = "update"
	action.Reason = appendGapReason(gap.Reason, "normalized by kline gap cleanup")
	return action, true, nil
}

func (s *KlineService) applyCollectGapCleanup(gap *CollectGapRecord, action KlineGapCleanupAction) error {
	switch action.Action {
	case "close":
		return s.store.CloseCollectGap(gap.ID, action.Reason)
	case "update":
		gap.StartKey = action.AfterStartKey
		gap.EndKey = action.AfterEndKey
		gap.Reason = action.Reason
		return s.store.UpdateCollectGap(gap)
	default:
		return nil
	}
}
