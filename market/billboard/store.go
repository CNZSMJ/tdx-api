package billboard

import (
	"encoding/base64"
	"encoding/json"
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

const DefaultDBName = "market_billboard.db"

type Store struct {
	engine *xorm.Engine
}

func DefaultDBPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "./data/database"
	}
	return filepath.Join(baseDir, DefaultDBName)
}

func OpenStore(filename string) (*Store, error) {
	if strings.TrimSpace(filename) == "" {
		filename = DefaultDBPath("")
	}
	if dir := filepath.Dir(filename); dir != "." && dir != "" {
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
	return s.engine.Sync2(
		new(RawRowRecord),
		new(EntryRecord),
		new(ReasonRecord),
		new(EntryReasonRecord),
		new(SeatRecord),
		new(SeatTradeRecord),
		new(InstitutionTradeRecord),
		new(InstrumentStatRecord),
		new(SyncStatusRecord),
	)
}

func (s *Store) UpsertRawRow(record RawRowRecord) error {
	if record.RowKey == "" {
		return fmt.Errorf("raw row requires row key")
	}
	if record.FetchedAt.IsZero() {
		record.FetchedAt = time.Now()
	}
	if record.SourceReportName == "" {
		record.SourceReportName = record.ReportName
	}
	if record.SourcePayloadHash == "" {
		record.SourcePayloadHash = record.PayloadHash
	}
	if record.SourceRowID == "" {
		record.SourceRowID = record.RowKey
	}
	existing := new(RawRowRecord)
	has, err := s.engine.Where("RowKey = ?", record.RowKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return err
	}
	_, err = s.engine.Insert(&record)
	return err
}

func (s *Store) UpsertEntry(record EntryRecord, reasons []ReasonRecord) (int64, error) {
	if record.StableKey == "" {
		record.StableKey = entryStableKey(record)
	}
	if record.StableKey == "" {
		return 0, fmt.Errorf("entry requires stable key")
	}
	existing := new(EntryRecord)
	has, err := s.engine.Where("StableKey = ?", record.StableKey).Get(existing)
	if err != nil {
		return 0, err
	}
	if has {
		record.ID = existing.ID
		if _, err := s.engine.ID(existing.ID).AllCols().Update(&record); err != nil {
			return 0, err
		}
	} else {
		if _, err := s.engine.Insert(&record); err != nil {
			return 0, err
		}
	}
	id := record.ID
	if id == 0 {
		current := new(EntryRecord)
		if has, err := s.engine.Where("StableKey = ?", record.StableKey).Get(current); err != nil {
			return 0, err
		} else if has {
			id = current.ID
		}
	}
	for _, reason := range reasons {
		reasonID, err := s.UpsertReason(reason)
		if err != nil {
			return id, err
		}
		if err := s.linkEntryReason(id, reasonID); err != nil {
			return id, err
		}
	}
	return id, nil
}

func (s *Store) UpsertReason(record ReasonRecord) (int64, error) {
	if strings.TrimSpace(record.ReasonText) == "" {
		return 0, nil
	}
	if record.ReasonHash == "" {
		record.ReasonHash = HashText(record.ReasonText)
	}
	existing := new(ReasonRecord)
	has, err := s.engine.Where("ReasonHash = ?", record.ReasonHash).Get(existing)
	if err != nil {
		return 0, err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return existing.ID, err
	}
	if _, err := s.engine.Insert(&record); err != nil {
		return 0, err
	}
	current := new(ReasonRecord)
	has, err = s.engine.Where("ReasonHash = ?", record.ReasonHash).Get(current)
	if err != nil || !has {
		return 0, err
	}
	return current.ID, nil
}

func (s *Store) linkEntryReason(entryID, reasonID int64) error {
	if entryID == 0 || reasonID == 0 {
		return nil
	}
	link := &EntryReasonRecord{
		EntryID:  entryID,
		ReasonID: reasonID,
		LinkKey:  fmt.Sprintf("%d:%d", entryID, reasonID),
	}
	has, err := s.engine.Where("LinkKey = ?", link.LinkKey).Exist(new(EntryReasonRecord))
	if err != nil || has {
		return err
	}
	_, err = s.engine.Insert(link)
	return err
}

func (s *Store) UpsertSeat(record SeatRecord) (int64, error) {
	if record.SeatType == "" {
		record.SeatType = SeatTypeUnknown
	}
	if record.SeatName == "" {
		record.SeatName = record.SourceSeatName
	}
	if record.SeatKey == "" {
		record.SeatKey = seatKey(record)
	}
	if record.SeatKey == "" {
		return 0, fmt.Errorf("seat requires name or source code")
	}
	existing := new(SeatRecord)
	has, err := s.engine.Where("SeatKey = ?", record.SeatKey).Get(existing)
	if err != nil {
		return 0, err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return existing.ID, err
	}
	if _, err := s.engine.Insert(&record); err != nil {
		return 0, err
	}
	current := new(SeatRecord)
	has, err = s.engine.Where("SeatKey = ?", record.SeatKey).Get(current)
	if err != nil || !has {
		return 0, err
	}
	return current.ID, nil
}

func (s *Store) UpsertSeatTrade(record SeatTradeRecord) error {
	if record.StableKey == "" {
		record.StableKey = seatTradeStableKey(record)
	}
	if record.StableKey == "" {
		return fmt.Errorf("seat trade requires stable key")
	}
	existing := new(SeatTradeRecord)
	has, err := s.engine.Where("StableKey = ?", record.StableKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return err
	}
	_, err = s.engine.Insert(&record)
	return err
}

func (s *Store) UpsertInstitutionTrade(record InstitutionTradeRecord) error {
	if record.StableKey == "" {
		record.StableKey = institutionStableKey(record)
	}
	existing := new(InstitutionTradeRecord)
	has, err := s.engine.Where("StableKey = ?", record.StableKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return err
	}
	_, err = s.engine.Insert(&record)
	return err
}

func (s *Store) UpsertInstrumentStat(record InstrumentStatRecord) error {
	if record.StableKey == "" {
		record.StableKey = instrumentStatStableKey(record)
	}
	existing := new(InstrumentStatRecord)
	has, err := s.engine.Where("StableKey = ?", record.StableKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return err
	}
	_, err = s.engine.Insert(&record)
	return err
}

func (s *Store) UpsertSyncStatus(record SyncStatusRecord) error {
	if record.StatusKey == "" {
		record.StatusKey = syncStatusKey(record.TradeDate, record.ReportName)
	}
	if record.StatusKey == "" {
		return fmt.Errorf("sync status requires trade date and report name")
	}
	existing := new(SyncStatusRecord)
	has, err := s.engine.Where("StatusKey = ?", record.StatusKey).Get(existing)
	if err != nil {
		return err
	}
	if has {
		record.ID = existing.ID
		_, err = s.engine.ID(existing.ID).AllCols().Update(&record)
		return err
	}
	_, err = s.engine.Insert(&record)
	return err
}

func (s *Store) ListEntries(query EntryQuery) (EntryList, error) {
	limit := normalizeLimit(query.Limit)
	rows := make([]EntryRecord, 0, limit+1)
	session := s.engine.Desc("TradeDate").Desc("ID").Limit(limit + 1)
	if query.StartDate != "" {
		session = session.And("TradeDate >= ?", query.StartDate)
	}
	if query.EndDate != "" {
		session = session.And("TradeDate <= ?", query.EndDate)
	}
	if query.FullCode != "" {
		session = session.And("FullCode = ?", query.FullCode)
	}
	if id := decodeCursorID(query.Cursor); id > 0 {
		session = session.And("ID < ?", id)
	}
	if err := session.Find(&rows); err != nil {
		return EntryList{}, err
	}
	next := ""
	if len(rows) > limit {
		next = encodeCursorID(rows[limit-1].ID)
		rows = rows[:limit]
	}
	return EntryList{Items: rows, NextCursor: next}, nil
}

func (s *Store) ListSeatTrades(query SeatTradeQuery) (SeatTradeList, error) {
	limit := normalizeLimit(query.Limit)
	rows := make([]SeatTradeRecord, 0, limit+1)
	session := s.engine.Desc("TradeDate").Desc("ID").Limit(limit + 1)
	seatByID := make(map[int64]SeatRecord)
	if query.Keyword != "" {
		matchingSeats := make([]SeatRecord, 0, 16)
		if err := s.engine.Where("SeatName LIKE ?", "%"+query.Keyword+"%").Find(&matchingSeats); err != nil {
			return SeatTradeList{}, err
		}
		if len(matchingSeats) == 0 {
			return SeatTradeList{}, nil
		}
		seatIDs := make([]int64, 0, len(matchingSeats))
		for _, seat := range matchingSeats {
			seatIDs = append(seatIDs, seat.ID)
			seatByID[seat.ID] = seat
		}
		session = session.In("SeatID", seatIDs)
	}
	if query.TradeDate != "" {
		session = session.And("TradeDate = ?", query.TradeDate)
	}
	if query.FullCode != "" {
		session = session.And("FullCode = ?", query.FullCode)
	}
	if query.Side != "" {
		session = session.And("Side = ?", query.Side)
	}
	if id := decodeCursorID(query.Cursor); id > 0 {
		session = session.And("ID < ?", id)
	}
	if err := session.Find(&rows); err != nil {
		return SeatTradeList{}, err
	}
	next := ""
	if len(rows) > limit {
		next = encodeCursorID(rows[limit-1].ID)
		rows = rows[:limit]
	}
	if len(seatByID) == 0 {
		seatIDs := make([]int64, 0, len(rows))
		for _, row := range rows {
			seatIDs = append(seatIDs, row.SeatID)
		}
		seats, err := s.seatsByID(seatIDs)
		if err != nil {
			return SeatTradeList{}, err
		}
		seatByID = seats
	}
	items := make([]SeatTradeView, 0, len(rows))
	for _, row := range rows {
		seat := seatByID[row.SeatID]
		items = append(items, SeatTradeView{
			SeatTradeRecord: row,
			SeatName:        seat.SeatName,
			SeatType:        seat.SeatType,
			SourceSeatCode:  seat.SourceSeatCode,
		})
	}
	return SeatTradeList{Items: items, NextCursor: next}, nil
}

func (s *Store) seatsByID(ids []int64) (map[int64]SeatRecord, error) {
	if len(ids) == 0 {
		return map[int64]SeatRecord{}, nil
	}
	seats := make([]SeatRecord, 0, len(ids))
	if err := s.engine.In("ID", ids).Find(&seats); err != nil {
		return nil, err
	}
	byID := make(map[int64]SeatRecord, len(seats))
	for _, seat := range seats {
		byID[seat.ID] = seat
	}
	return byID, nil
}

func (s *Store) ListInstitutions(query InstitutionQuery) (InstitutionList, error) {
	limit := normalizeLimit(query.Limit)
	rows := make([]InstitutionTradeRecord, 0, limit+1)
	session := s.engine.Desc("TradeDate").Desc("ID").Limit(limit + 1)
	if query.StartDate != "" {
		session = session.And("TradeDate >= ?", query.StartDate)
	}
	if query.EndDate != "" {
		session = session.And("TradeDate <= ?", query.EndDate)
	}
	if query.FullCode != "" {
		session = session.And("FullCode = ?", query.FullCode)
	}
	if id := decodeCursorID(query.Cursor); id > 0 {
		session = session.And("ID < ?", id)
	}
	if err := session.Find(&rows); err != nil {
		return InstitutionList{}, err
	}
	next := ""
	if len(rows) > limit {
		next = encodeCursorID(rows[limit-1].ID)
		rows = rows[:limit]
	}
	return InstitutionList{Items: rows, NextCursor: next}, nil
}

func (s *Store) ListInstrumentStats(query InstrumentStatQuery) (InstrumentStatList, error) {
	limit := normalizeLimit(query.Limit)
	rows := make([]InstrumentStatRecord, 0, limit+1)
	session := s.engine.Desc("LatestTradeDate").Desc("ID").Limit(limit + 1)
	if query.FullCode != "" {
		session = session.And("FullCode = ?", query.FullCode)
	}
	if query.Cycle != "" {
		session = session.And("StatisticsCycle = ?", query.Cycle)
	}
	if id := decodeCursorID(query.Cursor); id > 0 {
		session = session.And("ID < ?", id)
	}
	if err := session.Find(&rows); err != nil {
		return InstrumentStatList{}, err
	}
	next := ""
	if len(rows) > limit {
		next = encodeCursorID(rows[limit-1].ID)
		rows = rows[:limit]
	}
	return InstrumentStatList{Items: rows, NextCursor: next}, nil
}

func (s *Store) Coverage(startDate, endDate string, reports []string) (Freshness, error) {
	startDate = strings.TrimSpace(startDate)
	endDate = strings.TrimSpace(endDate)
	if endDate == "" {
		endDate = startDate
	}
	if startDate == "" {
		startDate = endDate
	}
	if len(reports) == 0 {
		reports = CoreReports()
	}
	statuses := make([]SyncStatusRecord, 0, 16)
	session := s.engine.Where("TradeDate >= ? AND TradeDate <= ?", startDate, endDate).In("ReportName", reports)
	if err := session.Find(&statuses); err != nil {
		return Freshness{}, err
	}
	statusByKey := make(map[string]SyncStatusRecord, len(statuses))
	watermark := ""
	lastErr := ""
	for _, status := range statuses {
		statusByKey[syncStatusKey(status.TradeDate, status.ReportName)] = status
		if status.Status == SyncStatusPassed || status.Status == SyncStatusEmptySuccess {
			if status.TradeDate > watermark {
				watermark = status.TradeDate
			}
		}
		if status.LastError != "" {
			lastErr = status.LastError
		}
	}
	requiredDates := datesBetween(startDate, endDate)
	if len(requiredDates) == 0 && startDate != "" {
		requiredDates = []string{startDate}
	}
	var total, passed, empty, failed, missing int
	for _, date := range requiredDates {
		for _, report := range reports {
			total++
			status, ok := statusByKey[syncStatusKey(date, report)]
			if !ok {
				missing++
				continue
			}
			switch status.Status {
			case SyncStatusPassed:
				passed++
			case SyncStatusEmptySuccess:
				empty++
			case SyncStatusFailed:
				failed++
			default:
				missing++
			}
		}
	}
	coverage := CoverageComplete
	status := "fresh"
	switch {
	case failed > 0:
		coverage = CoverageFailed
		status = "stale"
	case missing == total:
		coverage = CoverageMissing
		status = "stale"
	case missing > 0:
		coverage = CoveragePartial
		status = "stale"
	case passed == 0 && empty == total:
		coverage = CoverageEmptySuccess
	default:
		coverage = CoverageComplete
	}
	return Freshness{
		Domain:         DomainMarketBillboard,
		Watermark:      watermark,
		QueryStartDate: startDate,
		QueryEndDate:   endDate,
		Coverage:       coverage,
		Status:         status,
		ErrorSummary:   lastErr,
		Reports:        append([]string(nil), reports...),
	}, nil
}

func (s *Store) LatestWatermark() (string, error) {
	return s.LatestWatermarkForReports(nil)
}

func (s *Store) LatestWatermarkForReports(reports []string) (string, error) {
	statuses := make([]SyncStatusRecord, 0, 1)
	session := s.engine.In("Status", []string{SyncStatusPassed, SyncStatusEmptySuccess}).Desc("TradeDate").Limit(1)
	if len(reports) > 0 {
		session = session.In("ReportName", reports)
	}
	err := session.Find(&statuses)
	if err != nil || len(statuses) == 0 {
		return "", err
	}
	return statuses[0].TradeDate, nil
}

func (s *Store) Counts() (entries, seats, institutions, stats int64, err error) {
	if entries, err = s.engine.Count(new(EntryRecord)); err != nil {
		return
	}
	if seats, err = s.engine.Count(new(SeatTradeRecord)); err != nil {
		return
	}
	if institutions, err = s.engine.Count(new(InstitutionTradeRecord)); err != nil {
		return
	}
	stats, err = s.engine.Count(new(InstrumentStatRecord))
	return
}

func (s *Store) CountRawRows() (int64, error) {
	return s.engine.Count(new(RawRowRecord))
}

func (s *Store) EntryIDByStableKey(stableKey string) (int64, error) {
	if strings.TrimSpace(stableKey) == "" {
		return 0, nil
	}
	entry := new(EntryRecord)
	has, err := s.engine.Where("StableKey = ?", stableKey).Get(entry)
	if err != nil || !has {
		return 0, err
	}
	return entry.ID, nil
}

func CoreReports() []string {
	return []string{ReportDailyDetails, ReportBuyDetails, ReportSellDetails, ReportOrganizationTradeDetails}
}

func AllReports() []string {
	return []string{ReportDailyDetails, ReportBuyDetails, ReportSellDetails, ReportOrganizationTradeDetails, ReportTradeAll}
}

func entryStableKey(record EntryRecord) string {
	reasonIdentity := record.SourceTradeID
	if reasonIdentity == "" {
		reasonIdentity = record.SourceChangeType
	}
	if reasonIdentity == "" {
		reasonIdentity = record.SourceRowID
	}
	if reasonIdentity == "" {
		reasonIdentity = record.SourcePayloadHash
	}
	if record.TradeDate == "" || record.FullCode == "" || reasonIdentity == "" {
		return ""
	}
	return strings.Join([]string{record.TradeDate, record.FullCode, reasonIdentity}, "|")
}

func seatKey(record SeatRecord) string {
	if record.SourceSeatCode != "" {
		return record.SeatType + "|code|" + record.SourceSeatCode
	}
	if record.SeatName != "" {
		return record.SeatType + "|name|" + HashText(record.SeatName)
	}
	return ""
}

func seatTradeStableKey(record SeatTradeRecord) string {
	if record.SourceRowID != "" {
		return strings.Join([]string{record.SourceReportName, record.SourceRowID, record.Side}, "|")
	}
	return fmt.Sprintf("%d|%d|%s|%d", record.EntryID, record.SeatID, record.Side, record.Rank)
}

func institutionStableKey(record InstitutionTradeRecord) string {
	if record.SourceRowID != "" {
		return record.SourceReportName + "|" + record.SourceRowID
	}
	return strings.Join([]string{record.TradeDate, record.FullCode, HashText(record.SourcePayloadHash)}, "|")
}

func instrumentStatStableKey(record InstrumentStatRecord) string {
	return strings.Join([]string{record.FullCode, record.StatisticsCycle, record.PeriodLabel}, "|")
}

func syncStatusKey(tradeDate, reportName string) string {
	if tradeDate == "" || reportName == "" {
		return ""
	}
	return tradeDate + "|" + reportName
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func encodeCursorID(id int64) string {
	if id <= 0 {
		return ""
	}
	payload, _ := json.Marshal(map[string]int64{"id": id})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursorID(cursor string) int64 {
	if strings.TrimSpace(cursor) == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0
	}
	return payload.ID
}

func datesBetween(startDate, endDate string) []string {
	start, err := time.ParseInLocation("20060102", startDate, time.Local)
	if err != nil {
		return nil
	}
	end, err := time.ParseInLocation("20060102", endDate, time.Local)
	if err != nil {
		return nil
	}
	if end.Before(start) {
		start, end = end, start
	}
	dates := make([]string, 0, 8)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("20060102"))
	}
	sort.Strings(dates)
	return dates
}
