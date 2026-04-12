package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"xorm.io/xorm"
)

type LiveCaptureConfig struct {
	BaseDir            string
	BootstrapStartDate string
	Now                func() time.Time
}

type LiveCaptureService struct {
	store    *Store
	provider Provider
	cfg      LiveCaptureConfig
}

type QuoteCaptureQuery struct {
	Codes       []string
	CaptureTime time.Time
}

type SessionCaptureQuery struct {
	Code      string
	AssetType AssetType
	Date      string
}

type QuoteSnapshotRow struct {
	Code        string     `xorm:"varchar(16) index notnull"`
	CaptureTime int64      `xorm:"index notnull"`
	Last        PriceMilli `xorm:"notnull"`
	PreClose    PriceMilli `xorm:"notnull"`
	Open        PriceMilli `xorm:"notnull"`
	High        PriceMilli `xorm:"notnull"`
	Low         PriceMilli `xorm:"notnull"`
	VolumeHand  int64      `xorm:"notnull"`
	AmountYuan  float64    `xorm:"notnull"`
}

type MinuteLiveRow struct {
	Code      string     `xorm:"varchar(16) index notnull"`
	TradeDate string     `xorm:"varchar(16) index notnull"`
	Clock     string     `xorm:"varchar(8) index notnull"`
	Price     PriceMilli `xorm:"notnull"`
	Number    int        `xorm:"notnull"`
}

type TradeLiveRow struct {
	Code       string     `xorm:"varchar(16) index notnull"`
	TradeDate  string     `xorm:"varchar(16) index notnull"`
	TradeTime  int64      `xorm:"index notnull"`
	Seq        int        `xorm:"index notnull"`
	Price      PriceMilli `xorm:"notnull"`
	VolumeHand int        `xorm:"notnull"`
	Number     int        `xorm:"notnull"`
	StatusCode int        `xorm:"notnull"`
	Side       string     `xorm:"varchar(16)"`
}

const (
	liveCaptureDomain              = "live_capture"
	liveCaptureCoverageStartDomain = "live_capture_coverage_start"
)

func NewLiveCaptureService(store *Store, provider Provider, cfg LiveCaptureConfig) (*LiveCaptureService, error) {
	if store == nil {
		return nil, errors.New("live capture service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("live capture service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "live")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &LiveCaptureService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

const quoteBatchSize = 80

func (s *LiveCaptureService) CaptureQuotes(ctx context.Context, query QuoteCaptureQuery) error {
	if len(query.Codes) == 0 {
		return nil
	}
	if query.CaptureTime.IsZero() {
		query.CaptureTime = s.cfg.Now()
	}

	var items []QuoteSnapshot
	for start := 0; start < len(query.Codes); start += quoteBatchSize {
		end := start + quoteBatchSize
		if end > len(query.Codes) {
			end = len(query.Codes)
		}
		batch, err := s.provider.Quotes(ctx, query.Codes[start:end])
		if err != nil {
			log.Printf("quote snapshot batch %d-%d/%d failed: %v", start, end, len(query.Codes), err)
			continue
		}
		items = append(items, batch...)
	}
	if len(items) == 0 {
		return nil
	}

	engine, err := s.openLiveEngine("quotes.db")
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.Table("QuoteSnapshot").Sync2(new(QuoteSnapshotRow)); err != nil {
		return err
	}

	rows := make([]any, 0, len(items))
	captureUnix := query.CaptureTime.Unix()
	for _, item := range items {
		rows = append(rows, &QuoteSnapshotRow{
			Code:        item.Code,
			CaptureTime: captureUnix,
			Last:        item.Last,
			PreClose:    item.PreClose,
			Open:        item.Open,
			High:        item.High,
			Low:         item.Low,
			VolumeHand:  item.VolumeHand,
			AmountYuan:  item.AmountYuan,
		})
	}

	const insertBatchSize = 500
	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		for batchStart := 0; batchStart < len(query.Codes); batchStart += insertBatchSize {
			batchEnd := batchStart + insertBatchSize
			if batchEnd > len(query.Codes) {
				batchEnd = len(query.Codes)
			}
			for _, code := range query.Codes[batchStart:batchEnd] {
				if _, err := session.Table("QuoteSnapshot").Where("Code = ? AND CaptureTime = ?", code, captureUnix).Delete(new(QuoteSnapshotRow)); err != nil {
					return nil, err
				}
			}
		}
		for batchStart := 0; batchStart < len(rows); batchStart += insertBatchSize {
			batchEnd := batchStart + insertBatchSize
			if batchEnd > len(rows) {
				batchEnd = len(rows)
			}
			if _, err := session.Table("QuoteSnapshot").Insert(rows[batchStart:batchEnd]...); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		return err
	}

	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("live-quotes-%d-%d", captureUnix, time.Now().UnixNano()),
		PhaseID:     "phase_5",
		SuiteName:   "live_quote_capture",
		Status:      "passed",
		Blocking:    true,
		CommandText: "live quote capture",
		OutputText:  fmt.Sprintf("quotes=%d capture_time=%d", len(items), captureUnix),
	})
}

func (s *LiveCaptureService) CaptureSession(ctx context.Context, query SessionCaptureQuery) error {
	if query.Code == "" {
		return errors.New("live session capture requires code")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}
	if query.Date == "" {
		query.Date = s.cfg.Now().Format("20060102")
	}

	minutes, err := s.provider.Minutes(ctx, MinuteQuery{Code: query.Code, Date: query.Date})
	if err != nil {
		return err
	}
	trades, err := s.provider.TradeHistory(ctx, TradeHistoryQuery{Code: query.Code, Date: query.Date})
	if err != nil {
		return err
	}
	return s.publishLiveDay(query, minutes, trades, "live_capture")
}

func (s *LiveCaptureService) ReconcileDay(ctx context.Context, query SessionCaptureQuery) error {
	if query.Code == "" {
		return errors.New("live reconcile requires code")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}
	if query.Date == "" {
		return errors.New("live reconcile requires date")
	}

	minutes, err := s.provider.Minutes(ctx, MinuteQuery{Code: query.Code, Date: query.Date})
	if err != nil {
		return err
	}
	trades, err := s.provider.TradeHistory(ctx, TradeHistoryQuery{Code: query.Code, Date: query.Date})
	if err != nil {
		return err
	}
	return s.publishLiveDay(query, minutes, trades, "live_reconcile")
}

func (s *LiveCaptureService) publishLiveDay(query SessionCaptureQuery, minutes []MinutePoint, trades []TradeTick, suite string) error {
	engine, err := s.openLiveEngine(query.Code + ".db")
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.Table("MinuteLive").Sync2(new(MinuteLiveRow)); err != nil {
		return err
	}
	if err := engine.Table("TradeLive").Sync2(new(TradeLiveRow)); err != nil {
		return err
	}

	minuteRows := make([]any, 0, len(minutes))
	for _, item := range minutes {
		minuteRows = append(minuteRows, &MinuteLiveRow{
			Code:      query.Code,
			TradeDate: query.Date,
			Clock:     item.Clock,
			Price:     item.Price,
			Number:    item.Number,
		})
	}
	tradeRows := make([]any, 0, len(trades))
	for i, item := range trades {
		tradeRows = append(tradeRows, &TradeLiveRow{
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

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Table("MinuteLive").Where("TradeDate = ?", query.Date).Delete(new(MinuteLiveRow)); err != nil {
			return nil, err
		}
		if _, err := session.Table("TradeLive").Where("TradeDate = ?", query.Date).Delete(new(TradeLiveRow)); err != nil {
			return nil, err
		}
		if len(minuteRows) > 0 {
			if _, err := session.Table("MinuteLive").Insert(minuteRows...); err != nil {
				return nil, err
			}
		}
		if len(tradeRows) > 0 {
			if _, err := session.Table("TradeLive").Insert(tradeRows...); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := s.updateCoverageCursors(query); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("%s-%s-%s-%d", suite, query.Code, query.Date, time.Now().UnixNano()),
		PhaseID:     "phase_5",
		SuiteName:   suite,
		Status:      "passed",
		Blocking:    true,
		CommandText: suite,
		OutputText:  fmt.Sprintf("code=%s date=%s minutes=%d trades=%d", query.Code, query.Date, len(minutes), len(trades)),
	})
}

func (s *LiveCaptureService) updateCoverageCursors(query SessionCaptureQuery) error {
	if err := s.upsertLatestCursor(query); err != nil {
		return err
	}
	return s.upsertCoverageStartCursor(query)
}

func (s *LiveCaptureService) upsertLatestCursor(query SessionCaptureQuery) error {
	cursorValue := query.Date
	current, err := s.store.GetCollectCursor(liveCaptureDomain, string(query.AssetType), query.Code, "")
	if err != nil {
		return err
	}
	if current != nil && current.Cursor != "" && tradeDateAfter(current.Cursor, cursorValue) {
		cursorValue = current.Cursor
	}
	return s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     liveCaptureDomain,
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Cursor:     cursorValue,
	})
}

func (s *LiveCaptureService) upsertCoverageStartCursor(query SessionCaptureQuery) error {
	cursorValue := query.Date
	current, err := s.store.GetCollectCursor(liveCaptureCoverageStartDomain, string(query.AssetType), query.Code, "")
	if err != nil {
		return err
	}
	if current == nil || current.Cursor == "" {
		if seeded := seedTradeCoverageStart(s.cfg.BootstrapStartDate, query.Date); seeded != "" {
			cursorValue = seeded
		}
	} else if tradeDateAfter(cursorValue, current.Cursor) {
		cursorValue = current.Cursor
	}
	return s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     liveCaptureCoverageStartDomain,
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Cursor:     cursorValue,
	})
}

func (s *LiveCaptureService) openLiveEngine(filename string) (*xorm.Engine, error) {
	if err := os.MkdirAll(s.cfg.BaseDir, 0o777); err != nil {
		return nil, err
	}
	return openMetadataEngine(filepath.Join(s.cfg.BaseDir, filename))
}
