package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type auctionStubProvider struct {
	instruments []Instrument
	quotes      []QuoteSnapshot
}

func (s *auctionStubProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	return s.instruments, nil
}

func (s *auctionStubProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	var result []QuoteSnapshot
	codeSet := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		codeSet[c] = struct{}{}
	}
	for _, q := range s.quotes {
		if _, ok := codeSet[q.Code]; ok {
			result = append(result, q)
		}
	}
	return result, nil
}

// Remaining Provider interface stubs
func (s *auctionStubProvider) TradingDays(context.Context, TradingDayQuery) ([]TradingDay, error) {
	return nil, nil
}
func (s *auctionStubProvider) IsTradingDay(context.Context, time.Time) (bool, error) {
	return true, nil
}
func (s *auctionStubProvider) Minutes(context.Context, MinuteQuery) ([]MinutePoint, error) {
	return nil, nil
}
func (s *auctionStubProvider) Klines(context.Context, KlineQuery) ([]KlineBar, error) {
	return nil, nil
}
func (s *auctionStubProvider) TradeHistory(context.Context, TradeHistoryQuery) ([]TradeTick, error) {
	return nil, nil
}
func (s *auctionStubProvider) OrderHistory(context.Context, OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	return nil, nil
}
func (s *auctionStubProvider) Finance(context.Context, string) (*FinanceSnapshot, error) {
	return nil, nil
}
func (s *auctionStubProvider) F10Categories(context.Context, string) ([]F10Category, error) {
	return nil, nil
}
func (s *auctionStubProvider) F10Content(context.Context, F10ContentQuery) (*F10Content, error) {
	return nil, nil
}
func (s *auctionStubProvider) BlockGroups(context.Context, string) ([]BlockInfo, error) {
	return nil, nil
}

func TestAuctionCollectAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Date(2026, 5, 29, 9, 20, 0, 0, time.Local)

	provider := &auctionStubProvider{
		instruments: []Instrument{
			{Code: "002579.SZ", Name: "中京电子", AssetType: AssetTypeStock},
			{Code: "000063.SZ", Name: "中兴通讯", AssetType: AssetTypeStock},
			{Code: "300001.SZ", Name: "测试基金", AssetType: AssetType("fund")},
		},
		quotes: []QuoteSnapshot{
			{
				Code:     "002579.SZ",
				Name:     "中京电子",
				PreClose: PriceMilli(18500),
				Last:     PriceMilli(19300),
				BuyLevels: []QuoteLevel{
					{Price: PriceMilli(19290), Number: 1200},
				},
				AmountYuan: 85000000,
			},
			{
				Code:     "000063.SZ",
				Name:     "中兴通讯",
				PreClose: PriceMilli(42000),
				Last:     PriceMilli(40000),
				BuyLevels: []QuoteLevel{
					{Price: PriceMilli(39980), Number: 500},
				},
				AmountYuan: 75000000,
			},
		},
	}

	cfg := AuctionConfig{
		BaseDir: tmpDir,
		Now:     func() time.Time { return now },
	}

	svc, err := NewAuctionService(provider, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Collect
	if err := svc.Collect(context.Background(), "2026-05-29", "09:20:00"); err != nil {
		t.Fatal(err)
	}

	engine, err := openMetadataEngine(filepath.Join(tmpDir, "auction.db"))
	if err != nil {
		t.Fatalf("open auction db: %v", err)
	}
	defer engine.Close()
	var rows []AuctionSnapshotRow
	if err := engine.Table("AuctionSnapshot").Find(&rows); err != nil {
		t.Fatalf("load auction rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 auction snapshot rows, got %d", len(rows))
	}

	// Load
	snap, err := svc.Load("2026-05-29", "09:20:00")
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if len(snap.Items) != 2 {
		t.Fatalf("expected 2 items (stock only), got %d", len(snap.Items))
	}

	// Verify item data
	var item AuctionSnapshotItem
	for _, candidate := range snap.Items {
		if candidate.InstrumentCode == "sz002579" {
			item = candidate
			break
		}
	}
	if item.InstrumentCode == "" {
		t.Fatalf("expected sz002579 in snapshot, got %+v", snap.Items)
	}
	if item.AuctionPct < 4.0 || item.AuctionPct > 5.0 {
		t.Errorf("expected auction_pct ~4.32%%, got %.2f", item.AuctionPct)
	}
	if item.IsLimitUpOpen {
		t.Error("expected IsLimitUpOpen=false")
	}

	// Filter
	filtered := snap.FilterItems([]string{"000063.SZ"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(filtered))
	}
	if filtered[0].InstrumentCode != "sz000063" {
		t.Errorf("expected sz000063, got %s", filtered[0].InstrumentCode)
	}

	// Load non-existent
	snap2, err := svc.Load("2026-05-29", "09:26:00")
	if err != nil {
		t.Fatal(err)
	}
	if snap2 != nil {
		t.Error("expected nil for non-existent snapshot")
	}
}

func TestAuctionLoadNormalizesTradeDate(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Date(2026, 5, 29, 9, 20, 0, 0, time.Local)
	provider := &auctionStubProvider{
		instruments: []Instrument{
			{Code: "sz002579", Name: "中京电子", AssetType: AssetTypeStock},
		},
		quotes: []QuoteSnapshot{
			{
				Code:       "sz002579",
				Name:       "中京电子",
				PreClose:   PriceMilli(18500),
				Last:       PriceMilli(19300),
				AmountYuan: 85000000,
				BuyLevels:  []QuoteLevel{{Price: PriceMilli(19290), Number: 1200}},
				SellLevels: []QuoteLevel{{Price: PriceMilli(19300), Number: 900}},
			},
		},
	}
	svc, err := NewAuctionService(provider, AuctionConfig{
		BaseDir: tmpDir,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Collect(context.Background(), "20260529", "09:20:00"); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.Load("2026-05-29", "09:20:00")
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || len(snap.Items) != 1 || snap.Items[0].InstrumentCode != "sz002579" {
		t.Fatalf("expected normalized-date load to return sz002579 snapshot, got %+v", snap)
	}
}
