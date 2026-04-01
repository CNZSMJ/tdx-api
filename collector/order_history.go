package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"xorm.io/xorm"
)

type OrderHistoryConfig struct {
	BaseDir string
	Now     func() time.Time
}

type OrderHistoryService struct {
	store    *Store
	provider Provider
	cfg      OrderHistoryConfig
}

type OrderHistoryCollectQuery struct {
	Code      string
	AssetType AssetType
	Date      string
}

type OrderHistoryRow struct {
	Code         string     `xorm:"varchar(16) index notnull"`
	TradeDate    string     `xorm:"varchar(16) index notnull"`
	Seq          int        `xorm:"index notnull"`
	Price        PriceMilli `xorm:"notnull"`
	BuySellDelta int        `xorm:"notnull"`
	Volume       int        `xorm:"notnull"`
	InDate       int64      `xorm:"created"`
}

func NewOrderHistoryService(store *Store, provider Provider, cfg OrderHistoryConfig) (*OrderHistoryService, error) {
	if store == nil {
		return nil, errors.New("order history service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("order history service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "order_history")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &OrderHistoryService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (s *OrderHistoryService) RefreshDay(ctx context.Context, query OrderHistoryCollectQuery) error {
	if query.Code == "" || query.Date == "" {
		return errors.New("order history refresh requires code and date")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}

	snapshot, err := s.provider.OrderHistory(ctx, OrderHistoryQuery{
		Code: query.Code,
		Date: query.Date,
	})
	if err != nil {
		return err
	}
	staged, err := validateOrderHistoryRows(query, snapshot)
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

	publishedTable := "OrderHistory"
	stagingTable := "collector_order_history_staging"
	if err := engine.Table(publishedTable).Sync2(new(OrderHistoryRow)); err != nil {
		return err
	}
	if err := engine.Table(stagingTable).Sync2(new(OrderHistoryRow)); err != nil {
		return err
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM " + stagingTable); err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(staged))
		for _, row := range staged {
			rows = append(rows, row)
		}
		if _, err := session.Table(stagingTable).Insert(rows...); err != nil {
			return nil, err
		}
		count, err := session.Table(stagingTable).Where("TradeDate = ?", query.Date).Count(new(OrderHistoryRow))
		if err != nil {
			return nil, err
		}
		if count != int64(len(staged)) {
			return nil, fmt.Errorf("order history staging count mismatch: got %d want %d", count, len(staged))
		}

		if _, err := session.Table(publishedTable).Where("TradeDate = ?", query.Date).Delete(new(OrderHistoryRow)); err != nil {
			return nil, err
		}
		if _, err := session.Table(publishedTable).Insert(rows...); err != nil {
			return nil, err
		}
		if _, err := session.Exec("DELETE FROM " + stagingTable); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "order_history",
		AssetType:  string(query.AssetType),
		Instrument: query.Code,
		Cursor:     query.Date,
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("order-history-%s-%s-%d", query.Code, query.Date, time.Now().UnixNano()),
		PhaseID:     "phase_4",
		SuiteName:   "order_history_publish",
		Status:      "passed",
		Blocking:    true,
		CommandText: "order history publish transaction",
		OutputText:  fmt.Sprintf("code=%s date=%s rows=%d", query.Code, query.Date, len(staged)),
	})
}

func validateOrderHistoryRows(query OrderHistoryCollectQuery, snapshot *OrderHistorySnapshot) ([]*OrderHistoryRow, error) {
	if snapshot == nil {
		return nil, errors.New("order history snapshot is nil")
	}
	if snapshot.Date != query.Date {
		return nil, fmt.Errorf("order history snapshot date mismatch: got %s want %s", snapshot.Date, query.Date)
	}
	rows := make([]*OrderHistoryRow, 0, len(snapshot.Items))
	for i, item := range snapshot.Items {
		rows = append(rows, &OrderHistoryRow{
			Code:         query.Code,
			TradeDate:    query.Date,
			Seq:          i + 1,
			Price:        item.Price,
			BuySellDelta: item.BuySellDelta,
			Volume:       item.Volume,
		})
	}
	return rows, nil
}
