package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tdx "github.com/injoyai/tdx"
	collectorpkg "github.com/injoyai/tdx/collector"
	"github.com/injoyai/tdx/trading/instrumentmetrics"
)

const maxTradingInstrumentMetricsCodes = 50

type tradingInstrumentMetricsRequest struct {
	Market        string   `json:"market"`
	AsOfTradeDate string   `json:"as_of_trade_date"`
	FullCodes     []string `json:"full_codes"`
	MetricCodes   []string `json:"metric_codes"`
}

func handleTradingInstrumentMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}

	var req tradingInstrumentMetricsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}
	asOf, err := parseWorkdayDate(strings.TrimSpace(req.AsOfTradeDate))
	if err != nil {
		errorResponse(w, "as_of_trade_date 参数格式错误，应为 YYYYMMDD 或 YYYY-MM-DD")
		return
	}
	if len(req.FullCodes) == 0 {
		errorResponse(w, "full_codes 为必填参数")
		return
	}
	if len(req.FullCodes) > maxTradingInstrumentMetricsCodes {
		errorResponse(w, fmt.Sprintf("一次最多查询%d只证券", maxTradingInstrumentMetricsCodes))
		return
	}
	metricCodes := make([]instrumentmetrics.MetricCode, 0, len(req.MetricCodes))
	for _, code := range req.MetricCodes {
		text := strings.TrimSpace(code)
		if text == "" {
			continue
		}
		metricCodes = append(metricCodes, instrumentmetrics.MetricCode(text))
	}
	if len(metricCodes) == 0 {
		errorResponse(w, "metric_codes 为必填参数")
		return
	}
	if err := instrumentmetrics.ValidateMetricCodes(metricCodes); err != nil {
		errorResponse(w, err.Error())
		return
	}
	models, err := resolveTradingInstrumentMetricModels(req.FullCodes)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	service := instrumentmetrics.NewService(newTradingInstrumentMetricsLoader(models))
	resp, err := service.Calculate(r.Context(), instrumentmetrics.Request{
		Market:        req.Market,
		AsOfTradeDate: asOf,
		Instruments:   tradingMetricInstrumentsFromModels(models),
		MetricCodes:   metricCodes,
	})
	if err != nil {
		errorResponse(w, err.Error())
		return
	}
	successResponse(w, resp)
}

type tradingInstrumentMetricsLoader struct {
	models map[string]*tdx.CodeModel
}

func newTradingInstrumentMetricsLoader(models []*tdx.CodeModel) *tradingInstrumentMetricsLoader {
	byFullCode := make(map[string]*tdx.CodeModel, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		copyModel := *model
		byFullCode[strings.ToLower(copyModel.FullCode())] = &copyModel
	}
	return &tradingInstrumentMetricsLoader{models: byFullCode}
}

func (l *tradingInstrumentMetricsLoader) LoadDailyBars(_ context.Context, instrument instrumentmetrics.Instrument, before time.Time, count int) ([]instrumentmetrics.DailyBar, error) {
	fullCode := strings.ToLower(strings.TrimSpace(instrument.FullCode))
	if fullCode == "" {
		return nil, errors.New("full_code is required")
	}
	if rows, ok := loadTradingInstrumentMetricLocalDailyBars(fullCode, before, count); ok {
		return rows, nil
	}
	model := l.models[fullCode]
	if model == nil {
		parsed, err := parseTradingInstrumentMetricModel(fullCode)
		if err != nil {
			return nil, err
		}
		model = parsed
	}
	rows, _, err := fetchHistoricalBarRows(model, "day", "bfq")
	if err != nil {
		return nil, err
	}
	return tradingMetricDailyBarsFromHistoryRows(rows, before, count), nil
}

func (l *tradingInstrumentMetricsLoader) LoadAuctionAmounts(ctx context.Context, instrument instrumentmetrics.Instrument, before time.Time, count int) ([]instrumentmetrics.AuctionAmount, error) {
	fullCode := strings.ToLower(strings.TrimSpace(instrument.FullCode))
	if fullCode == "" {
		return nil, errors.New("full_code is required")
	}
	return loadTradingInstrumentMetricLocalAuctionAmounts(ctx, fullCode, before, count)
}

func loadTradingInstrumentMetricLocalDailyBars(fullCode string, before time.Time, count int) ([]instrumentmetrics.DailyBar, bool) {
	if count <= 0 {
		return nil, false
	}
	dbPath := filepath.Join(databaseDir, "kline", fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, false
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, false
	}
	defer db.Close()

	cutoff := time.Date(before.Year(), before.Month(), before.Day(), 0, 0, 0, 0, time.Local).Unix()
	rows, err := db.Query(`SELECT Date, Open, High, Low, Close, Volume, Amount FROM DayKline WHERE Code = ? AND Date < ? ORDER BY Date DESC LIMIT ?`, fullCode, cutoff, count)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	out := make([]instrumentmetrics.DailyBar, 0, count)
	for rows.Next() {
		var (
			at     int64
			open   collectorpkg.PriceMilli
			high   collectorpkg.PriceMilli
			low    collectorpkg.PriceMilli
			close  collectorpkg.PriceMilli
			volume int64
			amount collectorpkg.PriceMilli
		)
		if err := rows.Scan(&at, &open, &high, &low, &close, &volume, &amount); err != nil {
			return nil, false
		}
		out = append(out, instrumentmetrics.DailyBar{
			Date:   time.Unix(at, 0).In(time.Local),
			Open:   open.Float64(),
			High:   high.Float64(),
			Low:    low.Float64(),
			Close:  close.Float64(),
			Volume: volume,
			Amount: amount.Float64(),
		})
	}
	if rows.Err() != nil || len(out) == 0 {
		return nil, false
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Date.Before(out[j].Date)
	})
	return out, true
}

func loadTradingInstrumentMetricLocalAuctionAmounts(ctx context.Context, fullCode string, before time.Time, count int) ([]instrumentmetrics.AuctionAmount, error) {
	if count <= 0 {
		return nil, nil
	}
	dbPath := filepath.Join(databaseDir, "trade", fullCode+".db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	beforeDate := time.Date(before.Year(), before.Month(), before.Day(), 0, 0, 0, 0, time.Local).Format("20060102")
	dateRows, err := db.QueryContext(ctx, `SELECT TradeDate FROM TradeHistory WHERE Code = ? AND TradeDate < ? GROUP BY TradeDate ORDER BY TradeDate DESC LIMIT ?`, fullCode, beforeDate, count)
	if err != nil {
		return nil, err
	}
	defer dateRows.Close()

	dates := make([]string, 0, count)
	for dateRows.Next() {
		var date string
		if err := dateRows.Scan(&date); err != nil {
			return nil, err
		}
		dates = append(dates, date)
	}
	if err := dateRows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(dates)

	out := make([]instrumentmetrics.AuctionAmount, 0, len(dates))
	for _, date := range dates {
		day, err := parseWorkdayDate(date)
		if err != nil {
			return nil, fmt.Errorf("invalid TradeDate %q in %s: %w", date, fullCode, err)
		}
		start, end := tradingMetricAuctionWindow(day)
		var amountMilli sql.NullInt64
		err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CAST(Price AS INTEGER) * CAST(VolumeHand AS INTEGER) * 100), 0) FROM TradeHistory WHERE Code = ? AND TradeDate = ? AND TradeTime >= ? AND TradeTime < ?`, fullCode, date, start.Unix(), end.Unix()).Scan(&amountMilli)
		if err != nil {
			return nil, err
		}
		if !amountMilli.Valid || amountMilli.Int64 <= 0 {
			continue
		}
		out = append(out, instrumentmetrics.AuctionAmount{
			Date:   day,
			Amount: float64(amountMilli.Int64) / 1000,
		})
	}
	return out, nil
}

func tradingMetricAuctionWindow(day time.Time) (time.Time, time.Time) {
	local := day.In(time.Local)
	start := time.Date(local.Year(), local.Month(), local.Day(), 9, 15, 0, 0, time.Local)
	end := time.Date(local.Year(), local.Month(), local.Day(), 9, 30, 0, 0, time.Local)
	return start, end
}

func tradingMetricDailyBarsFromHistoryRows(rows []historyBarRow, before time.Time, count int) []instrumentmetrics.DailyBar {
	cutoff := time.Date(before.Year(), before.Month(), before.Day(), 0, 0, 0, 0, time.Local)
	out := make([]instrumentmetrics.DailyBar, 0, len(rows))
	for _, row := range rows {
		if !time.Date(row.Time.Year(), row.Time.Month(), row.Time.Day(), 0, 0, 0, 0, time.Local).Before(cutoff) {
			continue
		}
		out = append(out, instrumentmetrics.DailyBar{
			Date:   row.Time,
			Open:   row.Open,
			High:   row.High,
			Low:    row.Low,
			Close:  row.Close,
			Volume: row.Volume,
			Amount: row.Amount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Date.Before(out[j].Date)
	})
	if count > 0 && len(out) > count {
		out = out[len(out)-count:]
	}
	return out
}

func resolveTradingInstrumentMetricModels(fullCodes []string) ([]*tdx.CodeModel, error) {
	if tdx.DefaultCodes != nil || client != nil {
		return resolveFullCodeModels(fullCodes)
	}
	models := make([]*tdx.CodeModel, 0, len(fullCodes))
	for _, fullCode := range fullCodes {
		model, err := parseTradingInstrumentMetricModel(fullCode)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, nil
}

func parseTradingInstrumentMetricModel(raw string) (*tdx.CodeModel, error) {
	fullCode := strings.ToLower(strings.TrimSpace(raw))
	if fullCode == "" {
		return nil, errors.New("full_code 为必填参数")
	}
	if len(fullCode) != 8 {
		return nil, fmt.Errorf("full_code 参数无效，请传完整市场前缀代码，例如 sh600000：%s", raw)
	}
	exchange := fullCode[:2]
	if exchange != "sh" && exchange != "sz" && exchange != "bj" {
		return nil, fmt.Errorf("full_code 参数无效，请传完整市场前缀代码，例如 sh600000：%s", raw)
	}
	code := fullCode[2:]
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return nil, fmt.Errorf("full_code 参数无效，请传完整市场前缀代码，例如 sh600000：%s", raw)
		}
	}
	return &tdx.CodeModel{Code: code, Exchange: exchange, Decimal: 2, Multiple: 100}, nil
}

func tradingMetricInstrumentsFromModels(models []*tdx.CodeModel) []instrumentmetrics.Instrument {
	instruments := make([]instrumentmetrics.Instrument, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		instruments = append(instruments, instrumentmetrics.Instrument{
			FullCode:  strings.ToLower(model.FullCode()),
			Symbol:    tradingMetricSymbol(model.FullCode()),
			Name:      model.Name,
			Exchange:  providerExchange(model),
			AssetType: modelAssetType(model),
		})
	}
	return instruments
}

func tradingMetricSymbol(fullCode string) string {
	text := strings.ToLower(strings.TrimSpace(fullCode))
	if len(text) <= 2 {
		return strings.ToUpper(text)
	}
	exchange := strings.ToUpper(text[:2])
	return text[2:] + "." + exchange
}
