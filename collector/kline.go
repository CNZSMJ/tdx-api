package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"xorm.io/xorm"
)

type KlineConfig struct {
	BaseDir       string
	LiveBaseDir   string
	ReplayOverlap int
	Now           func() time.Time
}

type KlineService struct {
	store    *Store
	provider Provider
	cfg      KlineConfig
}

type KlineCollectQuery struct {
	Code      string
	AssetType AssetType
	Period    KlinePeriod
}

type KlinePublishRow struct {
	Code   string     `xorm:"varchar(16) index notnull"`
	Date   int64      `xorm:"index notnull"`
	Open   PriceMilli `xorm:"notnull"`
	High   PriceMilli `xorm:"notnull"`
	Low    PriceMilli `xorm:"notnull"`
	Close  PriceMilli `xorm:"notnull"`
	Volume int64      `xorm:"notnull"`
	Amount PriceMilli `xorm:"notnull"`
	InDate int64      `xorm:"created"`
}

func NewKlineService(store *Store, provider Provider, cfg KlineConfig) (*KlineService, error) {
	if store == nil {
		return nil, errors.New("kline service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("kline service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "kline")
	}
	if cfg.LiveBaseDir == "" {
		cfg.LiveBaseDir = filepath.Join(filepath.Dir(cfg.BaseDir), "live")
	}
	if cfg.ReplayOverlap <= 0 {
		cfg.ReplayOverlap = 1
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &KlineService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (s *KlineService) Refresh(ctx context.Context, query KlineCollectQuery) error {
	if query.Code == "" {
		return errors.New("kline refresh requires code")
	}
	if query.Period == "" {
		return errors.New("kline refresh requires period")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}

	cursor, err := s.store.GetCollectCursor("kline", string(query.AssetType), query.Code, string(query.Period))
	if err != nil {
		return err
	}

	since := time.Time{}
	if cursor != nil && cursor.Cursor != "" {
		unix, parseErr := strconv.ParseInt(cursor.Cursor, 10, 64)
		if parseErr != nil {
			return parseErr
		}
		since = time.Unix(unix, 0).Add(-periodOverlapDuration(query.Period, s.cfg.ReplayOverlap))
	}

	bars, err := s.provider.Klines(ctx, KlineQuery{
		Code:      query.Code,
		AssetType: query.AssetType,
		Period:    query.Period,
		Since:     since,
	})
	if err != nil {
		return err
	}
	staged, err := validateKlineBars(query, bars)
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		return nil
	}
	if cursor != nil && cursor.Cursor != "" {
		if err := s.recordKlineGap(ctx, query, cursor.Cursor, staged[0].Date); err != nil {
			return err
		}
	}

	if err := s.publishValidatedRows(ctx, query, staged); err != nil {
		return err
	}

	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("kline-%s-%s-%d", query.Code, query.Period, time.Now().UnixNano()),
		PhaseID:     "phase_2",
		SuiteName:   "kline_publish",
		Status:      "passed",
		Blocking:    true,
		CommandText: "kline publish transaction",
		OutputText:  fmt.Sprintf("code=%s period=%s rows=%d", query.Code, query.Period, len(staged)),
	})
}

func (s *KlineService) ReconcileDate(ctx context.Context, query KlineCollectQuery, date string) error {
	if query.Code == "" {
		return errors.New("kline reconcile requires code")
	}
	if query.Period == "" {
		return errors.New("kline reconcile requires period")
	}
	target, err := parseTradeCursor(date)
	if err != nil {
		return err
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}

	since := target.Add(-periodOverlapDuration(query.Period, s.cfg.ReplayOverlap))
	bars, err := s.provider.Klines(ctx, KlineQuery{
		Code:      query.Code,
		AssetType: query.AssetType,
		Period:    query.Period,
		Since:     since,
	})
	if err != nil {
		return err
	}
	staged, err := validateKlineBars(query, bars)
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		return nil
	}

	if err := s.publishValidatedRows(ctx, query, staged); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("kline-reconcile-%s-%s-%s-%d", query.Code, query.Period, date, time.Now().UnixNano()),
		PhaseID:     "phase_7",
		SuiteName:   "kline_reconcile",
		Status:      "passed",
		Blocking:    true,
		CommandText: "kline reconcile publish transaction",
		OutputText:  fmt.Sprintf("code=%s period=%s date=%s rows=%d", query.Code, query.Period, date, len(staged)),
	})
}

func (s *KlineService) publishValidatedRows(ctx context.Context, query KlineCollectQuery, staged []*KlinePublishRow) error {
	if len(staged) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.cfg.BaseDir, 0o777); err != nil {
		return err
	}
	engine, err := openMetadataEngine(filepath.Join(s.cfg.BaseDir, query.Code+".db"))
	if err != nil {
		return err
	}
	defer engine.Close()

	spec, err := klinePeriodSpec(query.Period)
	if err != nil {
		return err
	}
	if err := engine.Table(spec.PublishedTable).Sync2(new(KlinePublishRow)); err != nil {
		return err
	}
	if err := engine.Table(spec.StagingTable).Sync2(new(KlinePublishRow)); err != nil {
		return err
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM " + spec.StagingTable); err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(staged))
		for _, bar := range staged {
			rows = append(rows, bar)
		}
		if _, err := session.Table(spec.StagingTable).Insert(rows...); err != nil {
			return nil, err
		}
		count, err := session.Table(spec.StagingTable).Count(new(KlinePublishRow))
		if err != nil {
			return nil, err
		}
		if count != int64(len(staged)) {
			return nil, fmt.Errorf("kline publish staging count mismatch: got %d want %d", count, len(staged))
		}
		if _, err := session.Table(spec.PublishedTable).Where("Date >= ?", staged[0].Date).Delete(new(KlinePublishRow)); err != nil {
			return nil, err
		}
		if _, err := session.Table(spec.PublishedTable).Insert(rows...); err != nil {
			return nil, err
		}
		if _, err := session.Exec("DELETE FROM " + spec.StagingTable); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}

	last := staged[len(staged)-1]
	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "kline",
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Period:     string(query.Period),
		Cursor:     strconv.FormatInt(last.Date, 10),
	}); err != nil {
		return err
	}
	return s.reconcileCollectGaps(ctx, query, staged[0].Date, last.Date)
}

func (s *KlineService) ReconcileDateFromLiveCache(ctx context.Context, query KlineCollectQuery, date string) (bool, error) {
	target, err := parseTradeCursor(date)
	if err != nil {
		return false, err
	}
	fallbackBars, err := s.liveFallbackBars(query, date)
	if err != nil {
		return false, err
	}
	if len(fallbackBars) == 0 {
		return false, nil
	}

	keepAfter, err := s.loadPublishedKlineBarsAfter(query, dayAt(target, 15, 0))
	if err != nil {
		return false, err
	}
	merged, err := validateKlineBars(query, append(fallbackBars, keepAfter...))
	if err != nil {
		return false, err
	}
	if len(merged) == 0 {
		return false, nil
	}
	if err := s.publishValidatedRows(ctx, query, merged); err != nil {
		return false, err
	}
	if err := s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("kline-live-cache-%s-%s-%s-%d", query.Code, query.Period, date, time.Now().UnixNano()),
		PhaseID:     "phase_7",
		SuiteName:   "kline_live_cache_repair",
		Status:      "passed",
		Blocking:    true,
		CommandText: "kline live cache repair publish transaction",
		OutputText:  fmt.Sprintf("code=%s period=%s date=%s rows=%d", query.Code, query.Period, date, len(merged)),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *KlineService) liveFallbackBars(query KlineCollectQuery, date string) ([]KlineBar, error) {
	if query.Period == "" {
		return nil, nil
	}
	switch query.Period {
	case PeriodMinute, Period5Minute, Period15Minute, Period30Minute, Period60Minute:
	default:
		return nil, nil
	}

	filename := filepath.Join(s.cfg.LiveBaseDir, query.Code+".db")
	info, err := os.Stat(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}

	engine, err := openMetadataEngine(filename)
	if err != nil {
		return nil, err
	}
	defer engine.Close()

	rows := make([]MinuteLiveRow, 0, 256)
	if err := engine.Table("MinuteLive").Where("TradeDate = ?", date).Asc("Clock").Find(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return buildKlineBarsFromMinuteLiveRows(query, date, rows)
}

func (s *KlineService) loadPublishedKlineBarsAfter(query KlineCollectQuery, after time.Time) ([]KlineBar, error) {
	filename := filepath.Join(s.cfg.BaseDir, query.Code+".db")
	info, err := os.Stat(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}

	engine, err := openMetadataEngine(filename)
	if err != nil {
		return nil, err
	}
	defer engine.Close()

	spec, err := klinePeriodSpec(query.Period)
	if err != nil {
		return nil, err
	}
	rows := make([]KlinePublishRow, 0, 256)
	if err := engine.Table(spec.PublishedTable).Where("Date > ?", after.Unix()).Asc("Date").Find(&rows); err != nil {
		return nil, err
	}
	bars := make([]KlineBar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, KlineBar{
			Code:       query.Code,
			AssetType:  query.AssetType,
			Period:     query.Period,
			Time:       time.Unix(row.Date, 0),
			Open:       row.Open,
			High:       row.High,
			Low:        row.Low,
			Close:      row.Close,
			VolumeHand: row.Volume,
			Amount:     row.Amount,
		})
	}
	return bars, nil
}

func buildKlineBarsFromMinuteLiveRows(query KlineCollectQuery, date string, rows []MinuteLiveRow) ([]KlineBar, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	day, err := parseTradeCursor(date)
	if err != nil {
		return nil, err
	}

	out := make([]*KlineBar, 0, len(rows))
	index := make(map[int64]*KlineBar)
	for _, row := range rows {
		clockTime, err := time.ParseInLocation("15:04", strings.TrimSpace(row.Clock), time.Local)
		if err != nil {
			return nil, err
		}
		pointTime := dayAt(day, clockTime.Hour(), clockTime.Minute())
		bucketTime, ok := klineBucketTimeFromMinutePoint(query.Period, pointTime)
		if !ok {
			continue
		}
		key := bucketTime.Unix()
		bar, exists := index[key]
		if !exists {
			bar = &KlineBar{
				Code:      query.Code,
				AssetType: query.AssetType,
				Period:    query.Period,
				Time:      bucketTime,
				Open:      row.Price,
				High:      row.Price,
				Low:       row.Price,
				Close:     row.Price,
			}
			index[key] = bar
			out = append(out, bar)
			continue
		}
		if row.Price > bar.High {
			bar.High = row.Price
		}
		if row.Price < bar.Low {
			bar.Low = row.Price
		}
		bar.Close = row.Price
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	bars := make([]KlineBar, 0, len(out))
	for _, bar := range out {
		bars = append(bars, *bar)
	}
	return bars, nil
}

func klineBucketTimeFromMinutePoint(period KlinePeriod, point time.Time) (time.Time, bool) {
	switch period {
	case PeriodMinute:
		return point, true
	case Period5Minute, Period15Minute, Period30Minute, Period60Minute:
		clocks, ok := intradayPeriodClocks(period)
		if !ok {
			return time.Time{}, false
		}
		day := normalizeTradingDay(point)
		for _, clock := range clocks {
			candidate := dayAt(day, clock.hour, clock.minute)
			if !candidate.Before(point) {
				return candidate, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

func (s *KlineService) recordKlineGap(ctx context.Context, query KlineCollectQuery, cursorValue string, firstPublished int64) error {
	cursorUnix, err := strconv.ParseInt(cursorValue, 10, 64)
	if err != nil {
		return err
	}
	cursorTime := time.Unix(cursorUnix, 0)
	firstTime := time.Unix(firstPublished, 0)
	start, end, ok, err := s.klineGapWindow(ctx, query.Period, cursorTime, firstTime)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Period:     string(query.Period),
		StartKey:   strconv.FormatInt(start.Unix(), 10),
		EndKey:     strconv.FormatInt(end.Unix(), 10),
		Status:     "open",
		Reason:     "detected during kline replay",
	})
}

func (s *KlineService) reconcileCollectGaps(ctx context.Context, query KlineCollectQuery, publishedStart, publishedEnd int64) error {
	gaps, err := s.store.ListOpenCollectGaps("kline", string(query.AssetType), query.Code, string(query.Period))
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		return nil
	}

	startTime := time.Unix(publishedStart, 0)
	endTime := time.Unix(publishedEnd, 0)
	for i := range gaps {
		if err := s.reconcileCollectGap(ctx, query, &gaps[i], startTime, endTime); err != nil {
			return err
		}
	}
	return nil
}

func (s *KlineService) reconcileCollectGap(ctx context.Context, query KlineCollectQuery, gap *CollectGapRecord, publishedStart, publishedEnd time.Time) error {
	if gap == nil {
		return nil
	}
	startUnix, err := strconv.ParseInt(gap.StartKey, 10, 64)
	if err != nil {
		return err
	}
	endUnix, err := strconv.ParseInt(gap.EndKey, 10, 64)
	if err != nil {
		return err
	}

	start, end, ok, err := s.normalizeKlineGapWindow(ctx, query.Period, time.Unix(startUnix, 0), time.Unix(endUnix, 0))
	if err != nil {
		return err
	}
	if !ok {
		return s.store.CloseCollectGap(gap.ID, appendGapReason(gap.Reason, "closed after kline gap normalization"))
	}

	startKey := strconv.FormatInt(start.Unix(), 10)
	endKey := strconv.FormatInt(end.Unix(), 10)
	if gap.StartKey != startKey || gap.EndKey != endKey {
		gap.StartKey = startKey
		gap.EndKey = endKey
		if err := s.store.UpdateCollectGap(gap); err != nil {
			return err
		}
	}

	if publishedEnd.Before(start) || publishedStart.After(end) {
		return nil
	}
	if !publishedStart.After(start) && !publishedEnd.Before(end) {
		return s.store.CloseCollectGap(gap.ID, appendGapReason(gap.Reason, "covered by kline publish"))
	}

	leftEnd, hasLeft, err := s.previousExpectedKlineTime(ctx, query.Period, publishedStart)
	if err != nil {
		return err
	}
	if hasLeft && leftEnd.Before(start) {
		hasLeft = false
	}

	rightStart, hasRight, err := s.nextExpectedKlineTime(ctx, query.Period, publishedEnd)
	if err != nil {
		return err
	}
	if hasRight && rightStart.After(end) {
		hasRight = false
	}

	switch {
	case hasLeft && hasRight:
		gap.EndKey = strconv.FormatInt(leftEnd.Unix(), 10)
		if err := s.store.UpdateCollectGap(gap); err != nil {
			return err
		}
		return s.store.UpsertCollectGap(&CollectGapRecord{
			Domain:     gap.Domain,
			AssetType:  gap.AssetType,
			Instrument: gap.Instrument,
			Period:     gap.Period,
			StartKey:   strconv.FormatInt(rightStart.Unix(), 10),
			EndKey:     strconv.FormatInt(end.Unix(), 10),
			Status:     "open",
			Reason:     appendGapReason(gap.Reason, "split after kline publish"),
		})
	case hasLeft:
		gap.EndKey = strconv.FormatInt(leftEnd.Unix(), 10)
		return s.store.UpdateCollectGap(gap)
	case hasRight:
		gap.StartKey = strconv.FormatInt(rightStart.Unix(), 10)
		return s.store.UpdateCollectGap(gap)
	default:
		return s.store.CloseCollectGap(gap.ID, appendGapReason(gap.Reason, "covered by kline publish"))
	}
}

func appendGapReason(existing, extra string) string {
	existing = strings.TrimSpace(existing)
	extra = strings.TrimSpace(extra)
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	if strings.Contains(existing, extra) {
		return existing
	}
	return existing + " | " + extra
}

func validateKlineBars(query KlineCollectQuery, bars []KlineBar) ([]*KlinePublishRow, error) {
	if len(bars) == 0 {
		return nil, nil
	}
	sort.Slice(bars, func(i, j int) bool {
		return bars[i].Time.Before(bars[j].Time)
	})

	seen := make(map[int64]struct{}, len(bars))
	rows := make([]*KlinePublishRow, 0, len(bars))
	var last int64
	for _, bar := range bars {
		unix := bar.Time.Unix()
		if _, ok := seen[unix]; ok {
			return nil, fmt.Errorf("duplicate kline timestamp for %s %s: %d", query.Code, query.Period, unix)
		}
		if last != 0 && unix < last {
			return nil, fmt.Errorf("non-monotonic kline timestamp for %s %s", query.Code, query.Period)
		}
		last = unix
		seen[unix] = struct{}{}
		rows = append(rows, &KlinePublishRow{
			Code:   query.Code,
			Date:   unix,
			Open:   bar.Open,
			High:   bar.High,
			Low:    bar.Low,
			Close:  bar.Close,
			Volume: bar.VolumeHand,
			Amount: bar.Amount,
		})
	}
	return rows, nil
}

type klineTableSpec struct {
	PublishedTable string
	StagingTable   string
}

func klinePeriodSpec(period KlinePeriod) (*klineTableSpec, error) {
	table := ""
	switch period {
	case PeriodMinute:
		table = "MinuteKline"
	case Period5Minute:
		table = "Minute5Kline"
	case Period15Minute:
		table = "Minute15Kline"
	case Period30Minute:
		table = "Minute30Kline"
	case Period60Minute:
		table = "HourKline"
	case PeriodDay:
		table = "DayKline"
	case PeriodWeek:
		table = "WeekKline"
	case PeriodMonth:
		table = "MonthKline"
	case PeriodQuarter:
		table = "QuarterKline"
	case PeriodYear:
		table = "YearKline"
	default:
		return nil, fmt.Errorf("unsupported kline period: %s", period)
	}
	return &klineTableSpec{
		PublishedTable: table,
		StagingTable:   "collector_" + table + "_staging",
	}, nil
}

func periodOverlapDuration(period KlinePeriod, overlap int) time.Duration {
	if overlap <= 0 {
		overlap = 1
	}
	switch period {
	case PeriodMinute:
		return time.Minute * time.Duration(overlap)
	case Period5Minute:
		return 5 * time.Minute * time.Duration(overlap)
	case Period15Minute:
		return 15 * time.Minute * time.Duration(overlap)
	case Period30Minute:
		return 30 * time.Minute * time.Duration(overlap)
	case Period60Minute:
		return time.Hour * time.Duration(overlap)
	case PeriodDay:
		return 24 * time.Hour * time.Duration(overlap)
	case PeriodWeek:
		return 7 * 24 * time.Hour * time.Duration(overlap)
	case PeriodMonth:
		return 31 * 24 * time.Hour * time.Duration(overlap)
	case PeriodQuarter:
		return 93 * 24 * time.Hour * time.Duration(overlap)
	case PeriodYear:
		return 366 * 24 * time.Hour * time.Duration(overlap)
	default:
		return 24 * time.Hour * time.Duration(overlap)
	}
}

type klineClock struct {
	hour   int
	minute int
}

type tradingDayLookup func(context.Context, time.Time) (bool, error)

func intradayPeriodClocks(period KlinePeriod) ([]klineClock, bool) {
	switch period {
	case PeriodMinute:
		return buildIntradayClocks(9, 31, 11, 30, 1, 13, 1, 15, 0, 1), true
	case Period5Minute:
		return buildIntradayClocks(9, 35, 11, 30, 5, 13, 5, 15, 0, 5), true
	case Period15Minute:
		return buildIntradayClocks(9, 45, 11, 30, 15, 13, 15, 15, 0, 15), true
	case Period30Minute:
		return buildIntradayClocks(10, 0, 11, 30, 30, 13, 30, 15, 0, 30), true
	case Period60Minute:
		return []klineClock{
			{hour: 10, minute: 30},
			{hour: 13, minute: 0},
			{hour: 14, minute: 0},
			{hour: 15, minute: 0},
		}, true
	default:
		return nil, false
	}
}

func buildIntradayClocks(morningHour, morningMinute, morningEndHour, morningEndMinute, morningStep, afternoonHour, afternoonMinute, afternoonEndHour, afternoonEndMinute, afternoonStep int) []klineClock {
	clocks := make([]klineClock, 0, 64)
	for hour, minute := morningHour, morningMinute; hour < morningEndHour || (hour == morningEndHour && minute <= morningEndMinute); minute += morningStep {
		for minute >= 60 {
			hour++
			minute -= 60
		}
		clocks = append(clocks, klineClock{hour: hour, minute: minute})
	}
	for hour, minute := afternoonHour, afternoonMinute; hour < afternoonEndHour || (hour == afternoonEndHour && minute <= afternoonEndMinute); minute += afternoonStep {
		for minute >= 60 {
			hour++
			minute -= 60
		}
		clocks = append(clocks, klineClock{hour: hour, minute: minute})
	}
	return clocks
}

func (s *KlineService) klineGapWindow(ctx context.Context, period KlinePeriod, after, before time.Time) (time.Time, time.Time, bool, error) {
	return s.klineGapWindowWithLookup(ctx, s.provider.IsTradingDay, period, after, before)
}

func (s *KlineService) klineGapWindowWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, after, before time.Time) (time.Time, time.Time, bool, error) {
	nextExpected, ok, err := s.nextExpectedKlineTimeWithLookup(ctx, lookup, period, after)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !ok || !before.After(nextExpected) {
		return time.Time{}, time.Time{}, false, nil
	}
	lastMissing, ok, err := s.previousExpectedKlineTimeWithLookup(ctx, lookup, period, before)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !ok || lastMissing.Before(nextExpected) {
		return time.Time{}, time.Time{}, false, nil
	}
	return nextExpected, lastMissing, true, nil
}

func (s *KlineService) normalizeKlineGapWindow(ctx context.Context, period KlinePeriod, start, end time.Time) (time.Time, time.Time, bool, error) {
	return s.normalizeKlineGapWindowWithLookup(ctx, s.provider.IsTradingDay, period, start, end)
}

func (s *KlineService) normalizeKlineGapWindowWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, start, end time.Time) (time.Time, time.Time, bool, error) {
	if end.Before(start) {
		return time.Time{}, time.Time{}, false, nil
	}
	firstValid, ok, err := s.firstValidKlineTimeOnOrAfterWithLookup(ctx, lookup, period, start)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !ok || firstValid.After(end) {
		return time.Time{}, time.Time{}, false, nil
	}
	lastValid, ok, err := s.lastValidKlineTimeOnOrBeforeWithLookup(ctx, lookup, period, end)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !ok || lastValid.Before(firstValid) {
		return time.Time{}, time.Time{}, false, nil
	}
	return firstValid, lastValid, true, nil
}

func (s *KlineService) nextExpectedKlineTime(ctx context.Context, period KlinePeriod, after time.Time) (time.Time, bool, error) {
	return s.nextExpectedKlineTimeWithLookup(ctx, s.provider.IsTradingDay, period, after)
}

func (s *KlineService) nextExpectedKlineTimeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, after time.Time) (time.Time, bool, error) {
	switch period {
	case PeriodMinute, Period5Minute, Period15Minute, Period30Minute, Period60Minute:
		return s.firstValidKlineTimeOnOrAfterWithLookup(ctx, lookup, period, after.Add(time.Second))
	case PeriodDay:
		day, ok, err := s.nextTradingDayWithLookup(ctx, lookup, normalizeTradingDay(after))
		if err != nil || !ok {
			return time.Time{}, ok, err
		}
		return dayAt(day, 15, 0), true, nil
	case PeriodWeek, PeriodMonth, PeriodQuarter, PeriodYear:
		return s.nextAggregateKlineTimeWithLookup(ctx, lookup, period, after)
	default:
		return time.Time{}, false, fmt.Errorf("unsupported kline period: %s", period)
	}
}

func (s *KlineService) previousExpectedKlineTime(ctx context.Context, period KlinePeriod, before time.Time) (time.Time, bool, error) {
	return s.previousExpectedKlineTimeWithLookup(ctx, s.provider.IsTradingDay, period, before)
}

func (s *KlineService) previousExpectedKlineTimeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, before time.Time) (time.Time, bool, error) {
	switch period {
	case PeriodMinute, Period5Minute, Period15Minute, Period30Minute, Period60Minute:
		return s.lastValidKlineTimeOnOrBeforeWithLookup(ctx, lookup, period, before.Add(-time.Second))
	case PeriodDay:
		day, ok, err := s.previousTradingDayWithLookup(ctx, lookup, normalizeTradingDay(before))
		if err != nil || !ok {
			return time.Time{}, ok, err
		}
		return dayAt(day, 15, 0), true, nil
	case PeriodWeek, PeriodMonth, PeriodQuarter, PeriodYear:
		return s.previousAggregateKlineTimeWithLookup(ctx, lookup, period, before)
	default:
		return time.Time{}, false, fmt.Errorf("unsupported kline period: %s", period)
	}
}

func (s *KlineService) firstValidKlineTimeOnOrAfter(ctx context.Context, period KlinePeriod, at time.Time) (time.Time, bool, error) {
	return s.firstValidKlineTimeOnOrAfterWithLookup(ctx, s.provider.IsTradingDay, period, at)
}

func (s *KlineService) firstValidKlineTimeOnOrAfterWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, at time.Time) (time.Time, bool, error) {
	if clocks, ok := intradayPeriodClocks(period); ok {
		day := normalizeTradingDay(at)
		for attempt := 0; attempt < 4096; attempt++ {
			isTradingDay, err := lookup(ctx, day)
			if err != nil {
				return time.Time{}, false, err
			}
			if isTradingDay {
				for _, clock := range clocks {
					candidate := dayAt(day, clock.hour, clock.minute)
					if !candidate.Before(at) {
						return candidate, true, nil
					}
				}
			}
			day = day.AddDate(0, 0, 1)
		}
		return time.Time{}, false, nil
	}

	switch period {
	case PeriodDay:
		day := normalizeTradingDay(at)
		for attempt := 0; attempt < 4096; attempt++ {
			isTradingDay, err := lookup(ctx, day)
			if err != nil {
				return time.Time{}, false, err
			}
			if isTradingDay {
				candidate := dayAt(day, 15, 0)
				if !candidate.Before(at) {
					return candidate, true, nil
				}
			}
			day = day.AddDate(0, 0, 1)
		}
		return time.Time{}, false, nil
	case PeriodWeek, PeriodMonth, PeriodQuarter, PeriodYear:
		current, ok, err := s.aggregatePeriodEndTimeWithLookup(ctx, lookup, period, normalizeTradingDay(at))
		if err != nil {
			return time.Time{}, false, err
		}
		if ok && !current.Before(at) {
			return current, true, nil
		}
		return s.nextAggregateKlineTimeWithLookup(ctx, lookup, period, at)
	default:
		return time.Time{}, false, fmt.Errorf("unsupported kline period: %s", period)
	}
}

func (s *KlineService) lastValidKlineTimeOnOrBefore(ctx context.Context, period KlinePeriod, at time.Time) (time.Time, bool, error) {
	return s.lastValidKlineTimeOnOrBeforeWithLookup(ctx, s.provider.IsTradingDay, period, at)
}

func (s *KlineService) lastValidKlineTimeOnOrBeforeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, at time.Time) (time.Time, bool, error) {
	if clocks, ok := intradayPeriodClocks(period); ok {
		day := normalizeTradingDay(at)
		for attempt := 0; attempt < 4096; attempt++ {
			isTradingDay, err := lookup(ctx, day)
			if err != nil {
				return time.Time{}, false, err
			}
			if isTradingDay {
				for i := len(clocks) - 1; i >= 0; i-- {
					candidate := dayAt(day, clocks[i].hour, clocks[i].minute)
					if !candidate.After(at) {
						return candidate, true, nil
					}
				}
			}
			day = day.AddDate(0, 0, -1)
		}
		return time.Time{}, false, nil
	}

	switch period {
	case PeriodDay:
		day := normalizeTradingDay(at)
		for attempt := 0; attempt < 4096; attempt++ {
			isTradingDay, err := lookup(ctx, day)
			if err != nil {
				return time.Time{}, false, err
			}
			if isTradingDay {
				candidate := dayAt(day, 15, 0)
				if !candidate.After(at) {
					return candidate, true, nil
				}
			}
			day = day.AddDate(0, 0, -1)
		}
		return time.Time{}, false, nil
	case PeriodWeek, PeriodMonth, PeriodQuarter, PeriodYear:
		current, ok, err := s.aggregatePeriodEndTimeWithLookup(ctx, lookup, period, normalizeTradingDay(at))
		if err != nil {
			return time.Time{}, false, err
		}
		if ok && !current.After(at) {
			return current, true, nil
		}
		return s.previousAggregateKlineTimeWithLookup(ctx, lookup, period, at)
	default:
		return time.Time{}, false, fmt.Errorf("unsupported kline period: %s", period)
	}
}

func (s *KlineService) nextAggregateKlineTime(ctx context.Context, period KlinePeriod, after time.Time) (time.Time, bool, error) {
	return s.nextAggregateKlineTimeWithLookup(ctx, s.provider.IsTradingDay, period, after)
}

func (s *KlineService) nextAggregateKlineTimeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, after time.Time) (time.Time, bool, error) {
	currentBucket := klineAggregateBucket(period, normalizeTradingDay(after))
	day := normalizeTradingDay(after).AddDate(0, 0, 1)
	for attempt := 0; attempt < 4096; attempt++ {
		isTradingDay, err := lookup(ctx, day)
		if err != nil {
			return time.Time{}, false, err
		}
		if isTradingDay && klineAggregateBucket(period, day) != currentBucket {
			return s.aggregatePeriodEndTimeWithLookup(ctx, lookup, period, day)
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false, nil
}

func (s *KlineService) previousAggregateKlineTime(ctx context.Context, period KlinePeriod, before time.Time) (time.Time, bool, error) {
	return s.previousAggregateKlineTimeWithLookup(ctx, s.provider.IsTradingDay, period, before)
}

func (s *KlineService) previousAggregateKlineTimeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, before time.Time) (time.Time, bool, error) {
	currentBucket := klineAggregateBucket(period, normalizeTradingDay(before))
	day := normalizeTradingDay(before).AddDate(0, 0, -1)
	for attempt := 0; attempt < 4096; attempt++ {
		isTradingDay, err := lookup(ctx, day)
		if err != nil {
			return time.Time{}, false, err
		}
		if isTradingDay && klineAggregateBucket(period, day) != currentBucket {
			return s.aggregatePeriodEndTimeWithLookup(ctx, lookup, period, day)
		}
		day = day.AddDate(0, 0, -1)
	}
	return time.Time{}, false, nil
}

func (s *KlineService) aggregatePeriodEndTime(ctx context.Context, period KlinePeriod, within time.Time) (time.Time, bool, error) {
	return s.aggregatePeriodEndTimeWithLookup(ctx, s.provider.IsTradingDay, period, within)
}

func (s *KlineService) aggregatePeriodEndTimeWithLookup(ctx context.Context, lookup tradingDayLookup, period KlinePeriod, within time.Time) (time.Time, bool, error) {
	targetBucket := klineAggregateBucket(period, normalizeTradingDay(within))
	day := normalizeTradingDay(within)
	lastTrading := time.Time{}
	for attempt := 0; attempt < 4096; attempt++ {
		if klineAggregateBucket(period, day) != targetBucket {
			break
		}
		isTradingDay, err := lookup(ctx, day)
		if err != nil {
			return time.Time{}, false, err
		}
		if isTradingDay {
			lastTrading = day
		}
		day = day.AddDate(0, 0, 1)
	}
	if lastTrading.IsZero() {
		return time.Time{}, false, nil
	}
	return dayAt(lastTrading, 15, 0), true, nil
}

func (s *KlineService) nextTradingDay(ctx context.Context, after time.Time) (time.Time, bool, error) {
	return s.nextTradingDayWithLookup(ctx, s.provider.IsTradingDay, after)
}

func (s *KlineService) nextTradingDayWithLookup(ctx context.Context, lookup tradingDayLookup, after time.Time) (time.Time, bool, error) {
	day := normalizeTradingDay(after).AddDate(0, 0, 1)
	for attempt := 0; attempt < 4096; attempt++ {
		isTradingDay, err := lookup(ctx, day)
		if err != nil {
			return time.Time{}, false, err
		}
		if isTradingDay {
			return day, true, nil
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false, nil
}

func (s *KlineService) previousTradingDay(ctx context.Context, before time.Time) (time.Time, bool, error) {
	return s.previousTradingDayWithLookup(ctx, s.provider.IsTradingDay, before)
}

func (s *KlineService) previousTradingDayWithLookup(ctx context.Context, lookup tradingDayLookup, before time.Time) (time.Time, bool, error) {
	day := normalizeTradingDay(before).AddDate(0, 0, -1)
	for attempt := 0; attempt < 4096; attempt++ {
		isTradingDay, err := lookup(ctx, day)
		if err != nil {
			return time.Time{}, false, err
		}
		if isTradingDay {
			return day, true, nil
		}
		day = day.AddDate(0, 0, -1)
	}
	return time.Time{}, false, nil
}

func normalizeTradingDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func dayAt(day time.Time, hour, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location())
}

func klineAggregateBucket(period KlinePeriod, day time.Time) string {
	switch period {
	case PeriodWeek:
		year, week := day.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	case PeriodMonth:
		return fmt.Sprintf("%04d-%02d", day.Year(), int(day.Month()))
	case PeriodQuarter:
		quarter := (int(day.Month())-1)/3 + 1
		return fmt.Sprintf("%04d-Q%d", day.Year(), quarter)
	case PeriodYear:
		return fmt.Sprintf("%04d", day.Year())
	default:
		return day.Format("20060102")
	}
}
