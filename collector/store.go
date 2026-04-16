package collector

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/tdx/internal/appenv"
	"xorm.io/core"
	"xorm.io/xorm"
)

const DefaultDBName = "collector.db"

var DefaultBaseDir = resolveDefaultBaseDir()

func resolveDefaultBaseDir() string {
	return appenv.ResolveTDXDataDir("./data/database")
}

func DefaultDBPath(baseDir string) string {
	if baseDir == "" {
		baseDir = DefaultBaseDir
	}
	return filepath.Join(baseDir, DefaultDBName)
}

type Store struct {
	engine *xorm.Engine
}

func OpenStore(filename string) (*Store, error) {
	if filename == "" {
		filename = DefaultDBPath("")
	}
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

	store := &Store{engine: engine}
	if err := store.EnsureSchema(); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.Close()
}

func (s *Store) EnsureSchema() error {
	if err := s.engine.Sync2(
		new(SchemaVersion),
		new(PhaseStateRecord),
		new(TaskRunRecord),
		new(ValidationRunRecord),
		new(OperationLogRecord),
		new(CollectCursorRecord),
		new(CollectGapRecord),
		new(ScheduleRunRecord),
	); err != nil {
		return err
	}

	has, err := s.engine.Where("Version = ?", SchemaVersionCurrent).Exist(new(SchemaVersion))
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	_, err = s.engine.Insert(&SchemaVersion{
		Version:   SchemaVersionCurrent,
		AppliedAt: time.Now(),
	})
	return err
}

func (s *Store) HasTable(bean interface{}) (bool, error) {
	return s.engine.IsTableExist(bean)
}

func (s *Store) UpsertPhaseState(record *PhaseStateRecord) error {
	record.UpdatedAt = time.Now()
	has, err := s.engine.Where("PhaseID = ?", record.PhaseID).Exist(new(PhaseStateRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where("PhaseID = ?", record.PhaseID).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *Store) AddValidationRun(record *ValidationRunRecord) error {
	record.CreatedAt = time.Now()
	_, err := s.engine.Insert(record)
	return err
}

func (s *Store) AddOperationLog(record *OperationLogRecord) error {
	record.CreatedAt = time.Now()
	_, err := s.engine.Insert(record)
	return err
}

func (s *Store) AddScheduleRun(record *ScheduleRunRecord) error {
	_, err := s.engine.Insert(record)
	return err
}

func (s *Store) UpdateScheduleRun(record *ScheduleRunRecord) error {
	_, err := s.engine.ID(record.ID).AllCols().Update(record)
	return err
}

func appendScheduleRunDetails(existing, extra string) string {
	existing = strings.TrimSpace(existing)
	extra = strings.TrimSpace(extra)
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + " | " + extra
}

func (s *Store) InterruptRunningScheduleRuns(scheduleName, reason string, endedAt time.Time) (int64, error) {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}

	records := make([]ScheduleRunRecord, 0, 8)
	session := s.engine.Where("Status = ?", "running")
	if strings.TrimSpace(scheduleName) != "" {
		session = session.And("ScheduleName = ?", scheduleName)
	}
	if err := session.Find(&records); err != nil {
		return 0, err
	}

	var updated int64
	for i := range records {
		record := records[i]
		record.Status = "interrupted"
		record.EndedAt = endedAt
		record.Details = appendScheduleRunDetails(record.Details, reason)
		if _, err := s.engine.ID(record.ID).Cols("Status", "EndedAt", "Details").Update(&record); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func (s *Store) LatestScheduleRun(scheduleName string, statuses ...string) (*ScheduleRunRecord, error) {
	record := new(ScheduleRunRecord)
	session := s.engine.Where("ScheduleName = ?", scheduleName)
	if len(statuses) > 0 {
		session = session.In("Status", statuses)
	}
	has, err := session.Desc("StartedAt").Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *Store) ListRecentScheduleRuns(limit int) ([]ScheduleRunRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	records := make([]ScheduleRunRecord, 0, limit)
	err := s.engine.Desc("StartedAt").Limit(limit).Find(&records)
	return records, err
}

func (s *Store) HasScheduleRunInWindow(scheduleName string, windowStart, windowEnd time.Time, statuses ...string) (bool, error) {
	session := s.engine.Where("ScheduleName = ? AND StartedAt >= ? AND StartedAt < ?", scheduleName, windowStart, windowEnd)
	if len(statuses) > 0 {
		session = session.In("Status", statuses)
	}
	return session.Exist(new(ScheduleRunRecord))
}

func (s *Store) HasScheduleRunWithDetails(scheduleName, detailsNeedle string, statuses ...string) (bool, error) {
	session := s.engine.Where("ScheduleName = ? AND Details LIKE ?", scheduleName, "%"+detailsNeedle+"%")
	if len(statuses) > 0 {
		session = session.In("Status", statuses)
	}
	return session.Exist(new(ScheduleRunRecord))
}

func (s *Store) CountOpenCollectGaps() (int64, error) {
	return s.engine.Where("Status = ?", "open").Count(new(CollectGapRecord))
}

func (s *Store) GetCollectCursor(domain, assetType, instrument, period string) (*CollectCursorRecord, error) {
	record := new(CollectCursorRecord)
	has, err := s.engine.Where("Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?", domain, assetType, instrument, period).Get(record)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return record, nil
}

func (s *Store) UpsertCollectCursor(record *CollectCursorRecord) error {
	record.UpdatedAt = time.Now()
	has, err := s.engine.Where("Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?", record.Domain, record.AssetType, record.Instrument, record.Period).Exist(new(CollectCursorRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where("Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ?", record.Domain, record.AssetType, record.Instrument, record.Period).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *Store) SeedTradeHistoryCoverageStarts(defaultCursor string) (int64, error) {
	return s.seedCoverageStarts(tradeHistoryDomain, tradeHistoryCoverageStartDomain, defaultCursor)
}

func (s *Store) SeedLiveCaptureCoverageStarts(defaultCursor string) (int64, error) {
	return s.seedCoverageStarts(liveCaptureDomain, liveCaptureCoverageStartDomain, defaultCursor)
}

func (s *Store) seedCoverageStarts(domain, coverageDomain, defaultCursor string) (int64, error) {
	defaultCursor = strings.TrimSpace(defaultCursor)
	if defaultCursor == "" {
		return 0, nil
	}

	latest := make([]CollectCursorRecord, 0)
	if err := s.engine.Where("Domain = ? AND Cursor <> ''", domain).Find(&latest); err != nil {
		return 0, err
	}
	if len(latest) == 0 {
		return 0, nil
	}

	existing := make([]CollectCursorRecord, 0)
	if err := s.engine.Where("Domain = ? AND Cursor <> ''", coverageDomain).Find(&existing); err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		seen[collectCursorKey(item.Domain, item.AssetType, item.Instrument, item.Period)] = struct{}{}
	}

	var inserted int64
	for _, item := range latest {
		key := collectCursorKey(coverageDomain, item.AssetType, item.Instrument, item.Period)
		if _, ok := seen[key]; ok {
			continue
		}
		cursorValue := defaultCursor
		if tradeDateAfter(defaultCursor, item.Cursor) {
			cursorValue = item.Cursor
		}
		if err := s.UpsertCollectCursor(&CollectCursorRecord{
			Domain:     coverageDomain,
			AssetType:  item.AssetType,
			Instrument: item.Instrument,
			Period:     item.Period,
			Cursor:     cursorValue,
		}); err != nil {
			return inserted, err
		}
		seen[key] = struct{}{}
		inserted++
	}
	return inserted, nil
}

func collectCursorKey(domain, assetType, instrument, period string) string {
	return domain + "|" + assetType + "|" + instrument + "|" + period
}

func (s *Store) UpsertCollectGap(record *CollectGapRecord) error {
	now := time.Now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	has, err := s.engine.Where(
		"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ? AND StartKey = ? AND EndKey = ?",
		record.Domain, record.AssetType, record.Instrument, record.Period, record.StartKey, record.EndKey,
	).Exist(new(CollectGapRecord))
	if err != nil {
		return err
	}
	if has {
		_, err = s.engine.Where(
			"Domain = ? AND AssetType = ? AND Instrument = ? AND Period = ? AND StartKey = ? AND EndKey = ?",
			record.Domain, record.AssetType, record.Instrument, record.Period, record.StartKey, record.EndKey,
		).AllCols().Update(record)
		return err
	}
	_, err = s.engine.Insert(record)
	return err
}

func (s *Store) ListOpenCollectGaps(domain, assetType, instrument, period string) ([]CollectGapRecord, error) {
	records := make([]CollectGapRecord, 0, 8)
	session := s.engine.Where("Status = ?", "open")
	if domain != "" {
		session = session.And("Domain = ?", domain)
	}
	if assetType != "" {
		session = session.And("AssetType = ?", assetType)
	}
	if instrument != "" {
		session = session.And("Instrument = ?", instrument)
	}
	if period != "" {
		session = session.And("Period = ?", period)
	}
	if err := session.Asc("CreatedAt", "ID").Find(&records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) UpdateCollectGap(record *CollectGapRecord) error {
	if record == nil {
		return nil
	}
	record.UpdatedAt = time.Now()
	_, err := s.engine.ID(record.ID).AllCols().Update(record)
	return err
}

func (s *Store) CloseCollectGap(id int64, reason string) error {
	record := &CollectGapRecord{
		Status:    "closed",
		Reason:    reason,
		UpdatedAt: time.Now(),
	}
	_, err := s.engine.ID(id).Cols("Status", "Reason", "UpdatedAt").Update(record)
	return err
}
