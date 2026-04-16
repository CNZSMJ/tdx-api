package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"xorm.io/xorm"
)

type KlineConfig struct {
	BaseDir       string
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
		if err := s.recordKlineGap(query, cursor.Cursor, staged[0].Date); err != nil {
			return err
		}
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
			return nil, fmt.Errorf("kline staging count mismatch: got %d want %d", count, len(staged))
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
			return nil, fmt.Errorf("kline reconcile staging count mismatch: got %d want %d", count, len(staged))
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

func (s *KlineService) recordKlineGap(query KlineCollectQuery, cursorValue string, firstPublished int64) error {
	cursorUnix, err := strconv.ParseInt(cursorValue, 10, 64)
	if err != nil {
		return err
	}
	cursorTime := time.Unix(cursorUnix, 0)
	nextExpected := shiftPeriodTime(cursorTime, query.Period, 1)
	firstTime := time.Unix(firstPublished, 0)
	if !firstTime.After(nextExpected) {
		return nil
	}
	lastMissing := shiftPeriodTime(firstTime, query.Period, -1)
	return s.store.UpsertCollectGap(&CollectGapRecord{
		Domain:     "kline",
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Period:     string(query.Period),
		StartKey:   strconv.FormatInt(nextExpected.Unix(), 10),
		EndKey:     strconv.FormatInt(lastMissing.Unix(), 10),
		Status:     "open",
		Reason:     "detected during kline replay",
	})
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

func shiftPeriodTime(base time.Time, period KlinePeriod, step int) time.Time {
	switch period {
	case PeriodMinute:
		return base.Add(time.Minute * time.Duration(step))
	case Period5Minute:
		return base.Add(5 * time.Minute * time.Duration(step))
	case Period15Minute:
		return base.Add(15 * time.Minute * time.Duration(step))
	case Period30Minute:
		return base.Add(30 * time.Minute * time.Duration(step))
	case Period60Minute:
		return base.Add(time.Hour * time.Duration(step))
	case PeriodDay:
		return base.AddDate(0, 0, step)
	case PeriodWeek:
		return base.AddDate(0, 0, 7*step)
	case PeriodMonth:
		return base.AddDate(0, step, 0)
	case PeriodQuarter:
		return base.AddDate(0, 3*step, 0)
	case PeriodYear:
		return base.AddDate(step, 0, 0)
	default:
		return base.AddDate(0, 0, step)
	}
}
