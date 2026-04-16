package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"xorm.io/xorm"
)

type FundamentalsConfig struct {
	BaseDir string
	Now     func() time.Time
}

type FundamentalsService struct {
	store     *Store
	provider  Provider
	cfg       FundamentalsConfig
	financeMu sync.Mutex
	f10Mu     sync.Mutex
}

type FinanceRecord struct {
	Code        string `xorm:"varchar(16) index notnull"`
	UpdatedDate string `xorm:"varchar(16) index notnull"`
	PayloadJSON string `xorm:"text notnull"`
	InDate      int64  `xorm:"created"`
}

type F10CategoryRecord struct {
	Code     string `xorm:"varchar(16) index notnull"`
	Name     string `xorm:"varchar(255) notnull"`
	Filename string `xorm:"varchar(255) index notnull"`
	Start    uint32 `xorm:"notnull"`
	Length   uint32 `xorm:"notnull"`
}

type F10ContentRecord struct {
	Code        string `xorm:"varchar(16) index notnull"`
	Filename    string `xorm:"varchar(255) index notnull"`
	Start       uint32 `xorm:"notnull"`
	Length      uint32 `xorm:"notnull"`
	ContentHash string `xorm:"varchar(128) index notnull"`
	Content     string `xorm:"text notnull"`
}

func NewFundamentalsService(store *Store, provider Provider, cfg FundamentalsConfig) (*FundamentalsService, error) {
	if store == nil {
		return nil, errors.New("fundamentals service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("fundamentals service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "fundamentals")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &FundamentalsService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (s *FundamentalsService) RefreshFinance(ctx context.Context, code string) error {
	if code == "" {
		return errors.New("finance refresh requires code")
	}
	payload, err := s.provider.Finance(ctx, code)
	if err != nil {
		return err
	}
	if payload == nil {
		return errors.New("finance payload is nil")
	}

	s.financeMu.Lock()
	defer s.financeMu.Unlock()

	engine, err := s.openFundamentalsEngine("finance.db")
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.Table("Finance").Sync2(new(FinanceRecord)); err != nil {
		return err
	}

	record := &FinanceRecord{
		Code:        code,
		UpdatedDate: payload.UpdatedDate,
		PayloadJSON: mustJSON(payload),
	}
	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Table("Finance").Where("Code = ? AND UpdatedDate = ?", code, payload.UpdatedDate).Delete(new(FinanceRecord)); err != nil {
			return nil, err
		}
		if _, err := session.Table("Finance").Insert(record); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "finance",
		AssetType:  MetadataAssetType,
		Instrument: code,
		Cursor:     payload.UpdatedDate,
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("finance-%s-%s-%d", code, payload.UpdatedDate, time.Now().UnixNano()),
		PhaseID:     "phase_6",
		SuiteName:   "finance_refresh",
		Status:      "passed",
		Blocking:    true,
		CommandText: "finance refresh",
		OutputText:  fmt.Sprintf("code=%s updated_date=%s", code, payload.UpdatedDate),
	})
}

func (s *FundamentalsService) SyncF10(ctx context.Context, code string) error {
	if code == "" {
		return errors.New("f10 sync requires code")
	}
	categories, err := s.provider.F10Categories(ctx, code)
	if err != nil {
		return err
	}

	s.f10Mu.Lock()
	defer s.f10Mu.Unlock()

	engine, err := s.openFundamentalsEngine("f10.db")
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.Table("F10Category").Sync2(new(F10CategoryRecord)); err != nil {
		return err
	}
	if err := engine.Table("F10Content").Sync2(new(F10ContentRecord)); err != nil {
		return err
	}

	categoryRows := make([]any, 0, len(categories))
	contentRows := make([]any, 0, len(categories))
	for _, category := range categories {
		categoryRows = append(categoryRows, &F10CategoryRecord{
			Code:     code,
			Name:     category.Name,
			Filename: category.Filename,
			Start:    category.Start,
			Length:   category.Length,
		})
		content, err := s.provider.F10Content(ctx, F10ContentQuery{
			Code:     code,
			Filename: category.Filename,
			Start:    category.Start,
			Length:   category.Length,
		})
		if err != nil {
			return err
		}
		contentRows = append(contentRows, &F10ContentRecord{
			Code:        code,
			Filename:    category.Filename,
			Start:       category.Start,
			Length:      category.Length,
			ContentHash: hashContent(content.Content),
			Content:     content.Content,
		})
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Table("F10Category").Where("Code = ?", code).Delete(new(F10CategoryRecord)); err != nil {
			return nil, err
		}
		if _, err := session.Table("F10Content").Where("Code = ?", code).Delete(new(F10ContentRecord)); err != nil {
			return nil, err
		}
		if len(categoryRows) > 0 {
			if _, err := session.Table("F10Category").Insert(categoryRows...); err != nil {
				return nil, err
			}
		}
		if len(contentRows) > 0 {
			if _, err := session.Table("F10Content").Insert(contentRows...); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := s.store.UpsertCollectCursor(&CollectCursorRecord{
		Domain:     "f10",
		AssetType:  MetadataAssetType,
		Instrument: code,
		Cursor:     fmt.Sprintf("%d", len(categories)),
	}); err != nil {
		return err
	}
	return s.store.AddValidationRun(&ValidationRunRecord{
		RunID:       fmt.Sprintf("f10-%s-%d", code, time.Now().UnixNano()),
		PhaseID:     "phase_6",
		SuiteName:   "f10_sync",
		Status:      "passed",
		Blocking:    true,
		CommandText: "f10 sync",
		OutputText:  fmt.Sprintf("code=%s categories=%d", code, len(categories)),
	})
}

func (s *FundamentalsService) openFundamentalsEngine(filename string) (*xorm.Engine, error) {
	if err := os.MkdirAll(s.cfg.BaseDir, 0o777); err != nil {
		return nil, err
	}
	return openMetadataEngine(filepath.Join(s.cfg.BaseDir, filename))
}

func hashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	bs, _ := json.Marshal(v)
	return string(bs)
}
