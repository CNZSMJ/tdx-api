package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"xorm.io/xorm"
)

const auctionDBName = "auction.db"

// AuctionConfig configures the auction snapshot collector.
type AuctionConfig struct {
	BaseDir string
	Now     func() time.Time
}

// AuctionService collects and retrieves auction snapshots.
type AuctionService struct {
	provider Provider
	cfg      AuctionConfig
}

// AuctionSnapshotItem represents one instrument's auction snapshot.
type AuctionSnapshotItem struct {
	InstrumentCode  string  `json:"instrument_code"`
	Name            string  `json:"name"`
	AuctionPrice    float64 `json:"auction_price"`
	AuctionAmount   float64 `json:"auction_amount"`
	PrevClose       float64 `json:"prev_close"`
	AuctionPct      float64 `json:"auction_pct"`
	Bid1Price       float64 `json:"bid1_price"`
	Bid1Volume      int64   `json:"bid1_volume"`
	Ask1Price       float64 `json:"ask1_price"`
	Ask1Volume      int64   `json:"ask1_volume"`
	IsLimitUpOpen   bool    `json:"is_limit_up_open"`
	IsLimitDownOpen bool    `json:"is_limit_down_open"`
	CollectedAt     string  `json:"collected_at"`
}

// AuctionSnapshotRow is the persisted DB row for one instrument at one auction checkpoint.
type AuctionSnapshotRow struct {
	ID              int64   `xorm:"pk autoincr"`
	TradeDate       string  `xorm:"varchar(10) index notnull"`
	SnapshotTime    string  `xorm:"varchar(8) index notnull"`
	InstrumentCode  string  `xorm:"varchar(16) index notnull"`
	Name            string  `xorm:"varchar(128)"`
	AuctionPrice    float64 `xorm:"notnull"`
	AuctionAmount   float64 `xorm:"notnull"`
	PrevClose       float64 `xorm:"notnull"`
	AuctionPct      float64 `xorm:"notnull"`
	Bid1Price       float64 `xorm:"notnull"`
	Bid1Volume      int64   `xorm:"notnull"`
	Ask1Price       float64 `xorm:"notnull"`
	Ask1Volume      int64   `xorm:"notnull"`
	IsLimitUpOpen   bool    `xorm:"notnull"`
	IsLimitDownOpen bool    `xorm:"notnull"`
	CollectedAt     int64   `xorm:"index notnull"`
}

func (*AuctionSnapshotRow) TableName() string {
	return "AuctionSnapshot"
}

// AuctionSnapshot is the full snapshot for one time point.
type AuctionSnapshot struct {
	TradeDate    string                `json:"trade_date"`
	SnapshotTime string                `json:"snapshot_time"`
	Items        []AuctionSnapshotItem `json:"items"`
	CollectedAt  string                `json:"collected_at"`
}

// NewAuctionService creates a new auction service.
func NewAuctionService(provider Provider, cfg AuctionConfig) (*AuctionService, error) {
	if provider == nil {
		return nil, errors.New("auction service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "auction")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &AuctionService{provider: provider, cfg: cfg}, nil
}

// Collect captures an auction snapshot for the given trade date and snapshot time.
func (s *AuctionService) Collect(ctx context.Context, tradeDate string, snapshotTime string) error {
	normalizedTradeDate, err := normalizeAuctionTradeDate(tradeDate)
	if err != nil {
		return err
	}
	normalizedSnapshotTime, err := normalizeAuctionSnapshotTime(snapshotTime)
	if err != nil {
		return err
	}

	log.Printf("[auction] collecting %s %s -> %s", normalizedTradeDate, normalizedSnapshotTime, filepath.Join(s.cfg.BaseDir, auctionDBName))

	// Get all A-share instruments
	instruments, err := s.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock},
	})
	if err != nil {
		return fmt.Errorf("auction collect: instruments: %w", err)
	}

	// Filter to A-share stocks only (not bonds, funds, etc.)
	codes := make([]string, 0, len(instruments))
	for _, inst := range instruments {
		if inst.AssetType == AssetTypeStock {
			codes = append(codes, inst.Code)
		}
	}
	log.Printf("[auction] fetching quotes for %d A-share stocks", len(codes))

	allQuotes := make([]QuoteSnapshot, 0, len(codes))
	batchFailures := 0
	batchSize := quoteBatchSize
	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}
		batch := codes[i:end]
		quotes, err := s.provider.Quotes(ctx, batch)
		if err != nil {
			batchFailures++
			log.Printf("[auction] quotes batch %d failed: %v", i/batchSize, err)
			continue
		}
		allQuotes = append(allQuotes, quotes...)
	}
	if len(codes) > 0 && len(allQuotes) == 0 {
		return fmt.Errorf("auction collect: all quote batches failed")
	}

	now := s.cfg.Now().In(time.Local)
	snapshot := AuctionSnapshot{
		TradeDate:    normalizedTradeDate,
		SnapshotTime: normalizedSnapshotTime,
		Items:        make([]AuctionSnapshotItem, 0, len(allQuotes)),
		CollectedAt:  now.Format(time.RFC3339),
	}

	limitUpPct := 9.9 // default 10% limit
	limitDownPct := -9.9

	for _, q := range allQuotes {
		if q.Code == "" {
			continue
		}
		prevClose := q.PreClose.Float64()
		if prevClose <= 0 {
			continue
		}
		last := q.Last.Float64()

		aucPct := 0.0
		if last > 0 {
			aucPct = (last - prevClose) / prevClose * 100
		}

		bid1Price := 0.0
		var bid1Volume int64
		if len(q.BuyLevels) > 0 {
			bid1Price = q.BuyLevels[0].Price.Float64()
			bid1Volume = int64(q.BuyLevels[0].Number)
		}
		ask1Price := 0.0
		var ask1Volume int64
		if len(q.SellLevels) > 0 {
			ask1Price = q.SellLevels[0].Price.Float64()
			ask1Volume = int64(q.SellLevels[0].Number)
		}

		amount := q.AmountYuan // already in yuan from QuoteSnapshot
		if amount == 0 && last > 0 {
			amount = float64(q.VolumeHand) * last * 100 // fallback: hand * price * 100
		}

		item := AuctionSnapshotItem{
			InstrumentCode:  normalizeAuctionInstrumentCode(q.Code),
			Name:            q.Name,
			AuctionPrice:    last,
			AuctionAmount:   amount,
			PrevClose:       prevClose,
			AuctionPct:      aucPct,
			Bid1Price:       bid1Price,
			Bid1Volume:      bid1Volume,
			Ask1Price:       ask1Price,
			Ask1Volume:      ask1Volume,
			IsLimitUpOpen:   last > 0 && aucPct >= limitUpPct,
			IsLimitDownOpen: last > 0 && aucPct <= limitDownPct,
			CollectedAt:     now.Format(time.RFC3339),
		}
		snapshot.Items = append(snapshot.Items, item)
	}

	if err := StoreAuctionSnapshot(s.cfg.BaseDir, snapshot); err != nil {
		return err
	}

	log.Printf("[auction] collected %d instruments for %s %s", len(snapshot.Items), tradeDate, snapshotTime)
	if batchFailures > 0 {
		return fmt.Errorf("auction collect: %d quote batches failed after storing %d instruments", batchFailures, len(snapshot.Items))
	}
	return nil
}

// Load retrieves a stored auction snapshot. Returns nil, nil if not found.
func (s *AuctionService) Load(tradeDate string, snapshotTime string) (*AuctionSnapshot, error) {
	return LoadAuctionSnapshot(s.cfg.BaseDir, tradeDate, snapshotTime)
}

// FilterItems returns items matching the given instrument codes.
func (snap *AuctionSnapshot) FilterItems(codes []string) []AuctionSnapshotItem {
	if snap == nil || len(codes) == 0 {
		return nil
	}
	codeSet := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		if code := normalizeAuctionInstrumentCode(c); code != "" {
			codeSet[code] = struct{}{}
		}
	}
	var result []AuctionSnapshotItem
	for _, item := range snap.Items {
		if _, ok := codeSet[normalizeAuctionInstrumentCode(item.InstrumentCode)]; ok {
			result = append(result, item)
		}
	}
	return result
}

func auctionBaseDir(baseDir string) string {
	if baseDir == "" {
		return filepath.Join(DefaultBaseDir, "auction")
	}
	return baseDir
}

func openAuctionEngine(baseDir string) (*xorm.Engine, error) {
	engine, err := openMetadataEngine(filepath.Join(auctionBaseDir(baseDir), auctionDBName))
	if err != nil {
		return nil, err
	}
	if err := engine.Table("AuctionSnapshot").Sync2(new(AuctionSnapshotRow)); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return engine, nil
}

func normalizeAuctionTradeDate(value string) (string, error) {
	text := strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		t, err := time.ParseInLocation(layout, text, time.Local)
		if err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("auction trade date must be YYYY-MM-DD or YYYYMMDD: %s", value)
}

func normalizeAuctionSnapshotTime(value string) (string, error) {
	text := strings.TrimSpace(value)
	t, err := time.ParseInLocation("15:04:05", text, time.Local)
	if err != nil {
		return "", fmt.Errorf("auction snapshot time must be HH:MM:SS: %s", value)
	}
	return t.Format("15:04:05"), nil
}

func normalizeAuctionInstrumentCode(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	if len(text) == 8 && (strings.HasPrefix(text, "sh") || strings.HasPrefix(text, "sz") || strings.HasPrefix(text, "bj")) {
		return text
	}
	parts := strings.Split(text, ".")
	if len(parts) == 2 && len(parts[0]) == 6 {
		switch parts[1] {
		case "sh", "sz", "bj":
			return parts[1] + parts[0]
		}
	}
	return text
}

func auctionCollectedAtUnix(value string, fallback int64) int64 {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t.Unix()
	}
	return fallback
}

func auctionCollectedAtString(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).In(time.Local).Format(time.RFC3339)
}

// StoreAuctionSnapshot persists one checkpoint snapshot in the auction database.
func StoreAuctionSnapshot(baseDir string, snapshot AuctionSnapshot) error {
	tradeDate, err := normalizeAuctionTradeDate(snapshot.TradeDate)
	if err != nil {
		return err
	}
	snapshotTime, err := normalizeAuctionSnapshotTime(snapshot.SnapshotTime)
	if err != nil {
		return err
	}
	fallbackCollectedAt := auctionCollectedAtUnix(snapshot.CollectedAt, time.Now().Unix())
	rows := make([]any, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		code := normalizeAuctionInstrumentCode(item.InstrumentCode)
		if code == "" {
			continue
		}
		rows = append(rows, &AuctionSnapshotRow{
			TradeDate:       tradeDate,
			SnapshotTime:    snapshotTime,
			InstrumentCode:  code,
			Name:            item.Name,
			AuctionPrice:    item.AuctionPrice,
			AuctionAmount:   item.AuctionAmount,
			PrevClose:       item.PrevClose,
			AuctionPct:      item.AuctionPct,
			Bid1Price:       item.Bid1Price,
			Bid1Volume:      item.Bid1Volume,
			Ask1Price:       item.Ask1Price,
			Ask1Volume:      item.Ask1Volume,
			IsLimitUpOpen:   item.IsLimitUpOpen,
			IsLimitDownOpen: item.IsLimitDownOpen,
			CollectedAt:     auctionCollectedAtUnix(item.CollectedAt, fallbackCollectedAt),
		})
	}

	engine, err := openAuctionEngine(baseDir)
	if err != nil {
		return err
	}
	defer engine.Close()

	const insertBatchSize = 500
	_, err = engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if _, err := session.Table("AuctionSnapshot").Where("TradeDate = ? AND SnapshotTime = ?", tradeDate, snapshotTime).Delete(new(AuctionSnapshotRow)); err != nil {
			return nil, err
		}
		for start := 0; start < len(rows); start += insertBatchSize {
			end := start + insertBatchSize
			if end > len(rows) {
				end = len(rows)
			}
			if _, err := session.Table("AuctionSnapshot").Insert(rows[start:end]...); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// LoadAuctionSnapshot retrieves one stored checkpoint snapshot from the auction database.
func LoadAuctionSnapshot(baseDir string, tradeDate string, snapshotTime string) (*AuctionSnapshot, error) {
	normalizedTradeDate, err := normalizeAuctionTradeDate(tradeDate)
	if err != nil {
		return nil, err
	}
	normalizedSnapshotTime, err := normalizeAuctionSnapshotTime(snapshotTime)
	if err != nil {
		return nil, err
	}
	engine, err := openAuctionEngine(baseDir)
	if err != nil {
		return nil, err
	}
	defer engine.Close()

	rows := make([]AuctionSnapshotRow, 0)
	if err := engine.Table("AuctionSnapshot").
		Where("TradeDate = ? AND SnapshotTime = ?", normalizedTradeDate, normalizedSnapshotTime).
		Asc("InstrumentCode").
		Find(&rows); err != nil {
		return nil, fmt.Errorf("auction load: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	snapshot := AuctionSnapshot{
		TradeDate:    normalizedTradeDate,
		SnapshotTime: normalizedSnapshotTime,
		CollectedAt:  auctionCollectedAtString(rows[0].CollectedAt),
		Items:        make([]AuctionSnapshotItem, 0, len(rows)),
	}
	for _, row := range rows {
		snapshot.Items = append(snapshot.Items, AuctionSnapshotItem{
			InstrumentCode:  row.InstrumentCode,
			Name:            row.Name,
			AuctionPrice:    row.AuctionPrice,
			AuctionAmount:   row.AuctionAmount,
			PrevClose:       row.PrevClose,
			AuctionPct:      row.AuctionPct,
			Bid1Price:       row.Bid1Price,
			Bid1Volume:      row.Bid1Volume,
			Ask1Price:       row.Ask1Price,
			Ask1Volume:      row.Ask1Volume,
			IsLimitUpOpen:   row.IsLimitUpOpen,
			IsLimitDownOpen: row.IsLimitDownOpen,
			CollectedAt:     auctionCollectedAtString(row.CollectedAt),
		})
	}
	return &snapshot, nil
}
