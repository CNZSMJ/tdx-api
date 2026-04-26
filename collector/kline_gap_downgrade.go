package collector

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type KlineGapDowngradeOptions struct {
	AssetType  AssetType
	Instrument string
	Period     KlinePeriod
	Limit      int
	Reason     string
	DryRun     bool
}

type KlineGapDowngradeEntry struct {
	GapID          int64       `json:"gap_id"`
	AssetType      AssetType   `json:"asset_type"`
	Instrument     string      `json:"instrument"`
	Period         KlinePeriod `json:"period"`
	StartKey       string      `json:"start_key"`
	EndKey         string      `json:"end_key"`
	StartDate      string      `json:"start_date"`
	EndDate        string      `json:"end_date"`
	TaskDates      []string    `json:"task_dates,omitempty"`
	BeforeStatus   string      `json:"before_status"`
	AfterStatus    string      `json:"after_status"`
	GovernanceTask string      `json:"governance_task"`
	Reason         string      `json:"reason"`
}

type KlineGapDowngradeReport struct {
	DryRun            bool                     `json:"dry_run"`
	Reason            string                   `json:"reason"`
	Scanned           int                      `json:"scanned"`
	Matched           int                      `json:"matched"`
	Downgraded        int                      `json:"downgraded"`
	RemainingOpenGaps int64                    `json:"remaining_open_gaps"`
	DegradedGapCount  int64                    `json:"degraded_gap_count"`
	StartedAt         time.Time                `json:"started_at"`
	CompletedAt       time.Time                `json:"completed_at"`
	ReportPath        string                   `json:"report_path,omitempty"`
	TaskListPath      string                   `json:"task_list_path,omitempty"`
	Touched           []string                 `json:"touched_tables"`
	Entries           []KlineGapDowngradeEntry `json:"entries"`
}

func (s *KlineService) DowngradeCollectGaps(ctx context.Context, opts KlineGapDowngradeOptions) (*KlineGapDowngradeReport, error) {
	report := &KlineGapDowngradeReport{
		DryRun:    opts.DryRun,
		Reason:    normalizeKlineGapDowngradeReason(opts.Reason),
		StartedAt: time.Now(),
		Touched:   []string{"collector_gap"},
		Entries:   make([]KlineGapDowngradeEntry, 0, 32),
	}
	defer func() {
		report.CompletedAt = time.Now()
	}()

	gaps, err := s.store.ListOpenCollectGaps("kline", "", "", "")
	if err != nil {
		return nil, err
	}
	report.Scanned = len(gaps)

	lookup, err := s.cleanupTradingDayLookup(ctx, gaps, KlineGapCleanupOptions{
		AssetType:  opts.AssetType,
		Instrument: opts.Instrument,
		Period:     opts.Period,
		Limit:      opts.Limit,
	})
	if err != nil {
		return nil, err
	}

	reconcileOpts := KlineGapReconcileOptions{
		AssetType:  opts.AssetType,
		Instrument: opts.Instrument,
		Period:     opts.Period,
	}
	for i := range gaps {
		gap := gaps[i]
		if !klineGapMatchesCleanupFilter(gap, KlineGapCleanupOptions{
			AssetType:  opts.AssetType,
			Instrument: opts.Instrument,
			Period:     opts.Period,
		}) {
			continue
		}
		if opts.Limit > 0 && report.Matched >= opts.Limit {
			break
		}

		taskDates, matched, err := s.planGapReconcileDates(ctx, lookup, gap, reconcileOpts, klineGapReconcileDateRange{})
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}

		report.Matched++
		entry := KlineGapDowngradeEntry{
			GapID:          gap.ID,
			AssetType:      AssetType(gap.AssetType),
			Instrument:     gap.Instrument,
			Period:         KlinePeriod(gap.Period),
			StartKey:       gap.StartKey,
			EndKey:         gap.EndKey,
			StartDate:      klineGapKeyToDate(gap.StartKey),
			EndDate:        klineGapKeyToDate(gap.EndKey),
			TaskDates:      taskDates,
			BeforeStatus:   gap.Status,
			AfterStatus:    CollectGapStatusDegraded,
			GovernanceTask: buildKlineGapGovernanceTask(gap, taskDates),
			Reason:         appendGapReason(gap.Reason, report.Reason),
		}
		report.Entries = append(report.Entries, entry)

		if opts.DryRun {
			continue
		}
		if err := s.store.DegradeCollectGap(gap.ID, entry.Reason); err != nil {
			return nil, err
		}
		report.Downgraded++
	}

	report.RemainingOpenGaps, err = s.store.CountOpenCollectGaps()
	if err != nil {
		return nil, err
	}
	report.DegradedGapCount, err = s.store.CountCollectGapsByStatus(CollectGapStatusDegraded)
	if err != nil {
		return nil, err
	}
	return report, nil
}

func normalizeKlineGapDowngradeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return reason
	}
	return "manually downgraded after provider replay and live-cache fallback both failed"
}

func klineGapKeyToDate(raw string) string {
	unix, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return ""
	}
	return normalizeTradingDay(time.Unix(unix, 0)).Format("20060102")
}

func buildKlineGapGovernanceTask(gap CollectGapRecord, taskDates []string) string {
	window := klineGapKeyToDate(gap.StartKey)
	end := klineGapKeyToDate(gap.EndKey)
	if end != "" && end != window {
		window += ".." + end
	}
	if len(taskDates) == 0 {
		return gap.Instrument + "/" + gap.Period + " window=" + window
	}
	return gap.Instrument + "/" + gap.Period + " dates=" + strings.Join(taskDates, ",")
}

func klineGapDowngradeTaskListPath(reportPath string) string {
	base := strings.TrimSuffix(reportPath, filepath.Ext(reportPath))
	return base + ".md"
}
