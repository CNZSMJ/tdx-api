package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/eastmoney/rpt"
	systemgov "github.com/injoyai/tdx/governance"
	"github.com/injoyai/tdx/market/billboard"
)

var marketBillboardStore *billboard.Store

func collectorMarketBillboardSyncSchedule() string {
	if !lifecycleEnvBoolDefault("TDX_MARKET_BILLBOARD_ENABLE", true) {
		return ""
	}
	if schedule := strings.TrimSpace(os.Getenv("TDX_MARKET_BILLBOARD_SCHEDULE")); schedule != "" {
		return schedule
	}
	return "0 30 21 * * *"
}

func initMarketBillboardStore() {
	if marketBillboardStore != nil {
		return
	}
	store, err := billboard.OpenStore(billboard.DefaultDBPath(databaseDir))
	if err != nil {
		log.Printf("初始化 market_billboard store 失败: %v", err)
		return
	}
	marketBillboardStore = store
}

func shutdownMarketBillboardStore() {
	if marketBillboardStore == nil {
		return
	}
	if err := marketBillboardStore.Close(); err != nil {
		log.Printf("market_billboard store close failed: %v", err)
	}
	marketBillboardStore = nil
}

func initMarketBillboardSyncRunner() {
	marketBillboardSync = nil
	if governanceStore == nil || collectorRuntime == nil || marketBillboardStore == nil || !lifecycleEnvBoolDefault("TDX_MARKET_BILLBOARD_ENABLE", true) {
		return
	}
	runner, err := systemgov.NewMarketBillboardSyncRunner(systemgov.MarketBillboardSyncConfig{
		Store:           governanceStore,
		Paths:           governancePaths,
		Now:             time.Now,
		UseExternalLock: true,
		ResolveTargetDates: func(ctx context.Context, now time.Time, count int) ([]string, error) {
			return collectorRuntime.ResolveRecentTradingDatesAt(ctx, now, count)
		},
		Execute: func(ctx context.Context, dates []string) (billboard.SyncResult, error) {
			return executeMarketBillboardSync(ctx, dates)
		},
	})
	if err != nil {
		log.Printf("初始化 market_billboard_sync runner 失败: %v", err)
		return
	}
	marketBillboardSync = runner
}

func runMarketBillboardSyncWithDates(trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	if isServiceShuttingDown() {
		return nil, fmt.Errorf("service shutdown in progress, skip market_billboard_sync: trigger=%s", trigger)
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectorGeneralRunTimeout())
	endRun := governanceActiveRun.begin("market_billboard_sync", cancel)
	defer func() {
		cancel()
		endRun()
	}()
	return runMarketBillboardSyncWithDatesContext(ctx, trigger, targetDates)
}

func runMarketBillboardSyncWithDatesContext(ctx context.Context, trigger string, targetDates []string) (*collectorpkg.GovernanceRunRecord, error) {
	if marketBillboardSync == nil {
		return nil, fmt.Errorf("market_billboard_sync runner 未初始化")
	}
	var (
		run *collectorpkg.GovernanceRunRecord
		err error
	)
	if len(targetDates) > 0 {
		run, err = marketBillboardSync.RunWithDates(ctx, trigger, targetDates)
	} else {
		run, err = marketBillboardSync.Run(ctx, trigger)
	}
	if err != nil {
		return run, err
	}
	log.Printf("market_billboard_sync 完成: trigger=%s status=%s target=%s", trigger, run.Status, run.TargetWindow)
	return run, nil
}

func executeMarketBillboardSync(ctx context.Context, dates []string) (billboard.SyncResult, error) {
	if marketBillboardStore == nil {
		return billboard.SyncResult{}, fmt.Errorf("market_billboard store 未初始化")
	}
	syncer := billboard.NewSyncer(billboard.SyncerConfig{
		Store:           marketBillboardStore,
		Client:          rpt.NewClient(rpt.ClientConfig{PageSize: 5000, RetryCount: 3, Timeout: 10 * time.Second}),
		PageSize:        5000,
		SeatConcurrency: 3,
	})
	return syncer.SyncDates(ctx, dates)
}

func projectMarketBillboardGovernanceDomain() error {
	if governanceStore == nil || marketBillboardStore == nil {
		return nil
	}
	now := time.Now()
	watermark, err := marketBillboardStore.LatestWatermark()
	if err != nil {
		return err
	}
	entries, seats, institutions, stats, err := marketBillboardStore.Counts()
	if err != nil {
		return err
	}
	status := "healthy"
	freshness := "fresh"
	coverage := "covered"
	if watermark == "" {
		status = "missing"
		freshness = "stale"
		coverage = "unknown"
	}
	return governanceStore.UpsertDomainHealthSnapshot(&collectorpkg.DomainHealthSnapshotRecord{
		Domain:          billboard.DomainMarketBillboard,
		Status:          status,
		Freshness:       freshness,
		Coverage:        coverage,
		LatestCursor:    watermark,
		LatestWatermark: watermark,
		Summary:         fmt.Sprintf("entries=%d seat_trades=%d institutions=%d instrument_stats=%d db=%s", entries, seats, institutions, stats, filepath.Base(billboard.DefaultDBPath(databaseDir))),
		SnapshotAt:      now,
	})
}

func handleMarketBillboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	query := r.URL.Query()
	startDate, endDate := billboardDateRange(query.Get("trade_date"), query.Get("start_date"), query.Get("end_date"))
	list, freshness, err := listBillboardEntries(billboard.EntryQuery{
		StartDate: startDate,
		EndDate:   endDate,
		Limit:     parsePositiveInt(query.Get("limit")),
		Cursor:    strings.TrimSpace(query.Get("cursor")),
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	successResponse(w, billboardListResponse(list, freshness, billboard.CoreReports(), includeSource(r)))
}

func handleMarketBillboardInstrument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	fullCode := strings.TrimSpace(r.URL.Query().Get("full_code"))
	if fullCode == "" {
		errorResponse(w, "full_code 为必填参数")
		return
	}
	list, freshness, err := listBillboardEntries(billboard.EntryQuery{
		FullCode: fullCode,
		Limit:    parsePositiveInt(r.URL.Query().Get("limit")),
		Cursor:   strings.TrimSpace(r.URL.Query().Get("cursor")),
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	successResponse(w, billboardListResponse(list, freshness, billboard.CoreReports(), includeSource(r)))
}

func handleMarketBillboardDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	fullCode := strings.TrimSpace(r.URL.Query().Get("full_code"))
	tradeDate := billboard.ParseEastmoneyDate(r.URL.Query().Get("trade_date"))
	if fullCode == "" || tradeDate == "" {
		errorResponse(w, "full_code 与 trade_date 为必填参数")
		return
	}
	entries, freshness, err := listBillboardEntries(billboard.EntryQuery{FullCode: fullCode, StartDate: tradeDate, EndDate: tradeDate, Limit: 100})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	store := requireMarketBillboardStore(w)
	if store == nil {
		return
	}
	seats, err := store.ListSeatTrades(billboard.SeatTradeQuery{FullCode: fullCode, TradeDate: tradeDate, Limit: 100})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	sortSeatTradesByRank(seats.Items)
	institutions, err := store.ListInstitutions(billboard.InstitutionQuery{FullCode: fullCode, StartDate: tradeDate, EndDate: tradeDate, Limit: 100})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	successResponse(w, map[string]any{
		"items":        entries.Items,
		"seat_trades":  seatTradeItems(seats.Items, includeSource(r)),
		"institutions": institutionItems(institutions.Items, includeSource(r)),
		"freshness":    freshness,
		"source":       billboardSource(billboard.AllReports()),
	})
}

func handleMarketBillboardSeats(w http.ResponseWriter, r *http.Request) {
	handleMarketBillboardSeatList(w, r, false)
}

func handleMarketBillboardSeat(w http.ResponseWriter, r *http.Request) {
	handleMarketBillboardSeatList(w, r, true)
}

func handleMarketBillboardSeatList(w http.ResponseWriter, r *http.Request, requireKeyword bool) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	query := r.URL.Query()
	keyword := strings.TrimSpace(query.Get("keyword"))
	if requireKeyword && keyword == "" {
		errorResponse(w, "keyword 为必填参数")
		return
	}
	store := requireMarketBillboardStore(w)
	if store == nil {
		return
	}
	list, err := store.ListSeatTrades(billboard.SeatTradeQuery{
		FullCode:  strings.TrimSpace(query.Get("full_code")),
		TradeDate: billboard.ParseEastmoneyDate(query.Get("trade_date")),
		Keyword:   keyword,
		Side:      strings.TrimSpace(query.Get("side")),
		Limit:     parsePositiveInt(query.Get("limit")),
		Cursor:    strings.TrimSpace(query.Get("cursor")),
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	tradeDate := billboard.ParseEastmoneyDate(query.Get("trade_date"))
	freshness, _ := store.Coverage(tradeDate, tradeDate, []string{billboard.ReportBuyDetails, billboard.ReportSellDetails})
	successResponse(w, map[string]any{
		"items":       seatTradeItems(list.Items, includeSource(r)),
		"count":       len(list.Items),
		"next_cursor": emptyNil(list.NextCursor),
		"freshness":   freshness,
		"source":      billboardSource([]string{billboard.ReportBuyDetails, billboard.ReportSellDetails}),
	})
}

func handleMarketBillboardInstitutions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	query := r.URL.Query()
	startDate, endDate := billboardDateRange(query.Get("trade_date"), query.Get("start_date"), query.Get("end_date"))
	store := requireMarketBillboardStore(w)
	if store == nil {
		return
	}
	list, err := store.ListInstitutions(billboard.InstitutionQuery{
		StartDate: startDate,
		EndDate:   endDate,
		FullCode:  strings.TrimSpace(query.Get("full_code")),
		Limit:     parsePositiveInt(query.Get("limit")),
		Cursor:    strings.TrimSpace(query.Get("cursor")),
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	freshness, _ := store.Coverage(startDate, endDate, []string{billboard.ReportOrganizationTradeDetails})
	successResponse(w, map[string]any{
		"items":       institutionItems(list.Items, includeSource(r)),
		"count":       len(list.Items),
		"next_cursor": emptyNil(list.NextCursor),
		"freshness":   freshness,
		"source":      billboardSource([]string{billboard.ReportOrganizationTradeDetails}),
	})
}

func handleMarketBillboardStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}
	query := r.URL.Query()
	store := requireMarketBillboardStore(w)
	if store == nil {
		return
	}
	list, err := store.ListInstrumentStats(billboard.InstrumentStatQuery{
		FullCode: strings.TrimSpace(query.Get("full_code")),
		Cycle:    strings.TrimSpace(query.Get("cycle")),
		Limit:    parsePositiveInt(query.Get("limit")),
		Cursor:   strings.TrimSpace(query.Get("cursor")),
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	freshness, _ := store.Coverage("", "", []string{billboard.ReportTradeAll})
	successResponse(w, map[string]any{
		"items":       instrumentStatItems(list.Items, includeSource(r)),
		"count":       len(list.Items),
		"next_cursor": emptyNil(list.NextCursor),
		"freshness":   freshness,
		"source":      billboardSource([]string{billboard.ReportTradeAll}),
	})
}

func requireMarketBillboardStore(w http.ResponseWriter) *billboard.Store {
	if marketBillboardStore == nil {
		errorResponse(w, "market_billboard store 未初始化")
		return nil
	}
	return marketBillboardStore
}

func listBillboardEntries(query billboard.EntryQuery) (billboard.EntryList, billboard.Freshness, error) {
	if marketBillboardStore == nil {
		return billboard.EntryList{}, billboard.Freshness{}, fmt.Errorf("market_billboard store 未初始化")
	}
	list, err := marketBillboardStore.ListEntries(query)
	if err != nil {
		return billboard.EntryList{}, billboard.Freshness{}, err
	}
	freshness, err := marketBillboardStore.Coverage(query.StartDate, query.EndDate, billboard.CoreReports())
	if err != nil {
		return billboard.EntryList{}, billboard.Freshness{}, err
	}
	return list, freshness, nil
}

func sortSeatTradesByRank(items []billboard.SeatTradeView) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Side != right.Side {
			return left.Side < right.Side
		}
		if left.Rank != right.Rank {
			return left.Rank < right.Rank
		}
		return left.ID < right.ID
	})
}

func billboardListResponse(list billboard.EntryList, freshness billboard.Freshness, reports []string, includeSource bool) map[string]any {
	return map[string]any{
		"items":       entryItems(list.Items, includeSource),
		"count":       len(list.Items),
		"next_cursor": emptyNil(list.NextCursor),
		"freshness":   freshness,
		"source":      billboardSource(reports),
	}
}

func entryItems(items []billboard.EntryRecord, includeSource bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"id":                          item.ID,
			"trade_date":                  item.TradeDate,
			"full_code":                   item.FullCode,
			"code":                        item.Code,
			"exchange":                    item.Exchange,
			"name":                        item.Name,
			"asset_type":                  item.AssetType,
			"close_price_milli":           item.ClosePriceMilli,
			"change_rate_pct":             item.ChangeRatePct,
			"billboard_net_amount_milli":  item.BillboardNetAmountMilli,
			"billboard_buy_amount_milli":  item.BillboardBuyAmountMilli,
			"billboard_sell_amount_milli": item.BillboardSellAmountMilli,
			"billboard_deal_amount_milli": item.BillboardDealAmountMilli,
			"accum_amount_milli":          item.AccumAmountMilli,
			"deal_net_ratio_pct":          item.DealNetRatioPct,
			"deal_amount_ratio_pct":       item.DealAmountRatioPct,
			"turnover_rate_pct":           item.TurnoverRatePct,
			"free_market_cap_milli":       item.FreeMarketCapMilli,
			"provider_d1_close_adj_pct":   item.ProviderD1CloseAdjPct,
			"provider_d2_close_adj_pct":   item.ProviderD2CloseAdjPct,
			"provider_d3_close_adj_pct":   item.ProviderD3CloseAdjPct,
			"provider_d5_close_adj_pct":   item.ProviderD5CloseAdjPct,
			"provider_d10_close_adj_pct":  item.ProviderD10CloseAdjPct,
		}
		if includeSource {
			row["source_report_name"] = item.SourceReportName
			row["source_trade_id"] = item.SourceTradeID
			row["source_change_type"] = item.SourceChangeType
			row["source_row_id"] = item.SourceRowID
			row["source_payload_hash"] = item.SourcePayloadHash
		}
		out = append(out, row)
	}
	return out
}

func seatTradeItems(items []billboard.SeatTradeView, includeSource bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"id":                 item.ID,
			"trade_date":         item.TradeDate,
			"full_code":          item.FullCode,
			"side":               item.Side,
			"rank":               item.Rank,
			"seat_name":          item.SeatName,
			"seat_type":          item.SeatType,
			"source_seat_code":   item.SourceSeatCode,
			"buy_amount_milli":   item.BuyAmountMilli,
			"sell_amount_milli":  item.SellAmountMilli,
			"net_amount_milli":   item.NetAmountMilli,
			"accum_amount_milli": item.AccumAmountMilli,
			"accum_volume_share": item.AccumVolumeShare,
			"buy_ratio":          item.BuyRatio,
			"sell_ratio":         item.SellRatio,
		}
		if includeSource {
			row["source_report_name"] = item.SourceReportName
			row["source_row_id"] = item.SourceRowID
			row["source_payload_hash"] = item.SourcePayloadHash
		}
		out = append(out, row)
	}
	return out
}

func institutionItems(items []billboard.InstitutionTradeRecord, includeSource bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"id":                         item.ID,
			"trade_date":                 item.TradeDate,
			"full_code":                  item.FullCode,
			"code":                       item.Code,
			"exchange":                   item.Exchange,
			"name":                       item.Name,
			"buy_times":                  item.BuyTimes,
			"sell_times":                 item.SellTimes,
			"buy_count":                  item.BuyCount,
			"sell_count":                 item.SellCount,
			"buy_amount_milli":           item.BuyAmountMilli,
			"sell_amount_milli":          item.SellAmountMilli,
			"net_buy_amount_milli":       item.NetBuyAmountMilli,
			"free_market_cap_milli":      item.FreeMarketCapMilli,
			"provider_d1_close_adj_pct":  item.ProviderD1CloseAdjPct,
			"provider_d10_close_adj_pct": item.ProviderD10CloseAdjPct,
		}
		if includeSource {
			row["source_report_name"] = item.SourceReportName
			row["source_row_id"] = item.SourceRowID
			row["source_payload_hash"] = item.SourcePayloadHash
		}
		out = append(out, row)
	}
	return out
}

func instrumentStatItems(items []billboard.InstrumentStatRecord, includeSource bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"id":                             item.ID,
			"full_code":                      item.FullCode,
			"code":                           item.Code,
			"exchange":                       item.Exchange,
			"name":                           item.Name,
			"statistics_cycle":               item.StatisticsCycle,
			"period_label":                   item.PeriodLabel,
			"latest_trade_date":              item.LatestTradeDate,
			"billboard_times":                item.BillboardTimes,
			"billboard_deal_amount_milli":    item.BillboardDealAmountMilli,
			"billboard_buy_amount_milli":     item.BillboardBuyAmountMilli,
			"billboard_sell_amount_milli":    item.BillboardSellAmountMilli,
			"billboard_net_buy_amount_milli": item.BillboardNetBuyAmountMilli,
			"org_times":                      item.OrgTimes,
			"org_deal_amount_milli":          item.OrgDealAmountMilli,
			"org_buy_amount_milli":           item.OrgBuyAmountMilli,
			"org_sell_amount_milli":          item.OrgSellAmountMilli,
			"org_net_buy_amount_milli":       item.OrgNetBuyAmountMilli,
			"instrument_pct_1m":              item.InstrumentPct1M,
			"instrument_pct_3m":              item.InstrumentPct3M,
			"instrument_pct_6m":              item.InstrumentPct6M,
			"instrument_pct_1y":              item.InstrumentPct1Y,
		}
		if includeSource {
			row["source_report_name"] = item.SourceReportName
			row["source_row_id"] = item.SourceRowID
			row["source_payload_hash"] = item.SourcePayloadHash
		}
		out = append(out, row)
	}
	return out
}

func billboardSource(reports []string) map[string]any {
	return map[string]any{
		"provider": "eastmoney",
		"module":   "rpt",
		"reports":  reports,
	}
}

func billboardDateRange(tradeDate, startDate, endDate string) (string, string) {
	tradeDate = billboard.ParseEastmoneyDate(tradeDate)
	if tradeDate != "" {
		return tradeDate, tradeDate
	}
	return billboard.ParseEastmoneyDate(startDate), billboard.ParseEastmoneyDate(endDate)
}

func includeSource(r *http.Request) bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("include_source")))
	return raw == "1" || raw == "true" || raw == "yes"
}

func emptyNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
