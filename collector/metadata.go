package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"xorm.io/core"
	"xorm.io/xorm"
)

const (
	MetadataAssetType = "metadata"
	MetadataAllKey    = "all"
)

type MetadataConfig struct {
	CodesDBPath   string
	WorkdayDBPath string
	Now           func() time.Time
}

type MetadataService struct {
	store    *Store
	provider Provider
	cfg      MetadataConfig
}

func NewMetadataService(store *Store, provider Provider, cfg MetadataConfig) (*MetadataService, error) {
	if store == nil {
		return nil, errors.New("metadata service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("metadata service requires provider")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CodesDBPath == "" {
		cfg.CodesDBPath = filepath.Join(DefaultBaseDir, "codes.db")
	}
	if cfg.WorkdayDBPath == "" {
		cfg.WorkdayDBPath = filepath.Join(DefaultBaseDir, "workday.db")
	}
	return &MetadataService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (s *MetadataService) RefreshAll(ctx context.Context) error {
	if err := s.RefreshCodes(ctx); err != nil {
		return err
	}
	return s.RefreshWorkdays(ctx)
}

func (s *MetadataService) RefreshCodes(ctx context.Context) error {
	items, err := s.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock, AssetTypeETF, AssetTypeIndex},
		Refresh:    true,
	})
	if err != nil {
		return err
	}
	staged, err := validateCodeUniverse(items)
	if err != nil {
		return err
	}

	engine, err := openMetadataEngine(s.cfg.CodesDBPath)
	if err != nil {
		return err
	}
	defer engine.Close()

	if err := engine.Sync2(new(MetadataCodeRecord), new(MetadataCodeStagingRecord), new(MetadataUpdateRecord)); err != nil {
		return err
	}

	nowUnix := s.cfg.Now().Unix()
	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM collector_codes_staging"); err != nil {
			return nil, err
		}
		if len(staged) > 0 {
			rows := make([]any, 0, len(staged))
			for _, item := range staged {
				rows = append(rows, item)
			}
			if _, err := session.Insert(rows...); err != nil {
				return nil, err
			}
		}

		count, err := session.Table(new(MetadataCodeStagingRecord)).Count()
		if err != nil {
			return nil, err
		}
		if count != int64(len(staged)) {
			return nil, fmt.Errorf("codes staging count mismatch: got %d want %d", count, len(staged))
		}

		if _, err := session.Exec("DELETE FROM codes"); err != nil {
			return nil, err
		}
		if len(staged) > 0 {
			published := make([]any, 0, len(staged))
			for _, item := range staged {
				published = append(published, &MetadataCodeRecord{
					Name:      item.Name,
					Code:      item.Code,
					Exchange:  item.Exchange,
					Multiple:  item.Multiple,
					Decimal:   item.Decimal,
					LastPrice: item.LastPrice,
				})
			}
			if _, err := session.Insert(published...); err != nil {
				return nil, err
			}
		}

		if _, err := session.Exec("DELETE FROM collector_codes_staging"); err != nil {
			return nil, err
		}
		update := &MetadataUpdateRecord{
			Key:  "codes",
			Time: nowUnix,
		}
		has, err := session.Where("`Key` = ?", update.Key).Exist(new(MetadataUpdateRecord))
		if err != nil {
			return nil, err
		}
		if has {
			_, err = session.Where("`Key` = ?", update.Key).AllCols().Update(update)
			return nil, err
		}
		_, err = session.Insert(update)
		return nil, err
	}); err != nil {
		return err
	}

	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "codes",
		AssetType:  MetadataAssetType,
		Instrument: MetadataAllKey,
		Cursor:     fmt.Sprintf("%d", nowUnix),
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("codes-%d-%d", nowUnix, time.Now().UnixNano()),
		PhaseID:     "phase_1",
		SuiteName:   "metadata_codes_publish",
		Status:      "passed",
		Blocking:    true,
		CommandText: "metadata publish transaction",
		OutputText:  fmt.Sprintf("published_codes=%d", len(staged)),
	})
}

func (s *MetadataService) RefreshWorkdays(ctx context.Context) error {
	items, err := s.provider.TradingDays(ctx, TradingDayQuery{
		Start:   time.Date(1990, 12, 19, 0, 0, 0, 0, time.Local),
		End:     s.cfg.Now().Add(24 * time.Hour),
		Refresh: true,
	})
	if err != nil {
		return err
	}
	staged, err := validateTradingDays(items)
	if err != nil {
		return err
	}

	engine, err := openMetadataEngine(s.cfg.WorkdayDBPath)
	if err != nil {
		return err
	}
	defer engine.Close()

	if err := engine.Sync2(new(MetadataWorkdayRecord), new(MetadataWorkdayStagingRecord)); err != nil {
		return err
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Exec("DELETE FROM collector_workday_staging"); err != nil {
			return nil, err
		}
		if len(staged) > 0 {
			rows := make([]any, 0, len(staged))
			for _, item := range staged {
				rows = append(rows, item)
			}
			if _, err := session.Insert(rows...); err != nil {
				return nil, err
			}
		}

		count, err := session.Table(new(MetadataWorkdayStagingRecord)).Count()
		if err != nil {
			return nil, err
		}
		if count != int64(len(staged)) {
			return nil, fmt.Errorf("workday staging count mismatch: got %d want %d", count, len(staged))
		}

		if _, err := session.Exec("DELETE FROM workday"); err != nil {
			return nil, err
		}
		if len(staged) > 0 {
			published := make([]any, 0, len(staged))
			for _, item := range staged {
				published = append(published, &MetadataWorkdayRecord{
					Unix: item.Unix,
					Date: item.Date,
				})
			}
			if _, err := session.Insert(published...); err != nil {
				return nil, err
			}
		}

		if _, err := session.Exec("DELETE FROM collector_workday_staging"); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}

	last := ""
	if len(staged) > 0 {
		last = staged[len(staged)-1].Date
	}
	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "workday",
		AssetType:  MetadataAssetType,
		Instrument: MetadataAllKey,
		Cursor:     last,
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("workday-%d-%d", s.cfg.Now().Unix(), time.Now().UnixNano()),
		PhaseID:     "phase_1",
		SuiteName:   "metadata_workday_publish",
		Status:      "passed",
		Blocking:    true,
		CommandText: "metadata publish transaction",
		OutputText:  fmt.Sprintf("published_workdays=%d", len(staged)),
	})
}

func openMetadataEngine(filename string) (*xorm.Engine, error) {
	dir, _ := filepath.Split(filename)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return nil, err
		}
	}
	engine, err := xorm.NewEngine("sqlite", filename)
	if err != nil {
		return nil, err
	}
	engine.SetMapper(core.SameMapper{})
	engine.DB().SetMaxOpenConns(1)
	return engine, nil
}

func validateCodeUniverse(items []Instrument) ([]*MetadataCodeStagingRecord, error) {
	if len(items) == 0 {
		return nil, errors.New("code universe must not be empty")
	}
	seen := make(map[string]struct{}, len(items))
	rows := make([]*MetadataCodeStagingRecord, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Code) == "" || strings.TrimSpace(item.Exchange) == "" {
			return nil, errors.New("code universe contains empty code or exchange")
		}
		key := item.Exchange + ":" + strings.TrimPrefix(item.Code, item.Exchange)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate instrument in staged code universe: %s", item.Code)
		}
		seen[key] = struct{}{}
		rows = append(rows, &MetadataCodeStagingRecord{
			Name:      item.Name,
			Code:      strings.TrimPrefix(item.Code, item.Exchange),
			Exchange:  item.Exchange,
			Multiple:  item.Multiple,
			Decimal:   item.Decimal,
			LastPrice: item.LastPrice.Float64(),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Exchange == rows[j].Exchange {
			return rows[i].Code < rows[j].Code
		}
		return rows[i].Exchange < rows[j].Exchange
	})
	return rows, nil
}

func validateTradingDays(items []TradingDay) ([]*MetadataWorkdayStagingRecord, error) {
	if len(items) == 0 {
		return nil, errors.New("trading day universe must not be empty")
	}
	seen := make(map[string]struct{}, len(items))
	rows := make([]*MetadataWorkdayStagingRecord, 0, len(items))
	var last int64
	for _, item := range items {
		if strings.TrimSpace(item.Date) == "" {
			return nil, errors.New("trading day contains empty date")
		}
		if _, ok := seen[item.Date]; ok {
			return nil, fmt.Errorf("duplicate trading day in staged workday set: %s", item.Date)
		}
		seen[item.Date] = struct{}{}
		unix := item.Time.Unix()
		if last > 0 && unix < last {
			return nil, fmt.Errorf("trading days are not monotonic: %s", item.Date)
		}
		last = unix
		rows = append(rows, &MetadataWorkdayStagingRecord{
			Unix: unix,
			Date: item.Date,
		})
	}
	return rows, nil
}
