package collector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fundamentalsStubProvider struct {
	finance          *FinanceSnapshot
	categories       []F10Category
	contents         map[string]string
	financeCalls     int
	f10CategoryCalls int
	f10ContentCalls  int
}

func (s *fundamentalsStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return false, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, errors.New("not implemented")
}

func (s *fundamentalsStubProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	s.financeCalls++
	return s.finance, nil
}

func (s *fundamentalsStubProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	s.f10CategoryCalls++
	return s.categories, nil
}

func (s *fundamentalsStubProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	s.f10ContentCalls++
	return &F10Content{
		Code:     query.Code,
		Filename: query.Filename,
		Start:    query.Start,
		Length:   query.Length,
		Content:  s.contents[query.Filename],
	}, nil
}

func (s *fundamentalsStubProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func TestFundamentalsRefreshFinanceAndF10AreReplaySafe(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &fundamentalsStubProvider{
		finance: &FinanceSnapshot{
			Code:        "sh600000",
			UpdatedDate: "20260331",
		},
		categories: []F10Category{
			{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10},
		},
		contents: map[string]string{
			"000001.txt": "平安银行股份有限公司",
		},
	}
	service, err := NewFundamentalsService(store, provider, FundamentalsConfig{
		BaseDir: filepath.Join(tmp, "fundamentals"),
	})
	if err != nil {
		t.Fatalf("new fundamentals service: %v", err)
	}

	if err := service.RefreshFinance(context.Background(), "sh600000"); err != nil {
		t.Fatalf("refresh finance: %v", err)
	}
	if err := service.RefreshFinance(context.Background(), "sh600000"); err != nil {
		t.Fatalf("refresh finance replay: %v", err)
	}
	if err := service.SyncF10(context.Background(), "sh600000"); err != nil {
		t.Fatalf("sync f10: %v", err)
	}
	if err := service.SyncF10(context.Background(), "sh600000"); err != nil {
		t.Fatalf("sync f10 replay: %v", err)
	}

	engine, err := openMetadataEngine(filepath.Join(tmp, "fundamentals", "finance.db"))
	if err != nil {
		t.Fatalf("open finance db: %v", err)
	}
	defer engine.Close()
	financeRows := make([]FinanceRecord, 0)
	if err := engine.Table("Finance").Find(&financeRows); err != nil {
		t.Fatalf("load finance rows: %v", err)
	}
	if len(financeRows) != 1 {
		t.Fatalf("expected 1 finance row after replay-safe refresh, got %d", len(financeRows))
	}

	f10Engine, err := openMetadataEngine(filepath.Join(tmp, "fundamentals", "f10.db"))
	if err != nil {
		t.Fatalf("open f10 db: %v", err)
	}
	defer f10Engine.Close()
	contentRows := make([]F10ContentRecord, 0)
	if err := f10Engine.Table("F10Content").Find(&contentRows); err != nil {
		t.Fatalf("load f10 content rows: %v", err)
	}
	if len(contentRows) != 1 || contentRows[0].ContentHash == "" {
		t.Fatalf("expected one hashed F10 content row, got %+v", contentRows)
	}
}

func TestFundamentalsRefreshFinanceIfUpdatedSkipsUnchangedSnapshots(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &fundamentalsStubProvider{
		finance: &FinanceSnapshot{
			Code:        "sh600000",
			UpdatedDate: "20260401",
		},
	}
	service, err := NewFundamentalsService(store, provider, FundamentalsConfig{
		BaseDir: filepath.Join(tmp, "fundamentals"),
	})
	if err != nil {
		t.Fatalf("new fundamentals service: %v", err)
	}

	changed, updatedDate, err := service.RefreshFinanceIfUpdated(context.Background(), "sh600000")
	if err != nil {
		t.Fatalf("refresh finance if updated first run: %v", err)
	}
	if !changed || updatedDate != "20260401" {
		t.Fatalf("first refresh changed=%v updated_date=%s, want true/20260401", changed, updatedDate)
	}

	changed, updatedDate, err = service.RefreshFinanceIfUpdated(context.Background(), "sh600000")
	if err != nil {
		t.Fatalf("refresh finance if updated second run: %v", err)
	}
	if changed || updatedDate != "20260401" {
		t.Fatalf("second refresh changed=%v updated_date=%s, want false/20260401", changed, updatedDate)
	}
	if provider.financeCalls != 2 {
		t.Fatalf("finance calls = %d, want 2", provider.financeCalls)
	}
}

func TestFundamentalsSyncF10IfChangedSkipsUnchangedContentFetch(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "collector.db"))
	if err != nil {
		t.Fatalf("open collector store: %v", err)
	}
	defer store.Close()

	provider := &fundamentalsStubProvider{
		categories: []F10Category{
			{Code: "sh600000", Name: "公司概况", Filename: "000001.txt", Start: 1, Length: 10},
		},
		contents: map[string]string{
			"000001.txt": "平安银行股份有限公司",
		},
	}
	service, err := NewFundamentalsService(store, provider, FundamentalsConfig{
		BaseDir: filepath.Join(tmp, "fundamentals"),
	})
	if err != nil {
		t.Fatalf("new fundamentals service: %v", err)
	}

	changed, signature, err := service.SyncF10IfChanged(context.Background(), "sh600000")
	if err != nil {
		t.Fatalf("sync f10 if changed first run: %v", err)
	}
	if !changed || signature == "" {
		t.Fatalf("first sync changed=%v signature=%q, want true/non-empty", changed, signature)
	}

	changed, signature2, err := service.SyncF10IfChanged(context.Background(), "sh600000")
	if err != nil {
		t.Fatalf("sync f10 if changed second run: %v", err)
	}
	if changed || signature2 != signature {
		t.Fatalf("second sync changed=%v signature=%q, want false/%q", changed, signature2, signature)
	}
	if provider.f10CategoryCalls != 2 {
		t.Fatalf("f10 category calls = %d, want 2", provider.f10CategoryCalls)
	}
	if provider.f10ContentCalls != 1 {
		t.Fatalf("f10 content calls = %d, want 1", provider.f10ContentCalls)
	}
}
