package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"xorm.io/xorm"
)

type TradeConfig struct {
	BaseDir string
	Now     func() time.Time
}

type TradeService struct {
	store    *Store
	provider Provider
	cfg      TradeConfig
}

type TradeCollectQuery struct {
	Code      string
	AssetType AssetType
	Date      string
}

type TradeHistoryRow struct {
	Code       string     `xorm:"varchar(16) index notnull"`
	TradeDate  string     `xorm:"varchar(16) index notnull"`
	TradeTime  int64      `xorm:"index notnull"`
	Seq        int        `xorm:"index notnull"`
	Price      PriceMilli `xorm:"notnull"`
	VolumeHand int        `xorm:"notnull"`
	Number     int        `xorm:"notnull"`
	StatusCode int        `xorm:"notnull"`
	Side       string     `xorm:"varchar(16)"`
	InDate     int64      `xorm:"created"`
}

type TradeBarRow struct {
	Code       string     `xorm:"varchar(16) index notnull"`
	TradeDate  string     `xorm:"varchar(16) index notnull"`
	BucketTime int64      `xorm:"index notnull"`
	Open       PriceMilli `xorm:"notnull"`
	High       PriceMilli `xorm:"notnull"`
	Low        PriceMilli `xorm:"notnull"`
	Close      PriceMilli `xorm:"notnull"`
	VolumeHand int64      `xorm:"notnull"`
	Amount     PriceMilli `xorm:"notnull"`
	InDate     int64      `xorm:"created"`
}

func NewTradeService(store *Store, provider Provider, cfg TradeConfig) (*TradeService, error) {
	if store == nil {
		return nil, errors.New("trade service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("trade service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "trade")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &TradeService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (s *TradeService) RefreshDay(ctx context.Context, query TradeCollectQuery) error {
	if query.Code == "" || query.Date == "" {
		return errors.New("trade refresh requires code and date")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}

	items, err := s.provider.TradeHistory(ctx, TradeHistoryQuery{
		Code: query.Code,
		Date: query.Date,
	})
	if err != nil {
		return err
	}
	staged, err := validateTradeRows(query, items)
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		return nil
	}

	derived := deriveTradeBars(staged)

	if err := os.MkdirAll(s.cfg.BaseDir, 0o777); err != nil {
		return err
	}
	engine, err := openMetadataEngine(filepath.Join(s.cfg.BaseDir, query.Code+".db"))
	if err != nil {
		return err
	}
	defer engine.Close()

	rawPublished := "TradeHistory"
	rawStaging := "collector_trade_history_staging"
	derivedSpecs := []struct {
		table string
		rows  []*TradeBarRow
	}{
		{table: "TradeMinute1Bar", rows: derived[1]},
		{table: "TradeMinute5Bar", rows: derived[5]},
		{table: "TradeMinute15Bar", rows: derived[15]},
		{table: "TradeMinute30Bar", rows: derived[30]},
		{table: "TradeMinute60Bar", rows: derived[60]},
	}

	if err := engine.Table(rawPublished).Sync2(new(TradeHistoryRow)); err != nil {
		return err
	}
	if err := engine.Table(rawStaging).Sync2(new(TradeHistoryRow)); err != nil {
		return err
	}
	for _, spec := range derivedSpecs {
		if err := engine.Table(spec.table).Sync2(new(TradeBarRow)); err != nil {
			return err
		}
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM " + rawStaging); err != nil {
			return nil, err
		}
		rawRows := make([]any, 0, len(staged))
		for _, row := range staged {
			rawRows = append(rawRows, row)
		}
		if _, err := session.Table(rawStaging).Insert(rawRows...); err != nil {
			return nil, err
		}
		count, err := session.Table(rawStaging).Where("TradeDate = ?", query.Date).Count(new(TradeHistoryRow))
		if err != nil {
			return nil, err
		}
		if count != int64(len(staged)) {
			return nil, fmt.Errorf("trade staging count mismatch: got %d want %d", count, len(staged))
		}

		if _, err := session.Table(rawPublished).Where("TradeDate = ?", query.Date).Delete(new(TradeHistoryRow)); err != nil {
			return nil, err
		}
		if _, err := session.Table(rawPublished).Insert(rawRows...); err != nil {
			return nil, err
		}
		if _, err := session.Exec("DELETE FROM " + rawStaging); err != nil {
			return nil, err
		}

		for _, spec := range derivedSpecs {
			if _, err := session.Table(spec.table).Where("TradeDate = ?", query.Date).Delete(new(TradeBarRow)); err != nil {
				return nil, err
			}
			if len(spec.rows) == 0 {
				continue
			}
			rows := make([]any, 0, len(spec.rows))
			for _, row := range spec.rows {
				rows = append(rows, row)
			}
			if _, err := session.Table(spec.table).Insert(rows...); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "trade_history",
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Cursor:     query.Date,
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("trade-%s-%s-%d", query.Code, query.Date, time.Now().UnixNano()),
		PhaseID:     "phase_3",
		SuiteName:   "trade_publish",
		Status:      "passed",
		Blocking:    true,
		CommandText: "trade publish transaction",
		OutputText:  fmt.Sprintf("code=%s date=%s rows=%d", query.Code, query.Date, len(staged)),
	})
}

func validateTradeRows(query TradeCollectQuery, items []TradeTick) ([]*TradeHistoryRow, error) {
	if len(items) == 0 {
		return nil, nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Time.Equal(items[j].Time) {
			return items[i].Price < items[j].Price
		}
		return items[i].Time.Before(items[j].Time)
	})

	rows := make([]*TradeHistoryRow, 0, len(items))
	for i, item := range items {
		if item.Time.Format("20060102") != query.Date {
			return nil, fmt.Errorf("trade tick date mismatch: got %s want %s", item.Time.Format("20060102"), query.Date)
		}
		rows = append(rows, &TradeHistoryRow{
			Code:       query.Code,
			TradeDate:  query.Date,
			TradeTime:  item.Time.Unix(),
			Seq:        i + 1,
			Price:      item.Price,
			VolumeHand: item.VolumeHand,
			Number:     item.Number,
			StatusCode: item.StatusCode,
			Side:       item.Side,
		})
	}
	return rows, nil
}

func deriveTradeBars(rows []*TradeHistoryRow) map[int][]*TradeBarRow {
	minuteBars := groupTradeBars(rows, time.Minute)
	return map[int][]*TradeBarRow{
		1:  minuteBars,
		5:  regroupTradeBars(minuteBars, 5*time.Minute),
		15: regroupTradeBars(minuteBars, 15*time.Minute),
		30: regroupTradeBars(minuteBars, 30*time.Minute),
		60: regroupTradeBars(minuteBars, 60*time.Minute),
	}
}

func groupTradeBars(rows []*TradeHistoryRow, interval time.Duration) []*TradeBarRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]*TradeBarRow, 0, len(rows))
	index := make(map[int64]*TradeBarRow)
	for _, row := range rows {
		bucket := time.Unix(row.TradeTime, 0).Truncate(interval)
		key := bucket.Unix()
		bar, ok := index[key]
		if !ok {
			bar = &TradeBarRow{
				Code:       row.Code,
				TradeDate:  row.TradeDate,
				BucketTime: key,
				Open:       row.Price,
				High:       row.Price,
				Low:        row.Price,
				Close:      row.Price,
			}
			index[key] = bar
			out = append(out, bar)
		}
		if row.Price > bar.High {
			bar.High = row.Price
		}
		if row.Price < bar.Low {
			bar.Low = row.Price
		}
		bar.Close = row.Price
		bar.VolumeHand += int64(row.VolumeHand)
		bar.Amount += row.Price * PriceMilli(row.VolumeHand) * 100
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].BucketTime < out[j].BucketTime
	})
	return out
}

func regroupTradeBars(rows []*TradeBarRow, interval time.Duration) []*TradeBarRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]*TradeBarRow, 0, len(rows))
	index := make(map[int64]*TradeBarRow)
	for _, row := range rows {
		bucket := time.Unix(row.BucketTime, 0).Truncate(interval)
		key := bucket.Unix()
		bar, ok := index[key]
		if !ok {
			bar = &TradeBarRow{
				Code:       row.Code,
				TradeDate:  row.TradeDate,
				BucketTime: key,
				Open:       row.Open,
				High:       row.High,
				Low:        row.Low,
				Close:      row.Close,
			}
			index[key] = bar
			out = append(out, bar)
		}
		if row.High > bar.High {
			bar.High = row.High
		}
		if row.Low < bar.Low {
			bar.Low = row.Low
		}
		bar.Close = row.Close
		bar.VolumeHand += row.VolumeHand
		bar.Amount += row.Amount
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].BucketTime < out[j].BucketTime
	})
	return out
}

func loadTradeRows(engine *xorm.Engine, table, date string, dest interface{}) error {
	return engine.Table(table).Where("TradeDate = ?", date).Asc("TradeTime", "Seq").Find(dest)
}

func parseTradeCursor(cursor string) (time.Time, error) {
	if cursor == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("20060102", cursor, time.Local)
}

func tradeDateAfter(a, b string) bool {
	ta, errA := parseTradeCursor(a)
	tb, errB := parseTradeCursor(b)
	if errA != nil || errB != nil {
		return a > b
	}
	return ta.After(tb)
}
