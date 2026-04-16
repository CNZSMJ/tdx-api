package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/profinance"
	"github.com/injoyai/tdx/protocol"
)

// 扩展API接口

func handleSecurityStatus(w http.ResponseWriter, r *http.Request) {
	rawCode := strings.TrimSpace(r.URL.Query().Get("code"))
	if rawCode == "" {
		errorResponse(w, "code 为必填参数")
		return
	}

	// Resolve code → full code + name from code cache
	models, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "获取证券信息失败: "+err.Error())
		return
	}

	codeUpper := strings.ToUpper(rawCode)
	var matched *tdx.CodeModel
	for _, m := range models {
		if strings.ToUpper(m.Code) == codeUpper || strings.ToUpper(m.FullCode()) == codeUpper {
			matched = m
			break
		}
	}
	if matched == nil {
		errorResponse(w, fmt.Sprintf("证券未找到: %s", rawCode))
		return
	}

	fullCode := matched.FullCode()
	name := matched.Name
	at := classifySecurityAssetType(fullCode)

	nameUpper := strings.ToUpper(name)
	isST := strings.Contains(nameUpper, "ST")
	isDelistingRisk := strings.Contains(nameUpper, "*ST")

	// Determine suspension from real-time quote; if volume==0 and the market
	// is in session, the stock is very likely suspended. Outside trading hours
	// we can only report based on name patterns.
	isSuspended := false
	var quoteTime string
	quotes, qErr := client.GetQuote(rawCode)
	if qErr == nil && len(quotes) > 0 {
		q := quotes[0]
		quoteTime = q.ServerTime
		// During trading hours, a stock with zero total volume is suspended
		if q.TotalHand == 0 && q.K.Open.Float64() == 0 && q.K.Close.Float64() == 0 {
			isSuspended = true
		}
	}

	isTrading := !isSuspended && !isDelistingRisk

	resp := map[string]interface{}{
		"code":              matched.Code,
		"full_code":         fullCode,
		"name":              name,
		"asset_type":        at,
		"is_trading":        isTrading,
		"is_suspended":      isSuspended,
		"is_st":             isST,
		"is_delisting_risk": isDelistingRisk,
	}
	if quoteTime != "" {
		resp["quote_time"] = quoteTime
	}
	resp["updated_at"] = time.Now().Format(time.RFC3339)
	resp["note"] = "is_suspended 基于盘中 volume==0 推断，非交易时段可能不准确；is_st/is_delisting_risk 基于证券名称模式匹配"

	successResponse(w, resp)
}

// classifySecurityAssetType mirrors classifyAssetType in server.go for the extended package.
func classifySecurityAssetType(fullCode string) string {
	switch {
	case protocol.IsStock(fullCode):
		return "stock"
	case protocol.IsETF(fullCode):
		return "etf"
	case protocol.IsIndex(fullCode):
		return "index"
	default:
		return "other"
	}
}

func handleGetProfile(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		errorResponse(w, "code 为必填参数")
		return
	}

	models, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "获取证券信息失败: "+err.Error())
		return
	}

	codeUpper := strings.ToUpper(code)
	for _, model := range models {
		if strings.ToUpper(model.Code) == codeUpper || strings.ToUpper(model.FullCode()) == codeUpper {
			fullCode := model.FullCode()
			result := map[string]interface{}{
				"security": map[string]interface{}{
					"code":       model.Code,
					"full_code":  fullCode,
					"name":       model.Name,
					"exchange":   strings.ToLower(model.Exchange),
					"asset_type": classifySecurityAssetType(fullCode),
					"decimal":    model.Decimal,
					"multiple":   model.Multiple,
				},
			}

			quote := map[string]interface{}{
				"available":   false,
				"source":      "realtime_quote",
				"is_realtime": false,
				"reason":      "realtime_quote_unavailable",
			}
			priceForValuation := 0.0
			priceSource := "unavailable"

			quotes, quoteErr := client.GetQuote(code)
			if quoteErr == nil && len(quotes) > 0 && quotes[0] != nil {
				quote := quotes[0]
				price := quote.K.Close.Float64()
				prevClose := quote.K.Last.Float64()
				open := quote.K.Open.Float64()
				high := quote.K.High.Float64()
				low := quote.K.Low.Float64()
				change := price - prevClose
				changePct := 0.0
				if prevClose != 0 {
					changePct = change / prevClose * 100
				}

				result["quote"] = map[string]interface{}{
					"available":     true,
					"source":        "realtime_quote",
					"is_realtime":   true,
					"price":         price,
					"prev_close":    prevClose,
					"open":          open,
					"high":          high,
					"low":           low,
					"change":        change,
					"change_pct":    changePct,
					"volume_shares": int64(quote.TotalHand) * int64(model.Multiple),
					"turnover_yuan": quote.Amount,
					"quote_time":    quote.ServerTime,
				}
				priceForValuation = price
				priceSource = "realtime_quote"
			} else {
				result["quote"] = quote
				if quoteErr != nil {
					log.Printf("profile realtime quote unavailable for %s: %v", code, quoteErr)
				}
			}

			if protocol.IsStock(fullCode) {
				var finance *protocol.FinanceInfo
				finance, financeErr := client.GetFinanceInfo(code)
				if financeErr != nil {
					log.Printf("profile finance fallback for %s: %v", code, financeErr)
				}
				proSnapshot := loadProfessionalFinanceSnapshot(r, code)
				fundamentals, valuation := buildStockProfileSections(priceForValuation, priceSource, finance, proSnapshot)
				if err := validateInvestmentGradeStockSnapshot(result["quote"], valuation); err != nil {
					errorResponse(w, err.Error())
					return
				}
				result["fundamentals"] = fundamentals
				result["valuation"] = valuation
			} else {
				result["fundamentals"] = unavailableProfileSection("fundamentals_not_supported_for_asset_type")
				result["valuation"] = unavailableProfileSection("valuation_not_supported_for_asset_type")
			}

			successResponse(w, result)
			return
		}
	}

	errorResponse(w, fmt.Sprintf("证券未找到: %s", code))
}

func unavailableProfileSection(reason string) map[string]interface{} {
	return map[string]interface{}{
		"available": false,
		"reason":    reason,
	}
}

func loadProfessionalFinanceSnapshot(r *http.Request, code string) *profinance.Snapshot {
	if proFinanceService == nil {
		return nil
	}
	snapshot, err := proFinanceService.LatestForCode(r.Context(), code)
	if err != nil {
		log.Printf("profile professional finance fallback for %s: %v", code, err)
		return nil
	}
	return snapshot
}

func buildStockProfileSections(price float64, priceSource string, finance *protocol.FinanceInfo, proSnapshot *profinance.Snapshot) (map[string]interface{}, map[string]interface{}) {
	hasFinance := finance != nil
	hasProfessional := proSnapshot != nil
	source := profileSourceLabel(hasFinance, hasProfessional)
	hasRealtimePrice := price > 0 && priceSource == "realtime_quote"

	fundamentals := map[string]interface{}{
		"available": hasFinance || hasProfessional,
		"source":    source,
	}
	valuation := map[string]interface{}{
		"available":    false,
		"source":       source,
		"price_source": priceSource,
	}
	if hasRealtimePrice {
		valuation["price"] = price
	}

	if !hasFinance && !hasProfessional {
		fundamentals["reason"] = "no_finance_snapshot"
		valuation["reason"] = "no_finance_snapshot"
		return fundamentals, valuation
	}

	if hasFinance {
		if finance.UpdatedDate > 0 {
			fundamentals["finance_updated_date"] = fmt.Sprintf("%08d", finance.UpdatedDate)
			valuation["finance_updated_date"] = fmt.Sprintf("%08d", finance.UpdatedDate)
		}
		if finance.Jingzichan > 0 {
			fundamentals["report_net_assets"] = finance.Jingzichan
		}
		if finance.Jinglirun != 0 {
			fundamentals["report_net_profit"] = finance.Jinglirun
		}
		if finance.Zhuyingshouru != 0 {
			fundamentals["report_revenue"] = finance.Zhuyingshouru
		}
	}
	if hasProfessional {
		if proSnapshot.ReportDate != "" {
			fundamentals["report_date"] = proSnapshot.ReportDate
			valuation["report_date"] = proSnapshot.ReportDate
		}
		if proSnapshot.SourceReportFile != "" {
			fundamentals["source_report_file"] = proSnapshot.SourceReportFile
		}
		if proSnapshot.NetProfitTTM != 0 {
			fundamentals["net_profit_ttm"] = proSnapshot.NetProfitTTM
		}
		if proSnapshot.RevenueTTMYuan != 0 {
			fundamentals["revenue_ttm"] = proSnapshot.RevenueTTMYuan
		}
		if proSnapshot.WeightedROE != 0 {
			fundamentals["weighted_roe"] = proSnapshot.WeightedROE
		}
	}

	totalShares := pickPositive(
		func() float64 {
			if proSnapshot != nil {
				return proSnapshot.TotalShares
			}
			return 0
		}(),
		func() float64 {
			if finance != nil {
				return finance.Zongguben
			}
			return 0
		}(),
	)
	floatShares := pickPositive(
		func() float64 {
			if proSnapshot != nil {
				return proSnapshot.FloatAShares
			}
			return 0
		}(),
		func() float64 {
			if finance != nil {
				return finance.Liutongguben
			}
			return 0
		}(),
	)
	bookValuePerShare := pickPositive(
		func() float64 {
			if proSnapshot != nil {
				return proSnapshot.BookValuePerShare
			}
			return 0
		}(),
		func() float64 {
			if finance != nil {
				return finance.Meigujingzichan
			}
			return 0
		}(),
	)
	if proSnapshot != nil && proSnapshot.BookValuePerShare != 0 {
		bookValuePerShare = proSnapshot.BookValuePerShare
	} else if finance != nil && finance.Meigujingzichan != 0 {
		bookValuePerShare = finance.Meigujingzichan
	}
	netProfitTTM := 0.0
	revenueTTM := 0.0
	if proSnapshot != nil {
		netProfitTTM = proSnapshot.NetProfitTTM
		revenueTTM = proSnapshot.RevenueTTMYuan
	}

	if totalShares > 0 {
		fundamentals["total_shares"] = totalShares
	}
	if floatShares > 0 {
		fundamentals["float_shares"] = floatShares
	}
	if bookValuePerShare != 0 {
		fundamentals["book_value_per_share_mrq"] = bookValuePerShare
		valuation["book_value_per_share_mrq"] = bookValuePerShare
	}

	missing := make([]string, 0, 4)
	if hasRealtimePrice && totalShares > 0 {
		marketCapTotal := price * totalShares
		valuation["market_cap_total"] = marketCapTotal
		valuation["available"] = true
		if netProfitTTM != 0 {
			valuation["eps_ttm"] = netProfitTTM / totalShares
			valuation["pe_ttm"] = marketCapTotal / netProfitTTM
		} else {
			missing = append(missing, "pe_ttm")
		}
		if revenueTTM != 0 {
			valuation["revenue_per_share_ttm"] = revenueTTM / totalShares
			valuation["ps_ttm"] = marketCapTotal / revenueTTM
		} else {
			missing = append(missing, "ps_ttm")
		}
	} else {
		missing = append(missing, "market_cap_total", "pe_ttm", "ps_ttm")
	}
	if hasRealtimePrice && floatShares > 0 {
		valuation["market_cap_float"] = price * floatShares
		valuation["available"] = true
	}
	if hasRealtimePrice && bookValuePerShare != 0 {
		valuation["pb_mrq"] = price / bookValuePerShare
		valuation["available"] = true
	} else {
		missing = append(missing, "pb_mrq")
	}
	if !hasRealtimePrice {
		missing = append(missing, "price", "market_cap_total", "market_cap_float", "pb_mrq", "pe_ttm", "ps_ttm")
		valuation["reason"] = "realtime_quote_required"
	}
	if len(missing) > 0 {
		valuation["missing_fields"] = uniqueStrings(missing)
	}
	if available, _ := valuation["available"].(bool); !available {
		if _, exists := valuation["reason"]; !exists {
			valuation["reason"] = "insufficient_inputs"
		}
	}
	return fundamentals, valuation
}

func validateInvestmentGradeStockSnapshot(quoteValue interface{}, valuation map[string]interface{}) error {
	quote, ok := quoteValue.(map[string]interface{})
	if !ok {
		return fmt.Errorf("投资级快照不可用: 缺少行情快照")
	}
	quoteAvailable, _ := quote["available"].(bool)
	if !quoteAvailable {
		reason, _ := quote["reason"].(string)
		if reason == "" {
			reason = "realtime_quote_unavailable"
		}
		return fmt.Errorf("投资级快照不可用: 实时行情缺失 (%s)", reason)
	}
	valuationAvailable, _ := valuation["available"].(bool)
	if !valuationAvailable {
		reason, _ := valuation["reason"].(string)
		if reason == "" {
			reason = "valuation_unavailable"
		}
		return fmt.Errorf("投资级快照不可用: 估值快照缺失 (%s)", reason)
	}
	return nil
}

func profileSourceLabel(hasFinance, hasProfessional bool) string {
	switch {
	case hasFinance && hasProfessional:
		return "tdx_raw_finance+tdx_professional_finance"
	case hasProfessional:
		return "tdx_professional_finance"
	case hasFinance:
		return "tdx_raw_finance"
	default:
		return "unavailable"
	}
}

func pickPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// 获取股票代码列表
func handleGetCodes(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")

	type CodesResponse struct {
		Total     int                 `json:"total"`
		Exchanges map[string]int      `json:"exchanges"`
		Codes     []map[string]string `json:"codes"`
	}

	resp := &CodesResponse{
		Exchanges: map[string]int{
			"sh": 0,
			"sz": 0,
			"bj": 0,
		},
		Codes: []map[string]string{},
	}

	allCodes, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "获取代码列表失败: "+err.Error())
		return
	}
	targetExchange := strings.ToLower(exchange)

	for _, model := range allCodes {
		fullCode := model.FullCode()
		if !protocol.IsStock(fullCode) {
			continue
		}
		exName := strings.ToLower(model.Exchange)
		resp.Exchanges[exName]++

		if targetExchange != "" && targetExchange != "all" && targetExchange != exName {
			continue
		}

		resp.Codes = append(resp.Codes, map[string]string{
			"code":     model.Code,
			"name":     model.Name,
			"exchange": exName,
		})
	}

	resp.Total = len(resp.Codes)

	successResponse(w, resp)
}

// 批量获取行情
func handleBatchQuote(w http.ResponseWriter, r *http.Request) {
	serveBatchQuoteSnapshots(w, r)
}

// 获取历史K线（指定范围，日/周/月K线使用前复权）
func handleGetKlineHistory(w http.ResponseWriter, r *http.Request) {
	serveHistoricalBars(w, r)
}

// 获取指数数据
func handleGetIndex(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	klineType := r.URL.Query().Get("type")
	limitStr := r.URL.Query().Get("limit")

	if code == "" {
		errorResponse(w, "指数代码不能为空")
		return
	}

	// 解析limit，默认100，最大800
	limit := uint16(100)
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			if l > 800 {
				l = 800
			}
			limit = uint16(l)
		}
	}

	var resp *protocol.KlineResp
	var err error

	// 根据类型选择对应的指数接口
	switch klineType {
	case "minute1":
		resp, err = client.GetIndex(protocol.TypeKlineMinute, code, 0, limit)
	case "minute5":
		resp, err = client.GetIndex(protocol.TypeKline5Minute, code, 0, limit)
	case "minute15":
		resp, err = client.GetIndex(protocol.TypeKline15Minute, code, 0, limit)
	case "minute30":
		resp, err = client.GetIndex(protocol.TypeKline30Minute, code, 0, limit)
	case "hour":
		resp, err = client.GetIndex(protocol.TypeKline60Minute, code, 0, limit)
	case "week":
		resp, err = client.GetIndexWeekAll(code)
		if resp != nil && len(resp.List) > int(limit) {
			resp.List = resp.List[:limit]
			resp.Count = limit
		}
	case "month":
		resp, err = client.GetIndexMonthAll(code)
		if resp != nil && len(resp.List) > int(limit) {
			resp.List = resp.List[:limit]
			resp.Count = limit
		}
	case "day":
		fallthrough
	default:
		resp, err = client.GetIndexDay(code, 0, limit)
	}

	if err != nil {
		errorResponse(w, fmt.Sprintf("获取指数数据失败: %v", err))
		return
	}

	successResponse(w, resp)
}

// 获取指数全部历史K线
func handleGetIndexAll(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		errorResponse(w, "指数代码不能为空")
		return
	}

	klineType := strings.TrimSpace(r.URL.Query().Get("type"))
	if klineType == "" {
		klineType = "day"
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	list, err := fetchIndexAll(code, klineType)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取指数历史数据失败: %v", err))
		return
	}

	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}

	successResponse(w, map[string]interface{}{
		"count": len(list),
		"list":  list,
	})
}

// 获取市场统计（宽度指标来自 Ticker 预聚合；未就绪时不返回误导性的旧逻辑数据）
func handleGetMarketStats(w http.ResponseWriter, r *http.Request) {
	ts := getTickerService()
	if ts == nil {
		successResponse(w, map[string]interface{}{
			"status":      "not_started",
			"status_hint": "Ticker 服务未初始化，系统可能仍在启动中",
		})
		return
	}
	if ts.UpdatedAt().IsZero() {
		if ts.Running() {
			successResponse(w, map[string]interface{}{
				"status":      "warming_up",
				"status_hint": "Ticker 已启动，正在等待首次行情数据采集完成",
			})
		} else {
			successResponse(w, map[string]interface{}{
				"status":      "out_of_session",
				"status_hint": "当前处于非交易时段或 Ticker 尚未启动",
			})
		}
		return
	}

	assetType, err := parseMarketStatsAssetType(r.URL.Query().Get("asset_type"))
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	resp := buildMarketStatsData(ts.GetAllStocks(), assetType)
	updatedAt := ts.UpdatedAt()
	resp["updated_at"] = updatedAt.Format(time.RFC3339)
	if age := time.Since(updatedAt); age < 10*time.Second {
		resp["status"] = "live"
	} else {
		resp["status"] = "stale"
		resp["status_hint"] = fmt.Sprintf("数据已过期 %s，当前可能处于非交易时段", age.Truncate(time.Second))
	}
	successResponse(w, resp)
}

// 获取各交易所证券数量
func handleGetMarketCount(w http.ResponseWriter, r *http.Request) {
	type ExchangeCount struct {
		Exchange string `json:"exchange"`
		Count    uint16 `json:"count"`
	}

	type Response struct {
		Total     uint32          `json:"total"`
		Exchanges []ExchangeCount `json:"exchanges"`
	}

	exchanges := []protocol.Exchange{protocol.ExchangeSH, protocol.ExchangeSZ, protocol.ExchangeBJ}
	names := map[protocol.Exchange]string{
		protocol.ExchangeSH: "sh",
		protocol.ExchangeSZ: "sz",
		protocol.ExchangeBJ: "bj",
	}

	resp := Response{
		Exchanges: make([]ExchangeCount, 0, len(exchanges)),
	}

	for _, ex := range exchanges {
		countResp, err := client.GetCount(ex)
		if err != nil {
			errorResponse(w, fmt.Sprintf("获取 %s 数量失败: %v", names[ex], err))
			return
		}
		resp.Exchanges = append(resp.Exchanges, ExchangeCount{
			Exchange: names[ex],
			Count:    countResp.Count,
		})
		resp.Total += uint32(countResp.Count)
	}

	successResponse(w, resp)
}

// 获取全部股票代码列表
func handleGetStockCodes(w http.ResponseWriter, r *http.Request) {
	if tdx.DefaultCodes == nil {
		errorResponse(w, "股票代码缓存未初始化")
		return
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	prefixParam := strings.TrimSpace(r.URL.Query().Get("prefix"))
	includePrefix := true
	if prefixParam != "" {
		includePrefix = strings.ToLower(prefixParam) != "false"
	}

	codes := tdx.DefaultCodes.GetStocks()
	if limit > 0 && len(codes) > limit {
		codes = codes[:limit]
	}

	if !includePrefix {
		for i, code := range codes {
			if len(code) > 2 {
				codes[i] = code[2:]
			}
		}
	}

	successResponse(w, map[string]interface{}{
		"count": len(codes),
		"list":  codes,
	})
}

// 获取全部ETF代码列表
func handleGetETFCodes(w http.ResponseWriter, r *http.Request) {
	if tdx.DefaultCodes == nil {
		errorResponse(w, "代码缓存未初始化")
		return
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	prefixParam := strings.TrimSpace(r.URL.Query().Get("prefix"))
	includePrefix := true
	if prefixParam != "" {
		includePrefix = strings.ToLower(prefixParam) != "false"
	}

	codes := tdx.DefaultCodes.GetETFs()
	if limit > 0 && len(codes) > limit {
		codes = codes[:limit]
	}

	if !includePrefix {
		for i, code := range codes {
			if len(code) > 2 {
				codes[i] = code[2:]
			}
		}
	}

	successResponse(w, map[string]interface{}{
		"count": len(codes),
		"list":  codes,
	})
}

// 获取全部核心指数代码列表
func handleGetIndexCodes(w http.ResponseWriter, r *http.Request) {
	if tdx.DefaultCodes == nil {
		errorResponse(w, "代码缓存未初始化")
		return
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))
	prefixParam := strings.TrimSpace(r.URL.Query().Get("prefix"))
	includePrefix := true
	if prefixParam != "" {
		includePrefix = strings.ToLower(prefixParam) != "false"
	}

	items := tdx.DefaultCodes.GetIndexModels()
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	codes := make([]string, 0, len(items))
	if !includePrefix {
		for _, item := range items {
			code := item.FullCode()
			if len(code) > 2 {
				codes = append(codes, code[2:])
			}
		}
	} else {
		for _, item := range items {
			codes = append(codes, item.FullCode())
		}
	}

	successResponse(w, map[string]interface{}{
		"count":  len(codes),
		"list":   codes,
		"items":  items,
		"source": tdx.DefaultCodes.GetIndexSource(),
	})
}

// 获取ETF列表
func handleGetETFList(w http.ResponseWriter, r *http.Request) {
	exchangeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("exchange")))
	limitStr := strings.TrimSpace(r.URL.Query().Get("limit"))
	limit := 0
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	models, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "获取ETF列表失败: "+err.Error())
		return
	}

	type ETF struct {
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		Exchange  string  `json:"exchange"`
		LastPrice float64 `json:"last_price"`
	}

	result := struct {
		Total int   `json:"total"`
		List  []ETF `json:"list"`
	}{List: []ETF{}}

	for _, model := range models {
		fullCode := model.FullCode()
		if !protocol.IsETF(fullCode) {
			continue
		}
		ex := strings.ToLower(model.Exchange)
		if exchangeFilter != "" && exchangeFilter != "all" && exchangeFilter != ex {
			continue
		}
		result.List = append(result.List, ETF{
			Code:      model.Code,
			Name:      model.Name,
			Exchange:  ex,
			LastPrice: model.LastPrice,
		})
		if limit > 0 && len(result.List) >= limit {
			break
		}
	}
	result.Total = len(result.List)
	successResponse(w, result)
}

// 获取历史分时成交（分页）
func handleGetTradeHistory(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	startStr := strings.TrimSpace(r.URL.Query().Get("start"))
	countStr := strings.TrimSpace(r.URL.Query().Get("count"))

	if code == "" || date == "" {
		errorResponse(w, "code 与 date 均为必填参数")
		return
	}

	start := 0
	if startStr != "" {
		if v, err := strconv.Atoi(startStr); err == nil && v >= 0 {
			start = v
		} else {
			errorResponse(w, "start 参数无效")
			return
		}
	}

	count := 2000
	if countStr != "" {
		if v, err := strconv.Atoi(countStr); err == nil && v > 0 {
			if v > 2000 {
				v = 2000
			}
			count = v
		} else {
			errorResponse(w, "count 参数无效")
			return
		}
	}

	resp, err := client.GetHistoryMinuteTrade(date, code, uint16(start), uint16(count))
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取历史分时成交失败: %v", err))
		return
	}

	successResponse(w, resp)
}

// 获取历史委托分布
func handleGetOrderHistory(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	dateValue := strings.TrimSpace(r.URL.Query().Get("date"))

	if code == "" || dateValue == "" {
		errorResponse(w, "code 与 date 均为必填参数")
		return
	}

	date, err := parseWorkdayDate(dateValue)
	if err != nil {
		errorResponse(w, "date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}

	resp, err := client.GetHistoryOrders(date.Format("20060102"), code)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取历史委托失败: %v", err))
		return
	}

	successResponse(w, struct {
		Date     string                   `json:"date"`
		Count    uint16                   `json:"Count"`
		PreClose protocol.Price           `json:"PreClose"`
		List     []*protocol.HistoryOrder `json:"List"`
	}{
		Date:     date.Format("20060102"),
		Count:    resp.Count,
		PreClose: resp.PreClose,
		List:     resp.List,
	})
}

// 获取全天分时成交
func handleGetMinuteTradeAll(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))

	if code == "" {
		errorResponse(w, "code 为必填参数")
		return
	}

	var (
		resp *protocol.TradeResp
		err  error
	)

	if date != "" {
		resp, err = client.GetHistoryMinuteTradeDay(date, code)
	} else {
		resp, err = client.GetMinuteTradeAll(code)
	}
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取分时成交失败: %v", err))
		return
	}

	successResponse(w, resp)
}

// 获取上市以来的全部历史分时成交
func handleGetTradeHistoryFull(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	startParam := strings.TrimSpace(r.URL.Query().Get("start_date"))
	endParam := strings.TrimSpace(r.URL.Query().Get("end_date"))
	beforeParam := strings.TrimSpace(r.URL.Query().Get("before"))
	includeToday := parseBool(strings.TrimSpace(r.URL.Query().Get("include_today")))

	if code == "" {
		errorResponse(w, "code 为必填参数")
		return
	}
	if manager == nil || manager.Workday == nil {
		errorResponse(w, "交易日模块未初始化")
		return
	}

	limit := parsePositiveInt(r.URL.Query().Get("limit"))

	var start time.Time
	var end time.Time
	var err error

	if startParam != "" {
		start, err = parseWorkdayDate(startParam)
		if err != nil {
			errorResponse(w, "start_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
			return
		}
	} else {
		start = time.Now().AddDate(0, 0, -30)
	}

	if beforeParam != "" {
		end, err = parseWorkdayDate(beforeParam)
		if err != nil {
			errorResponse(w, "before 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
			return
		}
	} else if endParam != "" {
		end, err = parseWorkdayDate(endParam)
		if err != nil {
			errorResponse(w, "end_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
			return
		}
	} else {
		end = time.Now()
	}

	if start.After(end) {
		start, end = end, start
	}

	historyEnd := end
	yesterday := time.Now().AddDate(0, 0, -1)
	if historyEnd.After(yesterday) {
		historyEnd = yesterday
	}

	type tradeItem struct {
		Time   string  `json:"time"`
		Price  float64 `json:"price"`
		Volume int     `json:"volume"`
		Status int     `json:"status"`
		Number int     `json:"number"`
	}

	items := []tradeItem{}
	truncated := false
	daysCovered := []string{}
	var lastErr error

	if !start.After(historyEnd) {
		manager.Workday.Range(
			time.Date(start.Year(), start.Month(), start.Day(), 15, 0, 0, 0, time.Local),
			time.Date(historyEnd.Year(), historyEnd.Month(), historyEnd.Day(), 15, 0, 0, 0, time.Local).Add(24*time.Hour),
			func(t time.Time) bool {
				dateStr := t.Format("20060102")
				resp, err := client.GetHistoryMinuteTradeDay(dateStr, code)
				if err != nil {
					lastErr = err
					return true
				}
				if resp == nil || len(resp.List) == 0 {
					return true
				}
				daysCovered = append(daysCovered, dateStr)
				for _, v := range resp.List {
					items = append(items, tradeItem{
						Time:   v.Time.Format(time.RFC3339),
						Price:  v.Price.Float64(),
						Volume: v.Volume,
						Status: v.Status,
						Number: v.Number,
					})
					if limit > 0 && len(items) >= limit {
						truncated = true
						return false
					}
				}
				return true
			},
		)
	}

	if includeToday && !truncated {
		now := time.Now()
		resp, err := client.GetMinuteTradeAll(code)
		if err == nil && resp != nil && len(resp.List) > 0 {
			dateStr := now.Format("20060102")
			daysCovered = append(daysCovered, dateStr)
			for _, v := range resp.List {
				items = append(items, tradeItem{
					Time:   v.Time.Format(time.RFC3339),
					Price:  v.Price.Float64(),
					Volume: v.Volume,
					Status: v.Status,
					Number: v.Number,
				})
				if limit > 0 && len(items) >= limit {
					truncated = true
					break
				}
			}
		} else if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil && len(items) == 0 {
		errorResponse(w, fmt.Sprintf("获取分时成交失败: %v", lastErr))
		return
	}

	successResponse(w, map[string]interface{}{
		"code":          code,
		"start_date":    start.Format("2006-01-02"),
		"end_date":      end.Format("2006-01-02"),
		"limit":         limit,
		"count":         len(items),
		"truncated":     truncated,
		"covered_dates": daysCovered,
		"list":          items,
	})
}

// 获取股票全部历史K线（通达信）
func handleGetKlineAllTDX(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	klineType := strings.TrimSpace(r.URL.Query().Get("type"))
	if klineType == "" {
		klineType = "day"
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"))

	list, err := fetchStockKlineAllTDX(code, klineType)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取K线失败: %v", err))
		return
	}

	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}

	respondKlineSuccess(w, "tdx", klineType, list)
}

// 获取股票全部历史K线（同花顺前复权）
func handleGetKlineAllTHS(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	klineType := strings.TrimSpace(r.URL.Query().Get("type"))
	if klineType == "" {
		klineType = "day"
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"))

	list, err := fetchStockKlineAllTHS(code, klineType)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取同花顺K线失败: %v", err))
		return
	}

	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}

	respondKlineSuccess(w, "ths", klineType, list)
}

// 获取交易日信息
func handleGetWorkday(w http.ResponseWriter, r *http.Request) {
	if manager == nil || manager.Workday == nil {
		errorResponse(w, "交易日模块未初始化")
		return
	}

	dateParam := strings.TrimSpace(r.URL.Query().Get("date"))
	countStr := strings.TrimSpace(r.URL.Query().Get("count"))

	count := 1
	if countStr != "" {
		if v, err := strconv.Atoi(countStr); err == nil {
			if v < 1 {
				v = 1
			}
			if v > 30 {
				v = 30
			}
			count = v
		}
	}

	target := time.Now()
	if dateParam != "" {
		parsed, err := parseWorkdayDate(dateParam)
		if err != nil {
			errorResponse(w, "date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
			return
		}
		target = parsed
	}

	isWorkday, err := resolveTradingDay(target)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	response := map[string]interface{}{
		"date": map[string]string{
			"iso":     target.Format("2006-01-02"),
			"numeric": target.Format("20060102"),
		},
		"is_workday": isWorkday,
		"next":       collectNeighborWorkdays(target, count, 1),
		"previous":   collectNeighborWorkdays(target, count, -1),
	}

	successResponse(w, response)
}

// 获取指定范围内的交易日列表
func handleGetWorkdayRange(w http.ResponseWriter, r *http.Request) {
	if manager == nil || manager.Workday == nil {
		errorResponse(w, "交易日模块未初始化")
		return
	}

	startParam := strings.TrimSpace(r.URL.Query().Get("start"))
	endParam := strings.TrimSpace(r.URL.Query().Get("end"))
	if startParam == "" || endParam == "" {
		errorResponse(w, "start 与 end 均为必填参数")
		return
	}

	startDate, err := parseWorkdayDate(startParam)
	if err != nil {
		errorResponse(w, "start 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}
	endDate, err := parseWorkdayDate(endParam)
	if err != nil {
		errorResponse(w, "end 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}
	if endDate.Before(startDate) {
		errorResponse(w, "end 必须大于或等于 start")
		return
	}

	list := make([]map[string]string, 0)
	for day := startDate; !day.After(endDate); day = day.AddDate(0, 0, 1) {
		isWorkday, err := resolveTradingDay(day)
		if err != nil {
			if day.After(manager.Workday.Latest()) {
				errorResponse(w, err.Error())
				return
			}
			log.Printf("交易日判断失败: date=%s err=%v", day.Format("2006-01-02"), err)
			continue
		}
		if isWorkday {
			list = append(list, map[string]string{
				"iso":     day.Format("2006-01-02"),
				"numeric": day.Format("20060102"),
			})
		}
	}

	if len(list) == 0 && !endDate.After(manager.Workday.Latest()) {
		if err := manager.Workday.Update(); err == nil {
			manager.Workday.Range(startDate, endDate.AddDate(0, 0, 1), func(t time.Time) bool {
				list = append(list, map[string]string{
					"iso":     t.Format("2006-01-02"),
					"numeric": t.Format("20060102"),
				})
				return true
			})
		} else {
			log.Printf("刷新交易日失败: %v", err)
		}
	}

	successResponse(w, map[string]interface{}{
		"count": len(list),
		"list":  list,
	})
}

// 获取服务器状态
func handleGetServerStatus(w http.ResponseWriter, r *http.Request) {
	type ServerStatus struct {
		Status    string `json:"status"`
		Connected bool   `json:"connected"`
		Version   string `json:"version"`
		Uptime    string `json:"uptime"`
	}

	status := &ServerStatus{
		Status:    "running",
		Connected: true,
		Version:   "1.0.0",
		Uptime:    "unknown",
	}

	successResponse(w, status)
}

// 健康检查
func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healthy",
		"time":   time.Now().Unix(),
	})
}

// 基于K线的收益率计算
func handleGetIncome(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	startParam := strings.TrimSpace(r.URL.Query().Get("start_date"))
	daysParam := strings.TrimSpace(r.URL.Query().Get("days"))

	if code == "" {
		errorResponse(w, "code 为必填参数")
		return
	}
	if startParam == "" {
		errorResponse(w, "start_date 为必填参数")
		return
	}

	startDate, err := parseFullDate(startParam)
	if err != nil {
		errorResponse(w, "start_date 格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}

	dayOffsets := parseDaysParam(daysParam)
	if len(dayOffsets) == 0 {
		dayOffsets = []int{5, 10, 20, 60, 120}
	}

	resp, err := getQfqKlineDay(code)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取K线失败: %v", err))
		return
	}
	if resp == nil || len(resp.List) == 0 {
		successResponse(w, map[string]interface{}{
			"count": 0,
			"list":  []interface{}{},
		})
		return
	}

	klines := buildExtendKlines(code, resp.List)
	incomes := extend.DoIncomes(klines, startDate, dayOffsets...)

	list := make([]map[string]interface{}, 0, len(incomes))
	for _, income := range incomes {
		if income == nil {
			continue
		}
		list = append(list, map[string]interface{}{
			"offset":    income.Offset,
			"time":      income.Time.Format(time.RFC3339),
			"rise":      income.Rise().Float64(),
			"rise_rate": income.RiseRate(),
			"source": map[string]float64{
				"open":  income.Source.Open.Float64(),
				"high":  income.Source.High.Float64(),
				"low":   income.Source.Low.Float64(),
				"close": income.Source.Close.Float64(),
			},
			"current": map[string]float64{
				"open":  income.Current.Open.Float64(),
				"high":  income.Current.High.Float64(),
				"low":   income.Current.Low.Float64(),
				"close": income.Current.Close.Float64(),
			},
		})
	}

	successResponse(w, map[string]interface{}{
		"count": len(list),
		"list":  list,
	})
}

func getAllCodeModels() ([]*tdx.CodeModel, error) {
	if tdx.DefaultCodes != nil {
		if list, err := tdx.DefaultCodes.GetCodes(true); err == nil && len(list) > 0 {
			return list, nil
		} else if err != nil {
			log.Printf("从数据库读取代码失败: %v", err)
		}
	}

	aggregate := []*tdx.CodeModel{}
	for _, ex := range []protocol.Exchange{protocol.ExchangeSH, protocol.ExchangeSZ, protocol.ExchangeBJ} {
		resp, err := client.GetCodeAll(ex)
		if err != nil || resp == nil {
			if err != nil {
				log.Printf("从服务器获取代码失败(%s): %v", ex.String(), err)
			}
			continue
		}
		for _, v := range resp.List {
			aggregate = append(aggregate, &tdx.CodeModel{
				Name:      v.Name,
				Code:      v.Code,
				Exchange:  ex.String(),
				Multiple:  v.Multiple,
				Decimal:   v.Decimal,
				LastPrice: v.LastPrice,
			})
		}
	}

	return aggregate, nil
}

func parseWorkdayDate(value string) (time.Time, error) {
	layouts := []string{"20060102", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %s", value)
}

func collectNeighborWorkdays(base time.Time, count int, step int) []map[string]string {
	result := make([]map[string]string, 0, count)
	if manager == nil || manager.Workday == nil || step == 0 {
		return result
	}
	cursor := base
	attempts := 0
	maxAttempts := 366
	for len(result) < count && attempts < maxAttempts {
		attempts++
		cursor = cursor.AddDate(0, 0, step)
		isWorkday, err := resolveTradingDay(cursor)
		if err != nil {
			break
		}
		if isWorkday {
			result = append(result, map[string]string{
				"iso":     cursor.Format("2006-01-02"),
				"numeric": cursor.Format("20060102"),
			})
		}
	}
	return result
}

func parsePositiveInt(value string) int {
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func parseFullDate(value string) (time.Time, error) {
	t, err := parseWorkdayDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 15, 0, 0, 0, t.Location()), nil
}

func parseDaysParam(value string) []int {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	days := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n > 0 {
			days = append(days, n)
		}
	}
	return days
}

func buildExtendKlines(code string, list []*protocol.Kline) extend.Klines {
	ks := make(extend.Klines, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		ks = append(ks, &extend.Kline{
			Code:   code,
			Date:   item.Time.Unix(),
			Open:   item.Open,
			High:   item.High,
			Low:    item.Low,
			Close:  item.Close,
			Volume: item.Volume,
			Amount: item.Amount,
		})
	}
	sort.Slice(ks, func(i, j int) bool {
		return ks[i].Date < ks[j].Date
	})
	return ks
}

// 获取板块列表
func handleGetBlocks(w http.ResponseWriter, r *http.Request) {
	serveBlocks(w, r)
}

// 获取板块成份股
func handleGetBlockMembers(w http.ResponseWriter, r *http.Request) {
	serveBlockMembers(w, r)
}

// 获取个股所属板块
func handleGetStockBlocks(w http.ResponseWriter, r *http.Request) {
	serveStockBlocks(w, r)
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func fetchStockKlineAllTDX(code, klineType string) ([]*protocol.Kline, error) {
	switch strings.ToLower(klineType) {
	case "minute1":
		resp, err := client.GetKlineMinuteAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute5":
		resp, err := client.GetKline5MinuteAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute15":
		resp, err := client.GetKline15MinuteAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute30":
		resp, err := client.GetKline30MinuteAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "hour":
		resp, err := client.GetKlineHourAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "day":
		resp, err := client.GetKlineDayAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "week":
		resp, err := client.GetKlineWeekAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "month":
		resp, err := client.GetKlineMonthAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "quarter":
		resp, err := client.GetKlineQuarterAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "year":
		resp, err := client.GetKlineYearAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	default:
		return nil, fmt.Errorf("不支持的K线类型: %s", klineType)
	}
}

func fetchStockKlineAllTHS(code, klineType string) ([]*protocol.Kline, error) {
	resp, err := getQfqKlineDay(code)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(klineType) {
	case "", "day":
		return resp.List, nil
	case "week":
		return convertToWeekKline(resp).List, nil
	case "month":
		return convertToMonthKline(resp).List, nil
	default:
		return nil, fmt.Errorf("同花顺接口暂仅支持 type=day/week/month")
	}
}

func respondKlineSuccess(w http.ResponseWriter, source, klineType string, list []*protocol.Kline) {
	kType := strings.ToLower(klineType)
	meta := map[string]interface{}{
		"source": source,
		"type":   kType,
	}

	switch source {
	case "tdx":
		meta["batch_limit"] = 800
		meta["notes"] = []string{
			"通达信单次底层请求最多返回 800 条数据，服务端已顺序拼接全量结果",
			"对于上市时间较长的标的，请预估调用耗时（通常 1-5 秒），客户端可增加超时时间",
		}
	case "ths":
		meta["batch_limit"] = len(list)
		meta["notes"] = []string{
			"同花顺接口一次性返回前复权数据，响应时长取决于网络与标的数据量（通常 2-8 秒）",
			"建议调用方在 Python 等客户端中设置 ≥10 秒超时时间，并准备兜底策略",
		}
	default:
		meta["notes"] = []string{"未知数据源，请检查 source 参数"}
	}

	successResponse(w, map[string]interface{}{
		"count": len(list),
		"list":  list,
		"meta":  meta,
	})
}

func fetchIndexAll(code, klineType string) ([]*protocol.Kline, error) {
	switch strings.ToLower(klineType) {
	case "minute1":
		resp, err := client.GetIndexAll(protocol.TypeKlineMinute, code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute5":
		resp, err := client.GetIndexAll(protocol.TypeKline5Minute, code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute15":
		resp, err := client.GetIndexAll(protocol.TypeKline15Minute, code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "minute30":
		resp, err := client.GetIndexAll(protocol.TypeKline30Minute, code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "hour":
		resp, err := client.GetIndexAll(protocol.TypeKline60Minute, code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "week":
		resp, err := client.GetIndexWeekAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "month":
		resp, err := client.GetIndexMonthAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "quarter":
		resp, err := client.GetIndexQuarterAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "year":
		resp, err := client.GetIndexYearAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	case "day":
		fallthrough
	default:
		resp, err := client.GetIndexDayAll(code)
		if err != nil {
			return nil, err
		}
		return resp.List, nil
	}
}

// ─── 板块排名 & 板块内个股排名 ─────────────────────────────

func getTickerService() *collectorpkg.TickerService {
	if collectorRuntime == nil {
		return nil
	}
	return collectorRuntime.TickerService()
}

func getSignalService() *collectorpkg.SignalService {
	if collectorRuntime == nil {
		return nil
	}
	return collectorRuntime.SignalService()
}

func handleMarketScreen(w http.ResponseWriter, r *http.Request) {
	ts := getTickerService()
	if ts == nil {
		successResponse(w, map[string]interface{}{
			"status": "not_started", "status_hint": "Ticker 服务未初始化",
			"count": 0, "list": []interface{}{},
		})
		return
	}
	if ts.UpdatedAt().IsZero() {
		if ts.Running() {
			successResponse(w, map[string]interface{}{
				"status": "warming_up", "status_hint": "等待首次行情数据",
				"count": 0, "list": []interface{}{},
			})
		} else {
			successResponse(w, map[string]interface{}{
				"status": "out_of_session", "status_hint": "Ticker 尚未启动或无数据",
				"count": 0, "list": []interface{}{},
			})
		}
		return
	}

	sortBy := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortBy == "" {
		sortBy = "change_pct"
	}
	order := strings.TrimSpace(r.URL.Query().Get("order"))
	if order == "" {
		order = "desc"
	}
	filter := strings.TrimSpace(r.URL.Query().Get("filter"))
	assetType := strings.TrimSpace(r.URL.Query().Get("asset_type"))
	if assetType == "" {
		assetType = "stock"
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	ticks, filterNote := ts.MarketScreen(sortBy, order, filter, assetType, limit)
	list := make([]map[string]interface{}, 0, len(ticks))
	for i := range ticks {
		item := stockTickToScreenMap(&ticks[i])
		switch filter {
		case "limit_up":
			if p := ts.GetLimitUpPublic(ticks[i].Code); p != nil {
				mergeLimitPublic(item, p, true)
			}
		case "limit_down":
			if p := ts.GetLimitDownPublic(ticks[i].Code); p != nil {
				mergeLimitPublic(item, p, false)
			}
		}
		list = append(list, item)
	}

	resp := map[string]interface{}{
		"count": len(list),
		"list":  list,
	}
	if filterNote != "" {
		resp["filter_note"] = filterNote
	}
	addTickerMeta(resp, ts)
	successResponse(w, resp)
}

func stockTickToScreenMap(t *collectorpkg.StockTick) map[string]interface{} {
	return map[string]interface{}{
		"code": t.Code, "name": t.Name, "exchange": t.Exchange, "asset_type": t.AssetType,
		"price": t.Last, "change_pct": t.PctChange, "price_change": t.PriceChange,
		"volume": t.Volume, "amount": t.Amount, "amplitude": t.Amplitude,
		"is_limit_up": t.IsLimitUp, "is_limit_down": t.IsLimitDown,
	}
}

func mergeLimitPublic(item map[string]interface{}, p *collectorpkg.LimitSidePublic, isUp bool) {
	if p == nil {
		return
	}
	if p.FirstSeen != "" {
		item["limit_first_seen"] = p.FirstSeen
		if p.FirstSeenApprox {
			item["limit_first_seen_approx"] = true
		}
	}
	if p.LastSeen != "" {
		item["limit_last_seen"] = p.LastSeen
	}
	item["limit_break_count"] = p.BreakCount
	if isUp && p.Bid1Volume > 0 {
		item["bid1_volume"] = p.Bid1Volume
	}
	if !isUp && p.Ask1Volume > 0 {
		item["ask1_volume"] = p.Ask1Volume
	}
}

func handleMarketSignal(w http.ResponseWriter, r *http.Request) {
	ss := getSignalService()
	if ss == nil {
		errorResponse(w, "Signal 服务未初始化")
		return
	}
	snap, apiStatus := ss.Snapshot()
	typeFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("type")))
	if typeFilter == "" {
		typeFilter = "all"
	}

	resp := map[string]interface{}{
		"status":           apiStatus,
		"updated_at":       nil,
		"scan_duration_ms": snap.ScanDurationMs,
		"new_high":         snap.NewHigh,
		"new_low":          snap.NewLow,
		"volume_spike":     snap.VolumeSpike,
	}
	if !snap.UpdatedAt.IsZero() {
		resp["updated_at"] = snap.UpdatedAt.Format(time.RFC3339)
	}
	switch typeFilter {
	case "new_high":
		resp["list"] = snap.NewHigh
		resp["count"] = len(snap.NewHigh)
	case "new_low":
		resp["list"] = snap.NewLow
		resp["count"] = len(snap.NewLow)
	case "volume_spike":
		resp["list"] = snap.VolumeSpike
		resp["count"] = len(snap.VolumeSpike)
	case "all":
		resp["count"] = len(snap.NewHigh) + len(snap.NewLow) + len(snap.VolumeSpike)
	default:
		errorResponse(w, "type 参数无效，支持 all|new_high|new_low|volume_spike")
		return
	}
	if apiStatus == "not_ready" {
		resp["status_hint"] = "首轮 K 线扫描尚未完成，请稍后重试"
	}
	if apiStatus == "scanning" {
		resp["status_hint"] = "正在扫描中，以下为上一轮完整结果"
	}
	if apiStatus == "stale" {
		resp["status_hint"] = "结果已超过新鲜度阈值，可能过期"
	}
	successResponse(w, resp)
}

func handleBlockRanking(w http.ResponseWriter, r *http.Request) {
	serveBlockRanking(w, r)
}

func handleBlockStocks(w http.ResponseWriter, r *http.Request) {
	serveBlockStocks(w, r)
}

func handleTickerStatus(w http.ResponseWriter, _ *http.Request) {
	ts := getTickerService()
	if ts == nil {
		errorResponse(w, "实时行情服务未初始化")
		return
	}
	resp := map[string]interface{}{
		"running": ts.Running(),
	}
	addTickerMeta(resp, ts)
	successResponse(w, resp)
}

// addTickerMeta appends updated_at and status hint to an API response.
func addTickerMeta(resp map[string]interface{}, ts *collectorpkg.TickerService) {
	updatedAt := ts.UpdatedAt()
	if updatedAt.IsZero() {
		resp["updated_at"] = nil
		if ts.Running() {
			resp["status"] = "waiting"
			resp["status_hint"] = "Ticker 已启动，等待盘中时段(09:15-15:05)开始采集"
		} else {
			resp["status"] = "not_started"
			resp["status_hint"] = "Ticker 尚未启动，服务可能仍在初始化中"
		}
	} else {
		resp["updated_at"] = updatedAt.Format(time.RFC3339)
		age := time.Since(updatedAt)
		if age < 10*time.Second {
			resp["status"] = "live"
		} else {
			resp["status"] = "stale"
			resp["status_hint"] = fmt.Sprintf("数据已过期 %s，当前可能处于非交易时段", age.Truncate(time.Second))
		}
	}
}
