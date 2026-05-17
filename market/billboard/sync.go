package billboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/tdx/eastmoney/rpt"
)

type RPTClient interface {
	QueryAll(context.Context, rpt.Query) ([]map[string]any, error)
}

type SyncerConfig struct {
	Store           *Store
	Client          RPTClient
	Now             func() time.Time
	PageSize        int
	SeatConcurrency int
}

type Syncer struct {
	cfg SyncerConfig
}

type SyncResult struct {
	StartDate           string
	EndDate             string
	EntryCount          int
	SeatTradeCount      int
	InstitutionCount    int
	InstrumentStatCount int
	RawRowCount         int
	FilteredCount       int
	Reports             []string
}

func NewSyncer(cfg SyncerConfig) *Syncer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 5000
	}
	if cfg.SeatConcurrency <= 0 {
		cfg.SeatConcurrency = 3
	}
	return &Syncer{cfg: cfg}
}

func (s *Syncer) SyncDates(ctx context.Context, dates []string) (SyncResult, error) {
	if s == nil || s.cfg.Store == nil || s.cfg.Client == nil {
		return SyncResult{}, fmt.Errorf("market billboard syncer requires store and client")
	}
	dates = normalizeDates(dates)
	if len(dates) == 0 {
		return SyncResult{}, fmt.Errorf("market billboard sync requires at least one date")
	}
	result := SyncResult{
		StartDate: dates[0],
		EndDate:   dates[len(dates)-1],
		Reports:   AllReports(),
	}
	for _, reportName := range AllReports() {
		startedAt := s.cfg.Now()
		rows, err := s.cfg.Client.QueryAll(ctx, s.queryForReport(reportName, result.StartDate, result.EndDate))
		if err != nil {
			for _, date := range statusDatesForReport(reportName, dates, result.EndDate) {
				_ = s.cfg.Store.UpsertSyncStatus(SyncStatusRecord{
					TradeDate:   date,
					ReportName:  reportName,
					Status:      SyncStatusFailed,
					LastError:   err.Error(),
					StartedAt:   startedAt,
					CompletedAt: s.cfg.Now(),
				})
			}
			return result, err
		}
		countByDate, filtered, err := s.processReportRows(ctx, reportName, rows)
		result.FilteredCount += filtered
		if err != nil {
			return result, err
		}
		switch reportName {
		case ReportDailyDetails:
			result.EntryCount += sumCounts(countByDate)
		case ReportBuyDetails, ReportSellDetails:
			result.SeatTradeCount += sumCounts(countByDate)
		case ReportOrganizationTradeDetails:
			result.InstitutionCount += sumCounts(countByDate)
		case ReportTradeAll:
			result.InstrumentStatCount += sumCounts(countByDate)
		}
		result.RawRowCount += sumCounts(countByDate)
		for _, date := range statusDatesForReport(reportName, dates, result.EndDate) {
			rowCount := countByDate[date]
			status := SyncStatusPassed
			if rowCount == 0 {
				status = SyncStatusEmptySuccess
			}
			if err := s.cfg.Store.UpsertSyncStatus(SyncStatusRecord{
				TradeDate:     date,
				ReportName:    reportName,
				Status:        status,
				RowCount:      rowCount,
				FilteredCount: filtered,
				StartedAt:     startedAt,
				CompletedAt:   s.cfg.Now(),
			}); err != nil {
				return result, err
			}
		}
	}
	return result, ctx.Err()
}

func (s *Syncer) queryForReport(reportName, startDate, endDate string) rpt.Query {
	query := rpt.Query{
		ReportName: reportName,
		PageSize:   s.cfg.PageSize,
	}
	if reportName != ReportTradeAll {
		query.Filter = fmt.Sprintf("(TRADE_DATE<='%s')(TRADE_DATE>='%s')", dashedDate(endDate), dashedDate(startDate))
	}
	switch reportName {
	case ReportDailyDetails:
		query.Columns = []string{"ALL"}
		query.SortColumns = []string{"SECURITY_CODE", "TRADE_DATE"}
		query.SortTypes = []string{"1", "-1"}
	default:
		query.Columns = []string{"ALL"}
	}
	return query
}

func (s *Syncer) processReportRows(ctx context.Context, reportName string, rows []map[string]any) (map[string]int, int, error) {
	countByDate := make(map[string]int)
	rankByKey := make(map[string]int)
	filtered := 0
	for index, row := range rows {
		if err := ctx.Err(); err != nil {
			return countByDate, filtered, err
		}
		identity, ok := NormalizeSecuCode(StringField(row, "SECUCODE"))
		if !ok {
			filtered++
			continue
		}
		tradeDate := rowTradeDate(reportName, row)
		if tradeDate == "" {
			tradeDate = time.Now().Format("20060102")
		}
		payloadJSON, payloadHash, err := payload(row)
		if err != nil {
			return countByDate, filtered, err
		}
		sourceRowID := sourceRowID(reportName, row, index)
		if err := s.cfg.Store.UpsertRawRow(RawRowRecord{
			ReportName:        reportName,
			QueryKey:          tradeDate,
			RowKey:            reportName + "|" + sourceRowID,
			TradeDate:         tradeDate,
			FullCode:          identity.FullCode,
			PayloadJSON:       payloadJSON,
			PayloadHash:       payloadHash,
			FetchedAt:         s.cfg.Now(),
			SourceReportName:  reportName,
			SourceRowID:       sourceRowID,
			SourcePayloadHash: payloadHash,
		}); err != nil {
			return countByDate, filtered, err
		}
		switch reportName {
		case ReportDailyDetails:
			if err := s.saveEntry(row, identity, tradeDate, sourceRowID, payloadHash); err != nil {
				return countByDate, filtered, err
			}
		case ReportBuyDetails:
			rankByKey[seatRankKey("buy", identity.FullCode, tradeDate, row)]++
			if err := s.saveSeatTrade(row, identity, tradeDate, sourceRowID, payloadHash, "buy", rankByKey[seatRankKey("buy", identity.FullCode, tradeDate, row)]); err != nil {
				return countByDate, filtered, err
			}
		case ReportSellDetails:
			rankByKey[seatRankKey("sell", identity.FullCode, tradeDate, row)]++
			if err := s.saveSeatTrade(row, identity, tradeDate, sourceRowID, payloadHash, "sell", rankByKey[seatRankKey("sell", identity.FullCode, tradeDate, row)]); err != nil {
				return countByDate, filtered, err
			}
		case ReportOrganizationTradeDetails:
			if err := s.saveInstitution(row, identity, tradeDate, sourceRowID, payloadHash); err != nil {
				return countByDate, filtered, err
			}
		case ReportTradeAll:
			if err := s.saveInstrumentStat(row, identity, tradeDate, sourceRowID, payloadHash); err != nil {
				return countByDate, filtered, err
			}
		}
		countByDate[tradeDate]++
	}
	return countByDate, filtered, nil
}

func (s *Syncer) saveEntry(row map[string]any, identity InstrumentIdentity, tradeDate, sourceRowID, payloadHash string) error {
	explanation := StringField(row, "EXPLANATION")
	changeType := StringField(row, "CHANGE_TYPE")
	tradeID := StringField(row, "TRADE_ID")
	reason := ReasonRecord{
		ReasonText:        explanation,
		ReasonHash:        HashText(explanation),
		SourceChangeType:  changeType,
		SourceExplanation: explanation,
	}
	_, err := s.cfg.Store.UpsertEntry(EntryRecord{
		TradeDate:                tradeDate,
		FullCode:                 identity.FullCode,
		Code:                     identity.Code,
		Exchange:                 identity.Exchange,
		Name:                     StringField(row, "SECURITY_NAME_ABBR"),
		AssetType:                identity.AssetType,
		ClosePriceMilli:          OptionalMilli(ReportDailyDetails, "CLOSE_PRICE", row),
		ChangeRatePct:            OptionalFloat(row, "CHANGE_RATE"),
		BillboardNetAmountMilli:  OptionalMilli(ReportDailyDetails, "BILLBOARD_NET_AMT", row),
		BillboardBuyAmountMilli:  OptionalMilli(ReportDailyDetails, "BILLBOARD_BUY_AMT", row),
		BillboardSellAmountMilli: OptionalMilli(ReportDailyDetails, "BILLBOARD_SELL_AMT", row),
		BillboardDealAmountMilli: OptionalMilli(ReportDailyDetails, "BILLBOARD_DEAL_AMT", row),
		AccumAmountMilli:         OptionalMilli(ReportDailyDetails, "ACCUM_AMOUNT", row),
		DealNetRatioPct:          OptionalFloat(row, "DEAL_NET_RATIO"),
		DealAmountRatioPct:       OptionalFloat(row, "DEAL_AMOUNT_RATIO"),
		TurnoverRatePct:          OptionalFloat(row, "TURNOVERRATE"),
		FreeMarketCapMilli:       OptionalMilli(ReportDailyDetails, "FREE_MARKET_CAP", row),
		ProviderD1CloseAdjPct:    OptionalFloat(row, "D1_CLOSE_ADJCHRATE"),
		ProviderD2CloseAdjPct:    OptionalFloat(row, "D2_CLOSE_ADJCHRATE"),
		ProviderD3CloseAdjPct:    OptionalFloat(row, "D3_CLOSE_ADJCHRATE"),
		ProviderD5CloseAdjPct:    OptionalFloat(row, "D5_CLOSE_ADJCHRATE"),
		ProviderD10CloseAdjPct:   OptionalFloat(row, "D10_CLOSE_ADJCHRATE"),
		SourceReportName:         ReportDailyDetails,
		SourceTradeID:            tradeID,
		SourceChangeType:         changeType,
		SourceSecurityCode:       StringField(row, "SECURITY_CODE"),
		SourceSecuCode:           StringField(row, "SECUCODE"),
		SourceRowID:              sourceRowID,
		SourcePayloadHash:        payloadHash,
		FetchedAt:                s.cfg.Now(),
	}, []ReasonRecord{reason})
	return err
}

func (s *Syncer) saveSeatTrade(row map[string]any, identity InstrumentIdentity, tradeDate, sourceRowID, payloadHash, side string, rank int) error {
	reportName := ReportBuyDetails
	if side == "sell" {
		reportName = ReportSellDetails
	}
	entryID, err := s.cfg.Store.EntryIDByStableKey(entryStableKey(EntryRecord{
		TradeDate:        tradeDate,
		FullCode:         identity.FullCode,
		SourceTradeID:    StringField(row, "TRADE_ID"),
		SourceChangeType: StringField(row, "CHANGE_TYPE"),
		SourceRowID:      sourceRowID,
	}))
	if err != nil {
		return err
	}
	seatType := SeatTypeBrokerage
	if strings.Contains(StringField(row, "OPERATEDEPT_NAME"), "机构专用") {
		seatType = SeatTypeInstitution
	}
	seatID, err := s.cfg.Store.UpsertSeat(SeatRecord{
		SeatName:          StringField(row, "OPERATEDEPT_NAME"),
		SeatType:          seatType,
		SourceSeatCode:    StringField(row, "OPERATEDEPT_CODE"),
		SourceSeatName:    StringField(row, "OPERATEDEPT_NAME"),
		SourceSeatCodeOld: StringField(row, "OPERATEDEPT_CODE_OLD"),
	})
	if err != nil {
		return err
	}
	return s.cfg.Store.UpsertSeatTrade(SeatTradeRecord{
		EntryID:           entryID,
		SeatID:            seatID,
		TradeDate:         tradeDate,
		FullCode:          identity.FullCode,
		Side:              side,
		Rank:              rank,
		BuyAmountMilli:    OptionalMilli(reportName, "BUY", row),
		SellAmountMilli:   OptionalMilli(reportName, "SELL", row),
		NetAmountMilli:    OptionalMilli(reportName, "NET", row),
		AccumAmountMilli:  OptionalMilli(reportName, "ACCUM_AMOUNT", row),
		AccumVolumeShare:  int64(OptionalInt(row, "ACCUM_VOLUME")),
		ChangeRatePct:     OptionalFloat(row, "CHANGE_RATE"),
		ClosePriceMilli:   OptionalMilli(reportName, "CLOSE_PRICE", row),
		BuyRatio:          OptionalFloat(row, "TOTAL_BUYRIO"),
		SellRatio:         OptionalFloat(row, "TOTAL_SELLRIO"),
		SourceReportName:  reportName,
		SourceRowID:       sourceRowID,
		SourcePayloadHash: payloadHash,
		FetchedAt:         s.cfg.Now(),
	})
}

func seatRankKey(side, fullCode, tradeDate string, row map[string]any) string {
	return strings.Join([]string{
		side,
		fullCode,
		tradeDate,
		StringField(row, "TRADE_ID"),
		StringField(row, "CHANGE_TYPE"),
		HashText(StringField(row, "EXPLANATION")),
	}, "|")
}

func (s *Syncer) saveInstitution(row map[string]any, identity InstrumentIdentity, tradeDate, sourceRowID, payloadHash string) error {
	return s.cfg.Store.UpsertInstitutionTrade(InstitutionTradeRecord{
		TradeDate:              tradeDate,
		FullCode:               identity.FullCode,
		Code:                   identity.Code,
		Exchange:               identity.Exchange,
		Name:                   StringField(row, "SECURITY_NAME_ABBR"),
		BuyTimes:               OptionalInt(row, "BUY_TIMES"),
		SellTimes:              OptionalInt(row, "SELL_TIMES"),
		BuyCount:               OptionalInt(row, "BUY_COUNT"),
		SellCount:              OptionalInt(row, "SELL_COUNT"),
		BuyAmountMilli:         OptionalMilli(ReportOrganizationTradeDetails, "BUY_AMT", row),
		SellAmountMilli:        OptionalMilli(ReportOrganizationTradeDetails, "SELL_AMT", row),
		NetBuyAmountMilli:      OptionalMilli(ReportOrganizationTradeDetails, "NET_BUY_AMT", row),
		AccumAmountMilli:       OptionalMilli(ReportOrganizationTradeDetails, "ACCUM_AMOUNT", row),
		RatioPct:               OptionalFloat(row, "RATIO"),
		TurnoverRatePct:        OptionalFloat(row, "TURNOVERRATE"),
		FreeMarketCapMilli:     OptionalMilli(ReportOrganizationTradeDetails, "FREECAP", row),
		ProviderD1CloseAdjPct:  OptionalFloat(row, "D1_CLOSE_ADJCHRATE"),
		ProviderD2CloseAdjPct:  OptionalFloat(row, "D2_CLOSE_ADJCHRATE"),
		ProviderD3CloseAdjPct:  OptionalFloat(row, "D3_CLOSE_ADJCHRATE"),
		ProviderD5CloseAdjPct:  OptionalFloat(row, "D5_CLOSE_ADJCHRATE"),
		ProviderD10CloseAdjPct: OptionalFloat(row, "D10_CLOSE_ADJCHRATE"),
		SourceReportName:       ReportOrganizationTradeDetails,
		SourceRowID:            sourceRowID,
		SourcePayloadHash:      payloadHash,
		FetchedAt:              s.cfg.Now(),
	})
}

func (s *Syncer) saveInstrumentStat(row map[string]any, identity InstrumentIdentity, tradeDate, sourceRowID, payloadHash string) error {
	return s.cfg.Store.UpsertInstrumentStat(InstrumentStatRecord{
		FullCode:                   identity.FullCode,
		Code:                       identity.Code,
		Exchange:                   identity.Exchange,
		Name:                       StringField(row, "SECURITY_NAME_ABBR"),
		StatisticsCycle:            StringField(row, "STATISTICS_CYCLE"),
		PeriodLabel:                StringField(row, "PERIOD"),
		LatestTradeDate:            tradeDate,
		BillboardTimes:             OptionalInt(row, "BILLBOARD_TIMES"),
		BillboardDealAmountMilli:   OptionalMilli(ReportTradeAll, "BILLBOARD_DEAL_AMT", row),
		BillboardBuyAmountMilli:    OptionalMilli(ReportTradeAll, "BILLBOARD_BUY_AMT", row),
		BillboardSellAmountMilli:   OptionalMilli(ReportTradeAll, "BILLBOARD_SELL_AMT", row),
		BillboardNetBuyAmountMilli: OptionalMilli(ReportTradeAll, "BILLBOARD_NET_BUY", row),
		OrgTimes:                   OptionalInt(row, "ORG_TIMES"),
		OrgDealAmountMilli:         OptionalMilli(ReportTradeAll, "ORG_DEAL_AMT", row),
		OrgBuyAmountMilli:          OptionalMilli(ReportTradeAll, "ORG_BUY_AMT", row),
		OrgSellAmountMilli:         OptionalMilli(ReportTradeAll, "ORG_SELL_AMT", row),
		OrgNetBuyAmountMilli:       OptionalMilli(ReportTradeAll, "ORG_NET_BUY", row),
		OrgBuyTimes:                OptionalInt(row, "ORG_BUY_TIMES"),
		OrgSellTimes:               OptionalInt(row, "ORG_SELL_TIMES"),
		InstrumentPct1M:            OptionalFloat(row, "IPCT1M"),
		InstrumentPct3M:            OptionalFloat(row, "IPCT3M"),
		InstrumentPct6M:            OptionalFloat(row, "IPCT6M"),
		InstrumentPct1Y:            OptionalFloat(row, "IPCT1Y"),
		SourceReportName:           ReportTradeAll,
		SourceRowID:                sourceRowID,
		SourcePayloadHash:          payloadHash,
		FetchedAt:                  s.cfg.Now(),
	})
}

func payload(row map[string]any) (string, string, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return "", "", err
	}
	return string(raw), HashText(string(raw)), nil
}

func sourceRowID(reportName string, row map[string]any, index int) string {
	parts := []string{
		ParseEastmoneyDate(firstNonEmpty(StringField(row, "TRADE_DATE"), StringField(row, "LATEST_TDATE"))),
		StringField(row, "SECUCODE"),
		firstNonEmpty(StringField(row, "TRADE_ID"), StringField(row, "STATISTICS_CYCLE")),
		StringField(row, "CHANGE_TYPE"),
		StringField(row, "OPERATEDEPT_CODE"),
		StringField(row, "OPERATEDEPT_CODE_OLD"),
	}
	if explanation := StringField(row, "EXPLANATION"); explanation != "" {
		parts = append(parts, HashText(explanation))
	}
	if seatName := StringField(row, "OPERATEDEPT_NAME"); seatName != "" {
		parts = append(parts, HashText(seatName))
	}
	if anonymousInstitutionSeat(row) {
		parts = append(parts, StringField(row, "BUY"), StringField(row, "SELL"), StringField(row, "NET"))
	}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			clean = append(clean, strings.TrimSpace(part))
		}
	}
	if len(clean) == 0 {
		clean = append(clean, fmt.Sprintf("row:%d", index))
	}
	return reportName + ":" + strings.Join(clean, "|")
}

func anonymousInstitutionSeat(row map[string]any) bool {
	seatCode := strings.TrimSpace(StringField(row, "OPERATEDEPT_CODE"))
	seatName := StringField(row, "OPERATEDEPT_NAME")
	return seatCode == "0" && strings.Contains(seatName, "机构专用")
}

func rowTradeDate(reportName string, row map[string]any) string {
	if reportName == ReportTradeAll {
		return ParseEastmoneyDate(StringField(row, "LATEST_TDATE"))
	}
	return ParseEastmoneyDate(StringField(row, "TRADE_DATE"))
}

func normalizeDates(dates []string) []string {
	seen := make(map[string]struct{}, len(dates))
	out := make([]string, 0, len(dates))
	for _, date := range dates {
		date = ParseEastmoneyDate(date)
		if len(date) != 8 {
			continue
		}
		if _, ok := seen[date]; ok {
			continue
		}
		seen[date] = struct{}{}
		out = append(out, date)
	}
	sort.Strings(out)
	return out
}

func dashedDate(date string) string {
	date = ParseEastmoneyDate(date)
	if len(date) != 8 {
		return date
	}
	return date[:4] + "-" + date[4:6] + "-" + date[6:8]
}

func statusDatesForReport(reportName string, dates []string, endDate string) []string {
	if reportName == ReportTradeAll {
		return []string{endDate}
	}
	return dates
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
