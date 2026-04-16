package collector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tdx "github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

var _ Provider = (*TDXProvider)(nil)

const (
	tdxProviderMaxAttempts = 5
	tdxProviderRetryDelay  = 800 * time.Millisecond
	tdxClientTimeout       = 30 * time.Second
)

type TDXProvider struct {
	manage *tdx.Manage
	client *tdx.Client
}

func NewTDXProvider(manage *tdx.Manage, client *tdx.Client) *TDXProvider {
	return &TDXProvider{
		manage: manage,
		client: client,
	}
}

func (p *TDXProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	_ = ctx
	if query.Refresh {
		items, err := p.refreshInstruments()
		if err == nil {
			return items, nil
		}
		if cached, cachedErr := p.cachedInstruments(query); cachedErr == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	return p.cachedInstruments(query)
}

func (p *TDXProvider) cachedInstruments(query InstrumentQuery) ([]Instrument, error) {
	if p.manage == nil || p.manage.Codes == nil {
		return nil, errors.New("tdx provider requires manage.Codes for cached instruments")
	}

	filters := make(map[AssetType]bool)
	for _, kind := range query.AssetTypes {
		filters[kind] = true
	}
	includeAll := len(filters) == 0

	items := make([]Instrument, 0, 1024)
	appendModel := func(kind AssetType, model *tdx.CodeModel, source string) bool {
		if model == nil {
			return true
		}
		items = append(items, Instrument{
			Code:      model.FullCode(),
			Name:      model.Name,
			Exchange:  model.Exchange,
			AssetType: kind,
			Multiple:  model.Multiple,
			Decimal:   model.Decimal,
			LastPrice: PriceMilli(model.LastPrice * 1000),
			Source:    source,
		})
		return query.Limit <= 0 || len(items) < query.Limit
	}

	if includeAll || filters[AssetTypeStock] {
		for _, code := range p.manage.Codes.GetStocks() {
			if !appendModel(AssetTypeStock, p.manage.Codes.Get(code), "codes") {
				return items, nil
			}
		}
	}
	if includeAll || filters[AssetTypeETF] {
		for _, code := range p.manage.Codes.GetETFs() {
			if !appendModel(AssetTypeETF, p.manage.Codes.Get(code), "codes") {
				return items, nil
			}
		}
	}
	if includeAll || filters[AssetTypeIndex] {
		for _, model := range p.manage.Codes.GetIndexModels() {
			if !appendModel(AssetTypeIndex, model, p.manage.Codes.GetIndexSource()) {
				return items, nil
			}
		}
	}

	return items, nil
}

func (p *TDXProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	_ = ctx
	if query.Refresh {
		items, err := p.refreshTradingDays(ctx, query)
		if err == nil {
			return items, nil
		}
		if cached, cachedErr := p.cachedTradingDays(query); cachedErr == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	return p.cachedTradingDays(query)
}

func (p *TDXProvider) cachedTradingDays(query TradingDayQuery) ([]TradingDay, error) {
	if p.manage == nil || p.manage.Workday == nil {
		return nil, errors.New("tdx provider requires manage.Workday for cached trading days")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.Start.Before(query.End) {
		return nil, errors.New("trading day query requires start before end")
	}
	items := make([]TradingDay, 0, 256)
	start := query.Start
	if start.IsZero() {
		start = time.Date(1990, 12, 19, 0, 0, 0, 0, time.Local)
	}
	end := query.End
	if end.IsZero() {
		end = time.Date(2100, 1, 1, 0, 0, 0, 0, time.Local)
	}
	p.manage.Workday.Range(start, end, func(t time.Time) bool {
		items = append(items, TradingDay{
			Date: t.Format("20060102"),
			Time: t,
		})
		return true
	})
	return items, nil
}

func (p *TDXProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	_ = ctx
	if p.manage == nil || p.manage.Workday == nil {
		return false, errors.New("tdx provider requires manage.Workday for trading day checks")
	}
	return p.manage.Workday.Is(day), nil
}

func (p *TDXProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	var resp protocol.QuotesResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = client.GetQuote(codes...)
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]QuoteSnapshot, 0, len(resp))
	for _, quote := range resp {
		if quote == nil {
			continue
		}
		item := QuoteSnapshot{
			Code:        protocol.AddPrefix(quote.Code),
			Name:        p.lookupName(protocol.AddPrefix(quote.Code)),
			Exchange:    quote.Exchange.String(),
			AssetType:   detectAssetType(protocol.AddPrefix(quote.Code)),
			ServerTime:  quote.ServerTime,
			PreClose:    priceFromProtocol(quote.K.Last),
			Open:        priceFromProtocol(quote.K.Open),
			High:        priceFromProtocol(quote.K.High),
			Low:         priceFromProtocol(quote.K.Low),
			Last:        priceFromProtocol(quote.K.Close),
			VolumeHand:  int64(quote.TotalHand),
			AmountYuan:  quote.Amount,
			InsideHand:  int64(quote.InsideDish),
			OutsideHand: int64(quote.OuterDisc),
			BuyLevels:   make([]QuoteLevel, 0, len(quote.BuyLevel)),
			SellLevels:  make([]QuoteLevel, 0, len(quote.SellLevel)),
		}
		for _, level := range quote.BuyLevel {
			item.BuyLevels = append(item.BuyLevels, QuoteLevel{
				Price:  priceFromProtocol(level.Price),
				Number: level.Number,
			})
		}
		for _, level := range quote.SellLevel {
			item.SellLevels = append(item.SellLevels, QuoteLevel{
				Price:  priceFromProtocol(level.Price),
				Number: level.Number,
			})
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *TDXProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	if query.Code == "" {
		return nil, errors.New("minute query requires code")
	}

	var resp *protocol.MinuteResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		if query.Date == "" {
			resp, err = client.GetMinute(query.Code)
		} else {
			resp, err = client.GetHistoryMinute(query.Date, query.Code)
		}
		return err
	}); err != nil {
		return nil, err
	}

	date := query.Date
	if date == "" {
		date = time.Now().Format("20060102")
	}
	items := make([]MinutePoint, 0, resp.Count)
	for _, point := range resp.List {
		items = append(items, MinutePoint{
			Code:   query.Code,
			Date:   date,
			Clock:  point.Time,
			Price:  priceFromProtocol(point.Price),
			Number: point.Number,
		})
	}
	return items, nil
}

func (p *TDXProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	if query.Code == "" {
		return nil, errors.New("kline query requires code")
	}
	if query.AssetType == AssetTypeUnknown {
		query.AssetType = detectAssetType(query.Code)
	}

	var resp *protocol.KlineResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = fetchKlineRange(client, query)
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]KlineBar, 0, resp.Count)
	for _, bar := range resp.List {
		items = append(items, KlineBar{
			Code:       query.Code,
			AssetType:  detectAssetType(query.Code),
			Period:     query.Period,
			Time:       bar.Time,
			PrevClose:  priceFromProtocol(bar.Last),
			Open:       priceFromProtocol(bar.Open),
			High:       priceFromProtocol(bar.High),
			Low:        priceFromProtocol(bar.Low),
			Close:      priceFromProtocol(bar.Close),
			VolumeHand: bar.Volume,
			Amount:     priceFromProtocol(bar.Amount),
			UpCount:    bar.UpCount,
			DownCount:  bar.DownCount,
		})
	}
	return items, nil
}

func (p *TDXProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	if query.Code == "" {
		return nil, errors.New("trade history query requires code")
	}

	var resp *protocol.TradeResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		if query.Date == "" {
			resp, err = client.GetMinuteTradeAll(query.Code)
		} else {
			resp, err = client.GetHistoryTradeDay(query.Date, query.Code)
		}
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]TradeTick, 0, resp.Count)
	for _, trade := range resp.List {
		items = append(items, TradeTick{
			Code:       query.Code,
			Time:       trade.Time,
			Price:      priceFromProtocol(trade.Price),
			VolumeHand: trade.Volume,
			Number:     trade.Number,
			StatusCode: trade.Status,
			Side:       trade.StatusString(),
		})
	}
	return items, nil
}

func (p *TDXProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	if query.Code == "" || query.Date == "" {
		return nil, errors.New("order history query requires code and date")
	}

	var resp *protocol.HistoryOrdersResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = client.GetHistoryOrders(query.Date, query.Code)
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]OrderHistoryEntry, 0, resp.Count)
	for _, row := range resp.List {
		items = append(items, OrderHistoryEntry{
			Price:        priceFromProtocol(row.Price),
			BuySellDelta: row.BuySellDelta,
			Volume:       row.Volume,
		})
	}
	return &OrderHistorySnapshot{
		Code:     query.Code,
		Date:     query.Date,
		PreClose: priceFromProtocol(resp.PreClose),
		Items:    items,
	}, nil
}

func (p *TDXProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	if code == "" {
		return nil, errors.New("finance requires code")
	}

	var resp *protocol.FinanceInfo
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = client.GetFinanceInfo(code)
		return err
	}); err != nil {
		return nil, err
	}

	return &FinanceSnapshot{
		Code:               protocol.AddPrefix(resp.Code),
		Market:             resp.Market.String(),
		Province:           resp.Province,
		Industry:           resp.Industry,
		UpdatedDate:        fmt.Sprintf("%08d", resp.UpdatedDate),
		IPODate:            fmt.Sprintf("%08d", resp.IpoDate),
		Liutongguben:       resp.Liutongguben,
		Zongguben:          resp.Zongguben,
		Guojiagu:           resp.Guojiagu,
		Faqirenfarengu:     resp.Faqirenfarengu,
		Farengu:            resp.Farengu,
		Bgu:                resp.Bgu,
		Hgu:                resp.Hgu,
		Zhigonggu:          resp.Zhigonggu,
		Zongzichan:         resp.Zongzichan,
		Liudongzichan:      resp.Liudongzichan,
		Gudingzichan:       resp.Gudingzichan,
		Wuxingzichan:       resp.Wuxingzichan,
		Gudongrenshu:       resp.Gudongrenshu,
		Liudongfuzhai:      resp.Liudongfuzhai,
		Changqifuzhai:      resp.Changqifuzhai,
		Zibengongjijin:     resp.Zibengongjijin,
		Jingzichan:         resp.Jingzichan,
		Zhuyingshouru:      resp.Zhuyingshouru,
		Zhuyinglirun:       resp.Zhuyinglirun,
		Yingshouzhangkuan:  resp.Yingshouzhangkuan,
		Yingyelirun:        resp.Yingyelirun,
		Touzishouyu:        resp.Touzishouyu,
		Jingyingxianjinliu: resp.Jingyingxianjinliu,
		Zongxianjinliu:     resp.Zongxianjinliu,
		Cunhuo:             resp.Cunhuo,
		Lirunzonghe:        resp.Lirunzonghe,
		Shuihoulirun:       resp.Shuihoulirun,
		Jinglirun:          resp.Jinglirun,
		Weifenpeilirun:     resp.Weifenpeilirun,
		Meigujingzichan:    resp.Meigujingzichan,
		Baoliu2:            resp.Baoliu2,
	}, nil
}

func (p *TDXProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	if code == "" {
		return nil, errors.New("f10 categories require code")
	}

	var resp protocol.CompanyInfoCategories
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = client.GetCompanyInfoCategory(code)
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]F10Category, 0, len(resp))
	for _, category := range resp {
		items = append(items, F10Category{
			Code:     code,
			Name:     category.Name,
			Filename: category.Filename,
			Start:    category.Start,
			Length:   category.Length,
		})
	}
	return items, nil
}

func (p *TDXProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	if query.Code == "" || query.Filename == "" {
		return nil, errors.New("f10 content requires code and filename")
	}

	var content string
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		content, err = client.GetCompanyInfoContent(query.Code, query.Filename, query.Start, query.Length)
		return err
	}); err != nil {
		return nil, err
	}

	return &F10Content{
		Code:     query.Code,
		Filename: query.Filename,
		Start:    query.Start,
		Length:   query.Length,
		Content:  content,
	}, nil
}

func (p *TDXProvider) withClient(ctx context.Context, fn func(client *tdx.Client) error) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	var lastErr error
	for attempt := 1; attempt <= tdxProviderMaxAttempts; attempt++ {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		var err error
		switch {
		case p.manage != nil:
			err = p.manage.Do(func(client *tdx.Client) error {
				client.SetTimeout(tdxClientTimeout)
				return fn(client)
			})
		case p.client != nil:
			p.client.SetTimeout(tdxClientTimeout)
			err = fn(p.client)
		default:
			return errors.New("tdx provider requires manage or client")
		}
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableTDXError(err) || attempt == tdxProviderMaxAttempts {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt) * tdxProviderRetryDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return lastErr
}

func isRetryableTDXError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	message := err.Error()
	messageLower := strings.ToLower(message)
	return strings.Contains(message, "数据长度不足") ||
		strings.Contains(messageLower, "超时") ||
		strings.Contains(messageLower, "timeout") ||
		strings.Contains(messageLower, "broken pipe") ||
		strings.Contains(messageLower, "connection reset") ||
		strings.Contains(messageLower, "use of closed network connection") ||
		strings.Contains(messageLower, "eof") ||
		strings.Contains(messageLower, "i/o timeout") ||
		strings.Contains(messageLower, "connection refused")
}

func (p *TDXProvider) lookupName(code string) string {
	if p.manage == nil || p.manage.Codes == nil {
		return ""
	}
	return p.manage.Codes.GetName(code)
}

func priceFromProtocol(price protocol.Price) PriceMilli {
	return PriceMilli(price.Int64())
}

func detectAssetType(code string) AssetType {
	switch {
	case protocol.IsStock(code):
		return AssetTypeStock
	case protocol.IsETF(code):
		return AssetTypeETF
	case protocol.IsIndex(code):
		return AssetTypeIndex
	default:
		return AssetTypeUnknown
	}
}

func fetchKlineWithLimit(limit int, allFn func(code string) (*protocol.KlineResp, error), limitFn func(code string, start, count uint16) (*protocol.KlineResp, error), code string) (*protocol.KlineResp, error) {
	if limit <= 0 {
		return allFn(code)
	}
	if limit > 800 {
		limit = 800
	}
	resp, err := limitFn(code, 0, uint16(limit))
	if err != nil {
		return nil, err
	}
	sort.Slice(resp.List, func(i, j int) bool {
		return resp.List[i].Time.Before(resp.List[j].Time)
	})
	return resp, nil
}

func fetchKlineRange(client *tdx.Client, query KlineQuery) (*protocol.KlineResp, error) {
	if client == nil {
		return nil, errors.New("nil client")
	}

	isIndex := query.AssetType == AssetTypeIndex || protocol.IsIndex(query.Code)
	type allFunc func(code string) (*protocol.KlineResp, error)
	type limitFunc func(code string, start, count uint16) (*protocol.KlineResp, error)
	type untilFunc func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error)

	var (
		all   allFunc
		lim   limitFunc
		until untilFunc
	)
	switch query.Period {
	case PeriodMinute:
		if isIndex {
			all = func(code string) (*protocol.KlineResp, error) {
				return client.GetIndexAll(protocol.TypeKlineMinute, code)
			}
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKlineMinute, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKlineMinute, code, f)
			}
		} else {
			all, lim, until = client.GetKlineMinuteAll, client.GetKlineMinute, client.GetKlineMinuteUntil
		}
	case Period5Minute:
		if isIndex {
			all = func(code string) (*protocol.KlineResp, error) {
				return client.GetIndexAll(protocol.TypeKline5Minute, code)
			}
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKline5Minute, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKline5Minute, code, f)
			}
		} else {
			all, lim, until = client.GetKline5MinuteAll, client.GetKline5Minute, client.GetKline5MinuteUntil
		}
	case Period15Minute:
		if isIndex {
			all = func(code string) (*protocol.KlineResp, error) {
				return client.GetIndexAll(protocol.TypeKline15Minute, code)
			}
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKline15Minute, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKline15Minute, code, f)
			}
		} else {
			all, lim, until = client.GetKline15MinuteAll, client.GetKline15Minute, client.GetKline15MinuteUntil
		}
	case Period30Minute:
		if isIndex {
			all = func(code string) (*protocol.KlineResp, error) {
				return client.GetIndexAll(protocol.TypeKline30Minute, code)
			}
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKline30Minute, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKline30Minute, code, f)
			}
		} else {
			all, lim, until = client.GetKline30MinuteAll, client.GetKline30Minute, client.GetKline30MinuteUntil
		}
	case Period60Minute:
		if isIndex {
			all = func(code string) (*protocol.KlineResp, error) {
				return client.GetIndexAll(protocol.TypeKline60Minute, code)
			}
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKline60Minute, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKline60Minute, code, f)
			}
		} else {
			all, lim, until = client.GetKlineHourAll, client.GetKlineHour, client.GetKlineHourUntil
		}
	case PeriodDay:
		if isIndex {
			all, lim, until = client.GetIndexDayAll, client.GetIndexDay, client.GetIndexDayUntil
		} else {
			all, lim, until = client.GetKlineDayAll, client.GetKlineDay, client.GetKlineDayUntil
		}
	case PeriodWeek:
		if isIndex {
			all = client.GetIndexWeekAll
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKlineWeek, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKlineWeek, code, f)
			}
		} else {
			all, lim, until = client.GetKlineWeekAll, client.GetKlineWeek, client.GetKlineWeekUntil
		}
	case PeriodMonth:
		if isIndex {
			all = client.GetIndexMonthAll
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKlineMonth, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKlineMonth, code, f)
			}
		} else {
			all, lim, until = client.GetKlineMonthAll, client.GetKlineMonth, client.GetKlineMonthUntil
		}
	case PeriodQuarter:
		if isIndex {
			all = client.GetIndexQuarterAll
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKlineQuarter, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKlineQuarter, code, f)
			}
		} else {
			all, lim, until = client.GetKlineQuarterAll, client.GetKlineQuarter, client.GetKlineQuarterUntil
		}
	case PeriodYear:
		if isIndex {
			all = client.GetIndexYearAll
			lim = func(code string, start, count uint16) (*protocol.KlineResp, error) {
				return client.GetIndex(protocol.TypeKlineYear, code, start, count)
			}
			until = func(code string, f func(k *protocol.Kline) bool) (*protocol.KlineResp, error) {
				return client.GetIndexUntil(protocol.TypeKlineYear, code, f)
			}
		} else {
			all, lim, until = client.GetKlineYearAll, client.GetKlineYear, client.GetKlineYearUntil
		}
	default:
		return nil, fmt.Errorf("unsupported kline period: %s", query.Period)
	}

	if !query.Since.IsZero() {
		resp, err := until(query.Code, func(k *protocol.Kline) bool {
			return !k.Time.After(query.Since)
		})
		if err != nil {
			return nil, err
		}
		filtered := make([]*protocol.Kline, 0, len(resp.List))
		for _, item := range resp.List {
			if item.Time.After(query.Since) {
				filtered = append(filtered, item)
			}
		}
		resp.List = filtered
		resp.Count = uint16(len(filtered))
		return resp, nil
	}

	return fetchKlineWithLimit(query.Limit, all, lim, query.Code)
}

func (p *TDXProvider) refreshInstruments() ([]Instrument, error) {
	items := make([]Instrument, 0, 8192)
	seen := make(map[string]struct{}, 8192)
	for _, exchange := range []protocol.Exchange{protocol.ExchangeSH, protocol.ExchangeSZ, protocol.ExchangeBJ} {
		var resp *protocol.CodeResp
		if err := p.withClient(context.Background(), func(client *tdx.Client) error {
			var err error
			resp, err = client.GetCodeAll(exchange)
			return err
		}); err != nil {
			return nil, err
		}
		for _, model := range resp.List {
			code := exchange.String() + model.Code
			if _, ok := seen[code]; ok {
				continue
			}
			seen[code] = struct{}{}
			items = append(items, Instrument{
				Code:      code,
				Name:      model.Name,
				Exchange:  exchange.String(),
				AssetType: detectAssetType(code),
				Multiple:  model.Multiple,
				Decimal:   model.Decimal,
				LastPrice: PriceMilli(model.LastPrice * 1000),
				Source:    "tdx",
			})
		}
	}

	if p.manage != nil && p.manage.Codes != nil {
		for _, model := range p.manage.Codes.GetIndexModels() {
			code := model.FullCode()
			if _, ok := seen[code]; ok {
				continue
			}
			seen[code] = struct{}{}
			items = append(items, Instrument{
				Code:      code,
				Name:      model.Name,
				Exchange:  model.Exchange,
				AssetType: AssetTypeIndex,
				Multiple:  model.Multiple,
				Decimal:   model.Decimal,
				LastPrice: PriceMilli(model.LastPrice * 1000),
				Source:    p.manage.Codes.GetIndexSource(),
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Code < items[j].Code
	})
	return items, nil
}

func (p *TDXProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	if filename == "" {
		return nil, errors.New("block groups requires filename")
	}

	var data []byte
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		data, err = client.DownloadBlockFile(filename)
		return err
	}); err != nil {
		return nil, err
	}

	groups, err := protocol.ParseBlockFile(data)
	if err != nil {
		return nil, err
	}

	items := make([]BlockInfo, 0, len(groups))
	for _, g := range groups {
		items = append(items, BlockInfo{
			Name:      g.Name,
			BlockType: classifyBlockType(filename, g.Name),
			Source:    filename,
			Codes:     g.Codes,
		})
	}
	return items, nil
}

func (p *TDXProvider) refreshTradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	var resp *protocol.KlineResp
	if err := p.withClient(ctx, func(client *tdx.Client) error {
		var err error
		resp, err = client.GetIndexDayAll("sh000001")
		return err
	}); err != nil {
		return nil, err
	}

	items := make([]TradingDay, 0, len(resp.List))
	for _, bar := range resp.List {
		if !query.Start.IsZero() && bar.Time.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && !bar.Time.Before(query.End) {
			continue
		}
		items = append(items, TradingDay{
			Date: bar.Time.Format("20060102"),
			Time: bar.Time,
		})
	}
	return items, nil
}
