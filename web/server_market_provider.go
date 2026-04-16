package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tdx "github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type providerPriceLevel struct {
	Level  int     `json:"level"`
	Price  float64 `json:"price"`
	Volume int     `json:"volume"`
}

type providerQuoteSnapshot struct {
	Code          string               `json:"code"`
	FullCode      string               `json:"full_code"`
	Name          string               `json:"name"`
	Exchange      string               `json:"exchange"`
	AssetType     string               `json:"asset_type"`
	TradingStatus string               `json:"trading_status"`
	IsHalted      bool                 `json:"is_halted"`
	IsLimitUp     bool                 `json:"is_limit_up"`
	IsLimitDown   bool                 `json:"is_limit_down"`
	StatusSource  string               `json:"status_source"`
	StatusReason  string               `json:"status_reason"`
	Price         float64              `json:"price"`
	PrevClose     float64              `json:"prev_close"`
	Open          float64              `json:"open"`
	High          float64              `json:"high"`
	Low           float64              `json:"low"`
	Change        float64              `json:"change"`
	ChangePct     float64              `json:"change_pct"`
	Volume        int64                `json:"volume"`
	Amount        float64              `json:"amount"`
	QuoteTime     string               `json:"quote_time"`
	Bids          []providerPriceLevel `json:"bids,omitempty"`
	Asks          []providerPriceLevel `json:"asks,omitempty"`
}

type providerInstrumentReference struct {
	Code            string   `json:"code"`
	FullCode        string   `json:"full_code"`
	Name            string   `json:"name"`
	Exchange        string   `json:"exchange"`
	AssetType       string   `json:"asset_type"`
	TickSize        float64  `json:"tick_size"`
	LotSize         int      `json:"lot_size"`
	Multiple        int      `json:"multiple"`
	ListingDate     string   `json:"listing_date,omitempty"`
	IssuerName      string   `json:"issuer_name,omitempty"`
	BusinessSummary string   `json:"business_summary,omitempty"`
	IndustryCode    string   `json:"industry_code,omitempty"`
	IndustryName    string   `json:"industry_name,omitempty"`
	SubindustryName string   `json:"subindustry_name,omitempty"`
	TradingStatus   string   `json:"trading_status"`
	MissingFields   []string `json:"missing_fields"`
}

type providerBarItem struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
	Amount float64 `json:"amount"`
}

type providerAdjustmentFactor struct {
	ExDate         string  `json:"ex_date"`
	ForwardFactor  float64 `json:"forward_factor"`
	BackwardFactor float64 `json:"backward_factor"`
}

type historyBarRow struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
	Amount float64
}

type inferredQuoteStatus struct {
	TradingStatus string
	IsHalted      bool
	IsLimitUp     bool
	IsLimitDown   bool
	Source        string
	Reason        string
}

type blockProviderKey struct {
	Source    string
	BlockType string
	Name      string
}

type blockProviderKeyRequirement struct {
	RequireSource    bool
	RequireBlockType bool
	RequireName      bool
}

var (
	issuerPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)(?:公司全称|公司名称|中文名称|法定中文名称|发行人名称)\s*[:：]\s*([^\n\r]+)`),
		regexp.MustCompile(`(?m)(?:公司全称|公司名称|中文名称|法定中文名称|发行人名称)\s+([^\n\r]+)`),
	}
	businessSummaryPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)(?:主营业务|公司主营业务|主要业务|经营范围)\s*[:：]\s*([^\n\r]+)`),
		regexp.MustCompile(`(?m)(?:主营业务|公司主营业务|主要业务|经营范围)\s+([^\n\r]+)`),
		regexp.MustCompile(`(?m)(?:公司主要从事|公司是国内|公司是一家)\s*([^\n\r。；;]+)`),
	}
	f10BoxCharsPattern        = regexp.MustCompile(`[┌┐└┘├┤┬┴┼│─━┃┏┓┗┛┣┫┳┻╋═║╔╗╚╝╠╣╦╩╬╭╮╯╰]+`)
	f10LeaderPattern          = regexp.MustCompile(`[\.．·•…]{2,}`)
	f10LeadingMarkerPattern   = regexp.MustCompile(`^(?:[（(]?[一二三四五六七八九十0-9]+[)）]|[一二三四五六七八九十0-9]+[、.．])\s*`)
	f10WhitespacePattern      = regexp.MustCompile(`\s+`)
	issuerStopLabels          = []string{"英文名称", "英文简称", "证券代码", "股票代码", "A股简称", "法定代表人", "成立日期", "注册资本", "所属行业", "经营范围", "主营业务", "公司地址", "办公地址"}
	businessSummaryStopLabels = []string{"英文名称", "英文简称", "证券代码", "股票代码", "A股简称", "法定代表人", "成立日期", "注册资本", "所属行业", "公司地址", "办公地址", "联系电话", "传真", "电子邮箱"}
)

func serveQuoteSnapshots(w http.ResponseWriter, r *http.Request) {
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	items, err := fetchQuoteSnapshots([]*tdx.CodeModel{model})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	if len(items) == 0 {
		errorResponse(w, fmt.Sprintf("未获取到行情: %s", model.FullCode()))
		return
	}
	successResponse(w, items[0])
}

func serveBatchQuoteSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	var req struct {
		FullCodes []string `json:"full_codes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}
	if len(req.FullCodes) == 0 {
		errorResponse(w, "full_codes 为必填参数")
		return
	}
	models, err := resolveFullCodeModels(req.FullCodes)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	items, err := fetchQuoteSnapshots(models)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"count": len(items),
		"items": items,
	})
}

func fetchQuoteSnapshots(models []*tdx.CodeModel) ([]providerQuoteSnapshot, error) {
	if len(models) > 50 {
		return nil, errors.New("一次最多查询50只证券")
	}
	if client == nil {
		return nil, errors.New("TDX client 未初始化")
	}

	queryCodes := make([]string, 0, len(models))
	for _, model := range models {
		queryCodes = append(queryCodes, model.FullCode())
	}
	quotes, err := client.GetQuote(queryCodes...)
	if err != nil {
		return nil, fmt.Errorf("获取行情失败: %v", err)
	}

	byFullCode := make(map[string]*protocol.Quote, len(quotes))
	for _, quote := range quotes {
		if quote == nil {
			continue
		}
		byFullCode[strings.ToLower(protocol.AddPrefix(quote.Code))] = quote
	}

	now := time.Now()
	items := make([]providerQuoteSnapshot, 0, len(models))
	for _, model := range models {
		quote := byFullCode[strings.ToLower(model.FullCode())]
		if quote == nil {
			continue
		}
		items = append(items, buildQuoteSnapshotItem(model, quote, now))
	}
	return items, nil
}

func handleGetInstrument(w http.ResponseWriter, r *http.Request) {
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	if client == nil {
		errorResponse(w, "TDX client 未初始化")
		return
	}

	var finance *protocol.FinanceInfo
	if data, err := client.GetFinanceInfo(model.FullCode()); err == nil {
		finance = data
	}

	issuerName, businessSummary := fetchInstrumentF10Fields(model.FullCode())
	industryName, subindustryName := loadIndustryLabels(model.FullCode())
	tradingStatus := "unknown"
	if quotes, err := client.GetQuote(model.FullCode()); err == nil && len(quotes) > 0 && quotes[0] != nil {
		tradingStatus = inferQuoteStatus(model.FullCode(), model.Name, quotes[0], time.Now()).TradingStatus
	}

	successResponse(w, buildInstrumentReference(model, finance, issuerName, businessSummary, industryName, subindustryName, tradingStatus))
}

func serveHistoricalBars(w http.ResponseWriter, r *http.Request) {
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	frequency := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("frequency")))
	if frequency == "" {
		errorResponse(w, "frequency 为必填参数")
		return
	}
	adjustMode, err := parseAdjustMode(strings.TrimSpace(strings.ToLower(r.URL.Query().Get("adjust_mode"))))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	startDate, endDate, count, err := parseHistoryRangeParams(r)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	rows, source, err := fetchHistoricalBarRows(model, frequency, adjustMode)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	rows, err = filterHistoryBarRows(rows, startDate, endDate, count)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	items := make([]providerBarItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, providerBarItem{
			Time:   row.Time.Format("2006-01-02"),
			Open:   row.Open,
			High:   row.High,
			Low:    row.Low,
			Close:  row.Close,
			Volume: row.Volume,
			Amount: row.Amount,
		})
	}

	successResponse(w, map[string]interface{}{
		"code":        model.Code,
		"full_code":   model.FullCode(),
		"name":        model.Name,
		"exchange":    providerExchange(model),
		"asset_type":  modelAssetType(model),
		"frequency":   frequency,
		"adjust_mode": adjustMode,
		"source":      source,
		"count":       len(items),
		"items":       items,
	})
}

func handleGetAdjustmentFactors(w http.ResponseWriter, r *http.Request) {
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	if modelAssetType(model) == "index" {
		errorResponse(w, "index 不支持 adjustment factors")
		return
	}
	if client == nil {
		errorResponse(w, "TDX client 未初始化")
		return
	}

	startDate, endDate, _, err := parseHistoryRangeParamsAllowEmpty(r)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	_, factors, err := extend.GetTHSDayKlineFactorFull(model.FullCode(), client)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取复权因子失败: %v", err))
		return
	}

	items := buildAdjustmentFactorItems(factors, startDate, endDate)
	if len(items) == 0 {
		errorResponse(w, "指定范围内无复权因子数据")
		return
	}

	successResponse(w, map[string]interface{}{
		"code":      model.Code,
		"full_code": model.FullCode(),
		"count":     len(items),
		"items":     items,
	})
}

func serveIntradayBars(w http.ResponseWriter, r *http.Request) {
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	tradingDateRaw := strings.TrimSpace(r.URL.Query().Get("trading_date"))
	if tradingDateRaw == "" {
		errorResponse(w, "trading_date 为必填参数")
		return
	}
	tradingDate, err := parseWorkdayDate(tradingDateRaw)
	if err != nil {
		errorResponse(w, "trading_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}
	intervalMinutes, err := parseIntradayInterval(strings.TrimSpace(r.URL.Query().Get("interval_minutes")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	session := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("session")))
	if session == "" {
		session = "full"
	}
	if session != "full" {
		errorResponse(w, "session 仅支持 full")
		return
	}

	rows, source, err := fetchIntradayBarRows(model, intervalMinutes)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	rows, err = filterIntradayBarRows(rows, tradingDate)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	items := make([]providerBarItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, providerBarItem{
			Time:   row.Time.Format(time.RFC3339),
			Open:   row.Open,
			High:   row.High,
			Low:    row.Low,
			Close:  row.Close,
			Volume: row.Volume,
			Amount: row.Amount,
		})
	}

	successResponse(w, map[string]interface{}{
		"code":             model.Code,
		"full_code":        model.FullCode(),
		"name":             model.Name,
		"exchange":         providerExchange(model),
		"asset_type":       modelAssetType(model),
		"trading_date":     tradingDate.Format("2006-01-02"),
		"interval_minutes": intervalMinutes,
		"session":          session,
		"source":           source,
		"count":            len(items),
		"items":            items,
	})
}

func serveBlocks(w http.ResponseWriter, r *http.Request) {
	bs := getBlockServiceForProvider()
	if bs == nil {
		errorResponse(w, "板块服务未初始化")
		return
	}
	key, err := parseBlockProviderKey(r, blockProviderKeyRequirement{RequireSource: true})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	if key.Name != "" && key.BlockType == "" {
		errorResponse(w, "name 过滤需要同时提供 block_type")
		return
	}

	records := bs.GetBlocks("")
	items := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		if key.Source != "" && record.Source != key.Source {
			continue
		}
		if key.BlockType != "" && record.BlockType != key.BlockType {
			continue
		}
		if key.Name != "" && record.Name != key.Name {
			continue
		}
		items = append(items, map[string]interface{}{
			"source":      record.Source,
			"block_type":  record.BlockType,
			"name":        record.Name,
			"stock_count": record.StockCount,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i]["block_type"].(string) != items[j]["block_type"].(string) {
			return items[i]["block_type"].(string) < items[j]["block_type"].(string)
		}
		if items[i]["source"].(string) != items[j]["source"].(string) {
			return items[i]["source"].(string) < items[j]["source"].(string)
		}
		return items[i]["name"].(string) < items[j]["name"].(string)
	})

	successResponse(w, map[string]interface{}{
		"count": len(items),
		"items": items,
	})
}

func serveBlockMembers(w http.ResponseWriter, r *http.Request) {
	bs := getBlockServiceForProvider()
	if bs == nil {
		errorResponse(w, "板块服务未初始化")
		return
	}
	key, err := parseBlockProviderKey(r, blockProviderKeyRequirement{RequireSource: true, RequireBlockType: true, RequireName: true})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	codes := bs.GetBlockMembers(key.Source, key.BlockType, key.Name)
	if len(codes) == 0 {
		errorResponse(w, "指定 provider key 未找到板块成分")
		return
	}
	successResponse(w, map[string]interface{}{
		"source":     key.Source,
		"block_type": key.BlockType,
		"name":       key.Name,
		"count":      len(codes),
		"codes":      codes,
	})
}

func serveStockBlocks(w http.ResponseWriter, r *http.Request) {
	bs := getBlockServiceForProvider()
	if bs == nil {
		errorResponse(w, "板块服务未初始化")
		return
	}
	model, err := resolveSingleFullCodeModel(strings.TrimSpace(r.URL.Query().Get("full_code")))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	groups := bs.GetStockBlocks(model.FullCode())
	items := make([]map[string]interface{}, 0, len(groups))
	for _, group := range groups {
		items = append(items, map[string]interface{}{
			"source":      group.Source,
			"block_type":  group.BlockType,
			"name":        group.Name,
			"stock_count": group.StockCount,
		})
	}
	successResponse(w, map[string]interface{}{
		"code":      model.Code,
		"full_code": model.FullCode(),
		"count":     len(items),
		"items":     items,
	})
}

func serveBlockRanking(w http.ResponseWriter, r *http.Request) {
	ts := getTickerService()
	if ts == nil {
		errorResponse(w, "实时行情服务未初始化")
		return
	}
	key, err := parseBlockProviderKey(r, blockProviderKeyRequirement{RequireSource: true, RequireBlockType: true})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort_by"))
	order := strings.TrimSpace(r.URL.Query().Get("order"))
	limit := parsePositiveInt(strings.TrimSpace(r.URL.Query().Get("limit")))

	ranks := ts.GetBlockRanking(key.Source, key.BlockType, sortBy, order, limit)
	items := make([]map[string]interface{}, 0, len(ranks))
	for _, rank := range ranks {
		if key.Name != "" && rank.Name != key.Name {
			continue
		}
		item := map[string]interface{}{
			"source":            rank.Source,
			"block_type":        rank.BlockType,
			"name":              rank.Name,
			"pct_change":        rank.PctChange,
			"amount":            rank.Amount,
			"member_count":      rank.MemberCount,
			"available_count":   rank.AvailableCount,
			"rise_count":        rank.RiseCount,
			"fall_count":        rank.FallCount,
			"flat_count":        rank.FlatCount,
			"limit_up_count":    rank.LimitUpCount,
			"limit_down_count":  rank.LimitDownCount,
			"leading_full_code": rank.LeadingCode,
			"leading_name":      rank.LeadingName,
			"leading_pct":       rank.LeadingPct,
		}
		item["leading_code"] = bareCode(rank.LeadingCode)
		items = append(items, item)
	}
	resp := map[string]interface{}{
		"count": len(items),
		"items": items,
	}
	addTickerMeta(resp, ts)
	successResponse(w, resp)
}

func serveBlockStocks(w http.ResponseWriter, r *http.Request) {
	ts := getTickerService()
	if ts == nil {
		errorResponse(w, "实时行情服务未初始化")
		return
	}
	key, err := parseBlockProviderKey(r, blockProviderKeyRequirement{RequireSource: true, RequireBlockType: true, RequireName: true})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort_by"))
	order := strings.TrimSpace(r.URL.Query().Get("order"))
	limit := parsePositiveInt(strings.TrimSpace(r.URL.Query().Get("limit")))

	blockPct, ticks := ts.GetBlockStocks(key.Source, key.BlockType, key.Name, sortBy, order, limit)
	items := make([]map[string]interface{}, 0, len(ticks))
	for _, tick := range ticks {
		items = append(items, stockTickToProviderMap(tick))
	}
	resp := map[string]interface{}{
		"source":           key.Source,
		"block_type":       key.BlockType,
		"name":             key.Name,
		"block_pct_change": blockPct,
		"count":            len(items),
		"items":            items,
	}
	addTickerMeta(resp, ts)
	successResponse(w, resp)
}

func handleMarketSignalCheck(w http.ResponseWriter, r *http.Request) {
	ss := getSignalService()
	if ss == nil {
		errorResponse(w, "Signal 服务未初始化")
		return
	}
	models, err := resolveFullCodeModels(splitCodes(strings.TrimSpace(r.URL.Query().Get("full_codes"))))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	rawSignalTypes := splitCodes(strings.TrimSpace(r.URL.Query().Get("signal_types")))
	if len(rawSignalTypes) == 0 {
		errorResponse(w, "signal_types 为必填参数")
		return
	}
	mode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "hits_only"
	}
	if mode != "hits_only" && mode != "full" {
		errorResponse(w, "mode 仅支持 hits_only 或 full")
		return
	}

	fullCodes := make([]string, 0, len(models))
	for _, model := range models {
		fullCodes = append(fullCodes, model.FullCode())
	}
	hits, err := ss.CheckCodes(fullCodes, rawSignalTypes)
	if err != nil {
		errorResponse(w, fmt.Sprintf("执行定向 signal 检查失败: %v", err))
		return
	}

	checkedAt := time.Now()
	successResponse(w, buildSignalCheckPayload(fullCodes, rawSignalTypes, mode, hits, checkedAt))
}

func buildSignalCheckPayload(fullCodes []string, signalTypes []string, mode string, hits []collectorpkg.SignalItem, checkedAt time.Time) map[string]interface{} {
	hitMap := make(map[string]collectorpkg.SignalItem, len(hits))
	for _, hit := range hits {
		hitMap[hit.Code+"|"+hit.SignalType] = hit
	}

	items := make([]map[string]interface{}, 0, len(hits))
	if mode == "full" {
		items = make([]map[string]interface{}, 0, len(fullCodes)*len(signalTypes))
		for _, fullCode := range fullCodes {
			for _, signalType := range signalTypes {
				item := map[string]interface{}{
					"code":        bareCode(fullCode),
					"full_code":   fullCode,
					"signal_type": signalType,
					"matched":     false,
				}
				if hit, ok := hitMap[fullCode+"|"+signalType]; ok {
					item["matched"] = true
					item["name"] = hit.Name
					item["trigger_time"] = checkedAt.Format(time.RFC3339)
				}
				items = append(items, item)
			}
		}
	} else {
		for _, hit := range hits {
			items = append(items, map[string]interface{}{
				"code":         bareCode(hit.Code),
				"full_code":    hit.Code,
				"name":         hit.Name,
				"signal_type":  hit.SignalType,
				"trigger_time": checkedAt.Format(time.RFC3339),
			})
		}
	}

	return map[string]interface{}{
		"mode":          mode,
		"checked_at":    checkedAt.Format(time.RFC3339),
		"checked_codes": fullCodes,
		"signal_types":  signalTypes,
		"count":         len(items),
		"items":         items,
	}
}

func buildInstrumentReference(model *tdx.CodeModel, finance *protocol.FinanceInfo, issuerName, businessSummary, industryName, subindustryName, tradingStatus string) providerInstrumentReference {
	missing := make([]string, 0, 6)
	listingDate := ""
	industryCode := ""
	if finance == nil {
		missing = append(missing, "listing_date", "industry_code")
	} else {
		if finance.IpoDate > 0 {
			listingDate = compactDateToISO(fmt.Sprintf("%08d", finance.IpoDate))
		} else {
			missing = append(missing, "listing_date")
		}
		if finance.Industry > 0 {
			industryCode = strconv.Itoa(int(finance.Industry))
		} else {
			missing = append(missing, "industry_code")
		}
	}
	if issuerName == "" {
		missing = append(missing, "issuer_name")
	}
	if businessSummary == "" {
		missing = append(missing, "business_summary")
	}
	if industryName == "" {
		missing = append(missing, "industry_name")
	}
	if subindustryName == "" {
		missing = append(missing, "subindustry_name")
	}
	if tradingStatus == "" {
		tradingStatus = "unknown"
	}

	return providerInstrumentReference{
		Code:            model.Code,
		FullCode:        model.FullCode(),
		Name:            model.Name,
		Exchange:        providerExchange(model),
		AssetType:       modelAssetType(model),
		TickSize:        tickSizeFromDecimal(model.Decimal),
		LotSize:         int(model.Multiple),
		Multiple:        int(model.Multiple),
		ListingDate:     listingDate,
		IssuerName:      issuerName,
		BusinessSummary: businessSummary,
		IndustryCode:    industryCode,
		IndustryName:    industryName,
		SubindustryName: subindustryName,
		TradingStatus:   tradingStatus,
		MissingFields:   missing,
	}
}

func fetchInstrumentF10Fields(fullCode string) (string, string) {
	if client == nil {
		return "", ""
	}
	categories, err := client.GetCompanyInfoCategory(fullCode)
	if err != nil {
		return "", ""
	}

	selected := selectRelevantF10Categories(categories)
	var issuerName string
	var businessSummary string
	for _, category := range selected {
		content, err := client.GetCompanyInfoContent(fullCode, category.Filename, category.Start, category.Length)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		cleanContent := sanitizeF10Text(content)
		if issuerName == "" {
			issuerName = extractIssuerName(cleanContent)
		}
		if businessSummary == "" {
			businessSummary = extractBusinessSummary(cleanContent)
		}
		if issuerName != "" && businessSummary != "" {
			break
		}
	}
	return issuerName, businessSummary
}

func selectRelevantF10Categories(categories protocol.CompanyInfoCategories) []*protocol.CompanyInfoCategory {
	if len(categories) == 0 {
		return nil
	}
	priorityKeywords := []string{"公司概况", "公司简介", "公司资料", "公司概述", "招股说明书", "招股意向书"}
	selected := make([]*protocol.CompanyInfoCategory, 0, 4)
	for _, keyword := range priorityKeywords {
		for _, category := range categories {
			if category == nil || category.Length == 0 {
				continue
			}
			if strings.Contains(category.Name, keyword) {
				selected = append(selected, category)
				if len(selected) >= 4 {
					return selected
				}
			}
		}
	}
	if len(selected) > 0 {
		return selected
	}
	for _, category := range categories {
		if category == nil || category.Length == 0 {
			continue
		}
		selected = append(selected, category)
		if len(selected) >= 2 {
			break
		}
	}
	return selected
}

func extractIssuerName(text string) string {
	if value := extractPatternValue(text, issuerPatterns, issuerStopLabels); value != "" {
		return value
	}
	return extractLineValue(text, []string{"公司全称", "公司名称", "中文名称", "法定中文名称", "发行人名称"}, issuerStopLabels)
}

func extractBusinessSummary(text string) string {
	if value := extractPatternValue(text, businessSummaryPatterns, businessSummaryStopLabels); value != "" {
		return truncateSummary(value)
	}
	return truncateSummary(extractLineValue(text, []string{"主营业务", "主要业务", "经营范围"}, businessSummaryStopLabels))
}

func extractPatternValue(text string, patterns []*regexp.Regexp, stopLabels []string) string {
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) < 2 {
			continue
		}
		if value := normalizeF10FieldValue(matches[1], stopLabels); value != "" {
			return value
		}
	}
	return ""
}

func extractLineValue(text string, keys []string, stopLabels []string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n")
	for index, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		for _, key := range keys {
			if !strings.Contains(line, key) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if pos := strings.IndexAny(trimmed, "：:"); pos >= 0 && pos+1 < len(trimmed) {
				if value := normalizeF10FieldValue(trimmed[pos+1:], stopLabels); value != "" {
					return value
				}
			}
			if index+1 < len(lines) {
				if value := normalizeF10FieldValue(lines[index+1], stopLabels); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func cleanF10Value(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.NewReplacer("\r", "\n", "\t", " ", "\u00a0", " ", "|", " ", "｜", " ").Replace(value)
	value = f10BoxCharsPattern.ReplaceAllString(value, " ")
	value = f10LeaderPattern.ReplaceAllString(value, " ")
	value = f10WhitespacePattern.ReplaceAllString(value, " ")
	value = strings.Trim(value, "：:;；，,。 \t")
	if isF10NoiseLine(value) {
		return ""
	}
	if len(value) > 200 {
		value = value[:200]
	}
	return strings.TrimSpace(value)
}

func truncateSummary(value string) string {
	value = cleanF10Value(value)
	if value == "" {
		return ""
	}
	if parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '。' || r == '；' || r == ';'
	}); len(parts) > 0 {
		value = strings.TrimSpace(parts[0])
	}
	if len(value) > 160 {
		value = value[:160]
	}
	value = strings.TrimSpace(f10LeadingMarkerPattern.ReplaceAllString(value, ""))
	return strings.TrimSpace(value)
}

func sanitizeF10Text(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, rawLine := range lines {
		line := cleanF10Value(rawLine)
		if line == "" || isF10NoiseLine(line) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func normalizeF10FieldValue(value string, stopLabels []string) string {
	value = trimF10FieldFragments(cleanF10Value(value), stopLabels)
	if value == "" || isF10NoiseLine(value) {
		return ""
	}
	return cleanF10Value(value)
}

func trimF10FieldFragments(value string, stopLabels []string) string {
	trimmed := value
	for _, label := range stopLabels {
		if idx := strings.Index(trimmed, label); idx > 0 {
			trimmed = trimmed[:idx]
		}
	}
	return strings.TrimSpace(trimmed)
}

func isF10NoiseLine(value string) bool {
	compact := strings.TrimSpace(value)
	if compact == "" {
		return true
	}
	joined := strings.ReplaceAll(compact, " ", "")
	if strings.Contains(joined, "目录") || strings.Contains(joined, "目次") {
		return true
	}
	if strings.HasPrefix(joined, "第") && strings.Contains(joined, "页") && len([]rune(joined)) <= 10 {
		return true
	}

	meaningful := 0
	for _, r := range joined {
		switch {
		case r >= '0' && r <= '9':
			meaningful++
		case r >= 'a' && r <= 'z':
			meaningful++
		case r >= 'A' && r <= 'Z':
			meaningful++
		case r >= 0x4e00 && r <= 0x9fff:
			meaningful++
		}
	}
	return meaningful == 0
}

func loadIndustryLabels(fullCode string) (string, string) {
	bs := getBlockServiceForProvider()
	if bs == nil {
		return "", ""
	}
	groups := bs.GetStockBlocks(fullCode)
	industryBlocks := make([]collectorpkg.BlockGroupRecord, 0, len(groups))
	for _, group := range groups {
		if group.BlockType == string(collectorpkg.BlockTypeIndustry) {
			industryBlocks = append(industryBlocks, group)
		}
	}
	if len(industryBlocks) == 0 {
		return "", ""
	}
	sort.Slice(industryBlocks, func(i, j int) bool {
		if industryBlocks[i].Source != industryBlocks[j].Source {
			return industryBlocks[i].Source < industryBlocks[j].Source
		}
		return industryBlocks[i].Name < industryBlocks[j].Name
	})
	industryName := industryBlocks[0].Name
	subindustryName := ""
	for _, block := range industryBlocks[1:] {
		if block.Name != industryName {
			subindustryName = block.Name
			break
		}
	}
	return industryName, subindustryName
}

func buildQuoteSnapshotItem(model *tdx.CodeModel, quote *protocol.Quote, now time.Time) providerQuoteSnapshot {
	fullCode := model.FullCode()
	prevClose := quote.K.Last.Float64()
	price := quote.K.Close.Float64()
	change := price - prevClose
	changePct := 0.0
	if prevClose != 0 {
		changePct = change / prevClose * 100
	}
	status := inferQuoteStatus(fullCode, model.Name, quote, now)

	item := providerQuoteSnapshot{
		Code:          model.Code,
		FullCode:      fullCode,
		Name:          model.Name,
		Exchange:      providerExchange(model),
		AssetType:     modelAssetType(model),
		TradingStatus: status.TradingStatus,
		IsHalted:      status.IsHalted,
		IsLimitUp:     status.IsLimitUp,
		IsLimitDown:   status.IsLimitDown,
		StatusSource:  status.Source,
		StatusReason:  status.Reason,
		Price:         roundFloat(price, 3),
		PrevClose:     roundFloat(prevClose, 3),
		Open:          roundFloat(quote.K.Open.Float64(), 3),
		High:          roundFloat(quote.K.High.Float64(), 3),
		Low:           roundFloat(quote.K.Low.Float64(), 3),
		Change:        roundFloat(change, 3),
		ChangePct:     roundFloat(changePct, 2),
		Volume:        int64(quote.TotalHand) * int64(maxInt(int(model.Multiple), 1)),
		Amount:        roundFloat(quote.Amount, 2),
		QuoteTime:     now.Format(time.RFC3339),
		Bids:          buildQuoteLevels(quote.BuyLevel, true),
		Asks:          buildQuoteLevels(quote.SellLevel, false),
	}
	return normalizeQuoteSnapshot(item)
}

func buildQuoteLevels(levels protocol.PriceLevels, bids bool) []providerPriceLevel {
	items := make([]providerPriceLevel, 0, len(levels))
	for index, level := range levels {
		items = append(items, providerPriceLevel{
			Level:  index + 1,
			Price:  roundFloat(level.Price.Float64(), 3),
			Volume: level.Number,
		})
	}
	return items
}

func inferQuoteStatus(fullCode, name string, quote *protocol.Quote, now time.Time) inferredQuoteStatus {
	prevClose := quote.K.Last.Float64()
	price := quote.K.Close.Float64()
	changePct := 0.0
	if prevClose != 0 {
		changePct = (price - prevClose) / prevClose * 100
	}

	isLimitUp := quoteIsLimitUp(changePct, fullCode, name)
	isLimitDown := quoteIsLimitDown(changePct, fullCode, name)
	isHalted := providerInTradingSession(now) &&
		quote.TotalHand == 0 &&
		quote.K.Open.Float64() == 0 &&
		quote.K.High.Float64() == 0 &&
		quote.K.Low.Float64() == 0 &&
		quote.K.Close.Float64() == 0

	tradingStatus := "active"
	if isHalted {
		tradingStatus = "halted"
	}
	reasons := []string{"TDX quote 未提供显式状态字段"}
	if isHalted {
		reasons = append(reasons, "停牌状态根据盘中零成交且 OHLC 全为 0 推断")
	} else if !providerInTradingSession(now) {
		reasons = append(reasons, "非交易时段无法确认停牌，仅回显当前推断结果")
	}
	if isLimitUp || isLimitDown {
		reasons = append(reasons, "涨跌停状态根据涨跌幅阈值推断")
	}

	return inferredQuoteStatus{
		TradingStatus: tradingStatus,
		IsHalted:      isHalted,
		IsLimitUp:     isLimitUp,
		IsLimitDown:   isLimitDown,
		Source:        "inferred_from_quote",
		Reason:        strings.Join(reasons, "；"),
	}
}

func quoteIsLimitUp(changePct float64, fullCode, name string) bool {
	threshold := quoteLimitThreshold(fullCode, name)
	return changePct >= threshold-0.05
}

func quoteIsLimitDown(changePct float64, fullCode, name string) bool {
	threshold := quoteLimitThreshold(fullCode, name)
	return changePct <= -(threshold - 0.05)
}

func quoteLimitThreshold(fullCode, name string) float64 {
	bare := bareCode(fullCode)
	switch {
	case strings.HasPrefix(bare, "68"), strings.HasPrefix(bare, "30"):
		return 20.0
	case strings.HasPrefix(bare, "83"), strings.HasPrefix(bare, "87"),
		strings.HasPrefix(bare, "82"), strings.HasPrefix(bare, "43"):
		return 30.0
	default:
		if strings.Contains(strings.ToUpper(name), "ST") {
			return 5.0
		}
		return 10.0
	}
}

func providerInTradingSession(now time.Time) bool {
	hour, minute, _ := now.Clock()
	currentMinutes := hour*60 + minute
	if currentMinutes < 9*60+15 || currentMinutes > 15*60+5 {
		return false
	}
	switch now.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	default:
		return true
	}
}

func fetchHistoricalBarRows(model *tdx.CodeModel, frequency, adjustMode string) ([]historyBarRow, string, error) {
	if client == nil {
		return nil, "", errors.New("TDX client 未初始化")
	}

	switch modelAssetType(model) {
	case "index":
		if adjustMode != "bfq" {
			return nil, "", fmt.Errorf("index 不支持 adjust_mode=%s", adjustMode)
		}
		var resp *protocol.KlineResp
		var err error
		switch frequency {
		case "day":
			resp, err = client.GetIndexDayAll(model.FullCode())
		case "week":
			resp, err = client.GetIndexWeekAll(model.FullCode())
		case "month":
			resp, err = client.GetIndexMonthAll(model.FullCode())
		default:
			return nil, "", fmt.Errorf("frequency 仅支持 day/week/month")
		}
		if err != nil {
			return nil, "", fmt.Errorf("获取指数历史K线失败: %w", err)
		}
		return historyRowsFromProtocol(resp.List), "tdx_index_kline", nil
	default:
		rows, source, err := fetchStockDailyBarRows(model, adjustMode)
		if err != nil {
			return nil, "", err
		}
		if frequency == "day" {
			return rows, source, nil
		}
		aggregated, err := aggregateHistoryBarRows(rows, frequency)
		if err != nil {
			return nil, "", err
		}
		return aggregated, source + "_aggregated", nil
	}
}

func fetchStockDailyBarRows(model *tdx.CodeModel, adjustMode string) ([]historyBarRow, string, error) {
	switch adjustMode {
	case "bfq":
		resp, err := client.GetKlineDayAll(model.FullCode())
		if err != nil {
			return nil, "", fmt.Errorf("获取不复权日线失败: %w", err)
		}
		return historyRowsFromProtocol(resp.List), "tdx_bfq_day", nil
	case "qfq", "hfq":
		all, err := extend.GetTHSDayKlineFull(model.FullCode(), client)
		if err != nil {
			return nil, "", fmt.Errorf("获取复权日线失败: %w", err)
		}
		index := 1
		if adjustMode == "hfq" {
			index = 2
		}
		return historyRowsFromExtend(all[index]), "ths_" + adjustMode + "_day", nil
	default:
		return nil, "", fmt.Errorf("adjust_mode 仅支持 bfq/qfq/hfq")
	}
}

func fetchIntradayBarRows(model *tdx.CodeModel, intervalMinutes int) ([]historyBarRow, string, error) {
	if client == nil {
		return nil, "", errors.New("TDX client 未初始化")
	}
	var (
		resp *protocol.KlineResp
		err  error
	)
	isIndex := modelAssetType(model) == "index"
	switch intervalMinutes {
	case 1:
		if isIndex {
			resp, err = client.GetIndexAll(protocol.TypeKlineMinute, model.FullCode())
		} else {
			resp, err = client.GetKlineMinuteAll(model.FullCode())
		}
	case 5:
		if isIndex {
			resp, err = client.GetIndexAll(protocol.TypeKline5Minute, model.FullCode())
		} else {
			resp, err = client.GetKline5MinuteAll(model.FullCode())
		}
	case 15:
		if isIndex {
			resp, err = client.GetIndexAll(protocol.TypeKline15Minute, model.FullCode())
		} else {
			resp, err = client.GetKline15MinuteAll(model.FullCode())
		}
	case 30:
		if isIndex {
			resp, err = client.GetIndexAll(protocol.TypeKline30Minute, model.FullCode())
		} else {
			resp, err = client.GetKline30MinuteAll(model.FullCode())
		}
	case 60:
		if isIndex {
			resp, err = client.GetIndexAll(protocol.TypeKline60Minute, model.FullCode())
		} else {
			resp, err = client.GetKline60MinuteAll(model.FullCode())
		}
	default:
		return nil, "", errors.New("interval_minutes 仅支持 1/5/15/30/60")
	}
	if err != nil {
		return nil, "", fmt.Errorf("获取盘中K线失败: %w", err)
	}
	source := "tdx_intraday"
	if isIndex {
		source = "tdx_intraday_index"
	}
	return historyRowsFromProtocol(resp.List), source, nil
}

func historyRowsFromProtocol(list []*protocol.Kline) []historyBarRow {
	rows := make([]historyBarRow, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		rows = append(rows, historyBarRow{
			Time:   item.Time,
			Open:   item.Open.Float64(),
			High:   item.High.Float64(),
			Low:    item.Low.Float64(),
			Close:  item.Close.Float64(),
			Volume: item.Volume,
			Amount: item.Amount.Float64(),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Time.Before(rows[j].Time) })
	return rows
}

func historyRowsFromExtend(list []*extend.Kline) []historyBarRow {
	rows := make([]historyBarRow, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		rows = append(rows, historyBarRow{
			Time:   time.Unix(item.Date, 0),
			Open:   item.Open.Float64(),
			High:   item.High.Float64(),
			Low:    item.Low.Float64(),
			Close:  item.Close.Float64(),
			Volume: item.Volume,
			Amount: item.Amount.Float64(),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Time.Before(rows[j].Time) })
	return rows
}

func aggregateHistoryBarRows(rows []historyBarRow, frequency string) ([]historyBarRow, error) {
	if frequency != "week" && frequency != "month" {
		return nil, fmt.Errorf("frequency 仅支持 day/week/month")
	}
	if len(rows) == 0 {
		return nil, nil
	}

	grouped := make([]historyBarRow, 0)
	current := historyBarRow{}
	currentKey := ""
	flush := func() {
		if currentKey != "" {
			grouped = append(grouped, current)
		}
	}
	for _, row := range rows {
		key := row.Time.Format("2006-01")
		if frequency == "week" {
			year, week := row.Time.ISOWeek()
			key = fmt.Sprintf("%d-%02d", year, week)
		}
		if key != currentKey {
			flush()
			currentKey = key
			current = row
			continue
		}
		if row.High > current.High {
			current.High = row.High
		}
		if row.Low < current.Low {
			current.Low = row.Low
		}
		current.Close = row.Close
		current.Time = row.Time
		current.Volume += row.Volume
		current.Amount += row.Amount
	}
	flush()
	return grouped, nil
}

func filterHistoryBarRows(rows []historyBarRow, startDate, endDate time.Time, count int) ([]historyBarRow, error) {
	filtered := make([]historyBarRow, 0, len(rows))
	for _, row := range rows {
		if !startDate.IsZero() && row.Time.Before(startDate) {
			continue
		}
		if !endDate.IsZero() && row.Time.After(endDate) {
			continue
		}
		filtered = append(filtered, row)
	}
	if count > 0 {
		if count < len(filtered) {
			filtered = filtered[len(filtered)-count:]
		}
	}
	if len(filtered) == 0 {
		return nil, errors.New("指定条件下无历史 bars 数据")
	}
	return filtered, nil
}

func filterIntradayBarRows(rows []historyBarRow, tradingDate time.Time) ([]historyBarRow, error) {
	target := tradingDate.Format("20060102")
	filtered := make([]historyBarRow, 0, len(rows))
	for _, row := range rows {
		if row.Time.Format("20060102") == target {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) == 0 {
		return nil, errors.New("指定 trading_date 无盘中数据，exact 模式不回退最近交易日")
	}
	return filtered, nil
}

func buildAdjustmentFactorItems(factors []*extend.THSFactor, startDate, endDate time.Time) []providerAdjustmentFactor {
	items := make([]providerAdjustmentFactor, 0, len(factors))
	for _, factor := range factors {
		if factor == nil {
			continue
		}
		tradeDate := time.Unix(factor.Date, 0)
		if !startDate.IsZero() && tradeDate.Before(startDate) {
			continue
		}
		if !endDate.IsZero() && tradeDate.After(endDate) {
			continue
		}
		items = append(items, providerAdjustmentFactor{
			ExDate:         tradeDate.Format("2006-01-02"),
			ForwardFactor:  factor.QFactor,
			BackwardFactor: factor.HFactor,
		})
	}
	return items
}

func parseHistoryRangeParams(r *http.Request) (time.Time, time.Time, int, error) {
	startDate, endDate, count, err := parseHistoryRangeParamsAllowEmpty(r)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	if count > 0 {
		return time.Time{}, time.Time{}, count, nil
	}
	if startDate.IsZero() || endDate.IsZero() {
		return time.Time{}, time.Time{}, 0, errors.New("必须显式提供 start_date+end_date 或 count")
	}
	return startDate, endDate, 0, nil
}

func parseHistoryRangeParamsAllowEmpty(r *http.Request) (time.Time, time.Time, int, error) {
	count := parsePositiveInt(strings.TrimSpace(r.URL.Query().Get("count")))
	startRaw := strings.TrimSpace(r.URL.Query().Get("start_date"))
	endRaw := strings.TrimSpace(r.URL.Query().Get("end_date"))
	if count > 0 && (startRaw != "" || endRaw != "") {
		return time.Time{}, time.Time{}, 0, errors.New("count 与 start_date/end_date 不能同时传入")
	}
	if count > 0 {
		return time.Time{}, time.Time{}, count, nil
	}
	if startRaw == "" && endRaw == "" {
		return time.Time{}, time.Time{}, 0, nil
	}
	if startRaw == "" || endRaw == "" {
		return time.Time{}, time.Time{}, 0, errors.New("start_date 与 end_date 必须同时传入")
	}
	startDate, err := parseWorkdayDate(startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, 0, errors.New("start_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
	}
	endDate, err := parseWorkdayDate(endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, 0, errors.New("end_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
	}
	if endDate.Before(startDate) {
		return time.Time{}, time.Time{}, 0, errors.New("start_date 不能晚于 end_date")
	}
	return startDate, endDate, 0, nil
}

func parseAdjustMode(raw string) (string, error) {
	switch raw {
	case "bfq", "qfq", "hfq":
		return raw, nil
	default:
		return "", errors.New("adjust_mode 仅支持 bfq/qfq/hfq")
	}
}

func parseIntradayInterval(raw string) (int, error) {
	if raw == "" {
		return 1, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("interval_minutes 参数无效")
	}
	switch value {
	case 1, 5, 15, 30, 60:
		return value, nil
	default:
		return 0, errors.New("interval_minutes 仅支持 1/5/15/30/60")
	}
}

func parseBlockProviderKey(r *http.Request, requirement blockProviderKeyRequirement) (blockProviderKey, error) {
	key := blockProviderKey{
		Source:    strings.TrimSpace(r.URL.Query().Get("source")),
		BlockType: strings.TrimSpace(r.URL.Query().Get("block_type")),
		Name:      strings.TrimSpace(r.URL.Query().Get("name")),
	}
	if key.BlockType != "" {
		switch key.BlockType {
		case string(collectorpkg.BlockTypeIndustry), string(collectorpkg.BlockTypeConcept), string(collectorpkg.BlockTypeStyle), string(collectorpkg.BlockTypeIndexBlock):
		default:
			return blockProviderKey{}, errors.New("block_type 参数无效")
		}
	}
	missing := make([]string, 0, 3)
	if requirement.RequireSource && key.Source == "" {
		missing = append(missing, "source")
	}
	if requirement.RequireBlockType && key.BlockType == "" {
		missing = append(missing, "block_type")
	}
	if requirement.RequireName && key.Name == "" {
		missing = append(missing, "name")
	}
	if len(missing) > 0 {
		return blockProviderKey{}, fmt.Errorf("%s 为必填参数", strings.Join(missing, "、"))
	}
	return key, nil
}

func resolveSingleCodeModel(raw string) (*tdx.CodeModel, error) {
	models, err := resolveCodeModels([]string{raw})
	if err != nil {
		return nil, err
	}
	return models[0], nil
}

func resolveSingleFullCodeModel(raw string) (*tdx.CodeModel, error) {
	models, err := resolveFullCodeModels([]string{raw})
	if err != nil {
		return nil, err
	}
	return models[0], nil
}

func resolveFullCodeModels(rawCodes []string) ([]*tdx.CodeModel, error) {
	if len(rawCodes) == 0 {
		return nil, errors.New("full_codes 为必填参数")
	}
	allModels, err := getAllCodeModels()
	if err != nil {
		return nil, fmt.Errorf("获取证券信息失败: %w", err)
	}
	resolved := make([]*tdx.CodeModel, 0, len(rawCodes))
	for _, rawCode := range rawCodes {
		model, err := lookupFullCodeModel(rawCode, allModels)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, model)
	}
	if len(resolved) == 0 {
		return nil, errors.New("full_codes 为必填参数")
	}
	return resolved, nil
}

func resolveCodeModels(rawCodes []string) ([]*tdx.CodeModel, error) {
	if len(rawCodes) == 0 {
		return nil, errors.New("code 为必填参数")
	}
	allModels, err := getAllCodeModels()
	if err != nil {
		return nil, fmt.Errorf("获取证券信息失败: %w", err)
	}
	byFullCode := make(map[string]*tdx.CodeModel, len(allModels))
	byBareCode := make(map[string][]*tdx.CodeModel, len(allModels))
	for _, model := range allModels {
		if model == nil {
			continue
		}
		byFullCode[strings.ToLower(model.FullCode())] = model
		byBareCode[strings.ToLower(model.Code)] = append(byBareCode[strings.ToLower(model.Code)], model)
	}

	resolved := make([]*tdx.CodeModel, 0, len(rawCodes))
	for _, rawCode := range rawCodes {
		code := strings.ToLower(strings.TrimSpace(rawCode))
		if code == "" {
			continue
		}
		if model := byFullCode[code]; model != nil {
			resolved = append(resolved, model)
			continue
		}
		bare := bareCode(code)
		matches := byBareCode[bare]
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("证券未找到: %s", rawCode)
		case 1:
			resolved = append(resolved, matches[0])
		default:
			return nil, fmt.Errorf("证券代码存在歧义，请使用 full_code: %s", rawCode)
		}
	}
	if len(resolved) == 0 {
		return nil, errors.New("code 为必填参数")
	}
	return resolved, nil
}

func lookupFullCodeModel(raw string, allModels []*tdx.CodeModel) (*tdx.CodeModel, error) {
	fullCode := strings.ToLower(strings.TrimSpace(raw))
	if fullCode == "" {
		return nil, errors.New("full_code 为必填参数")
	}
	if bareCode(fullCode) == fullCode {
		return nil, fmt.Errorf("full_code 参数无效，请传完整市场前缀代码，例如 sh600000：%s", raw)
	}
	for _, model := range allModels {
		if model == nil {
			continue
		}
		if strings.ToLower(model.FullCode()) == fullCode {
			return model, nil
		}
	}
	return nil, fmt.Errorf("证券未找到: %s", raw)
}

func providerExchange(model *tdx.CodeModel) string {
	return strings.ToUpper(strings.TrimSpace(model.Exchange))
}

func modelAssetType(model *tdx.CodeModel) string {
	switch {
	case protocol.IsStock(model.FullCode()):
		return "stock"
	case protocol.IsETF(model.FullCode()):
		return "etf"
	case protocol.IsIndex(model.FullCode()):
		return "index"
	default:
		return "other"
	}
}

func getBlockServiceForProvider() *collectorpkg.BlockService {
	if collectorRuntime == nil {
		return nil
	}
	return collectorRuntime.BlockService()
}

func stockTickToProviderMap(tick collectorpkg.StockTick) map[string]interface{} {
	return map[string]interface{}{
		"code":          bareCode(tick.Code),
		"full_code":     tick.Code,
		"name":          tick.Name,
		"exchange":      strings.ToUpper(tick.Exchange),
		"asset_type":    tick.AssetType,
		"price":         tick.Last,
		"prev_close":    tick.PreClose,
		"open":          tick.Open,
		"high":          tick.High,
		"low":           tick.Low,
		"change":        roundFloat(tick.Last-tick.PreClose, 3),
		"change_pct":    tick.PctChange,
		"volume":        tick.Volume,
		"amount":        tick.Amount,
		"is_limit_up":   tick.IsLimitUp,
		"is_limit_down": tick.IsLimitDown,
	}
}

func normalizeQuoteSnapshot(item providerQuoteSnapshot) providerQuoteSnapshot {
	if item.Price != 0 {
		return item
	}
	if item.Change == 0 && item.ChangePct == 0 && item.Volume == 0 && item.Amount == 0 {
		return item
	}
	item.Change = 0
	item.ChangePct = 0
	item.Volume = 0
	item.Amount = 0
	if item.StatusReason != "" {
		item.StatusReason += "；"
	}
	item.StatusReason += "price 为 0 时已将涨跌额、涨跌幅、成交量、成交额归零"
	return item
}

func bareCode(fullCode string) string {
	code := strings.ToLower(strings.TrimSpace(fullCode))
	for _, prefix := range []string{"sh", "sz", "bj"} {
		if strings.HasPrefix(code, prefix) && len(code) > 2 {
			return code[2:]
		}
	}
	return code
}

func compactDateToISO(raw string) string {
	raw = normalizeDateString(raw)
	if raw == "" {
		return ""
	}
	return raw[:4] + "-" + raw[4:6] + "-" + raw[6:]
}

func tickSizeFromDecimal(decimal int8) float64 {
	if decimal <= 0 {
		return 1
	}
	return math.Pow10(-int(decimal))
}

func roundFloat(value float64, precision int) float64 {
	scale := math.Pow10(precision)
	return math.Round(value*scale) / scale
}

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
