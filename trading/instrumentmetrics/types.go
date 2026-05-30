package instrumentmetrics

import (
	"context"
	"fmt"
	"time"
)

type MetricCode string

const (
	MetricPCT5D              MetricCode = "PCT_5D"
	MetricMaxPCT20D          MetricCode = "MAX_PCT_20D"
	MetricAmountTrend5D      MetricCode = "AMOUNT_TREND_5D"
	MetricAvgAuctionAmount5D MetricCode = "AVG_AUCTION_AMOUNT_5D"
	MetricVSIndexPP          MetricCode = "VS_INDEX_PP"
)

type Availability string

const (
	AvailabilityAvailable Availability = "AVAILABLE"
	AvailabilityPartial   Availability = "PARTIAL"
	AvailabilityMissing   Availability = "MISSING"
)

type Precision string

const (
	PrecisionDailyBar       Precision = "DAILY_BAR"
	PrecisionAuctionHistory Precision = "AUCTION_HISTORY"
	PrecisionUnavailable    Precision = "UNAVAILABLE"
)

type Request struct {
	Market        string
	AsOfTradeDate time.Time
	Instruments   []Instrument
	MetricCodes   []MetricCode
}

type Response struct {
	Market           string  `json:"market"`
	AsOfTradeDate    string  `json:"as_of_trade_date"`
	CompletedThrough string  `json:"completed_through,omitempty"`
	Count            int     `json:"count"`
	Items            []Item  `json:"items"`
	Failures         []Error `json:"failures"`
}

type Instrument struct {
	FullCode  string `json:"full_code"`
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Exchange  string `json:"exchange"`
	AssetType string `json:"asset_type"`
}

type Item struct {
	FullCode       string                `json:"full_code"`
	Symbol         string                `json:"symbol"`
	Name           string                `json:"name"`
	Exchange       string                `json:"exchange"`
	AssetType      string                `json:"asset_type"`
	Benchmark      Benchmark             `json:"benchmark"`
	Metrics        map[MetricCode]Metric `json:"metrics"`
	MissingMetrics []MetricCode          `json:"missing_metrics"`
}

type Benchmark struct {
	Mode      string `json:"mode"`
	IndexCode string `json:"index_code,omitempty"`
	IndexName string `json:"index_name,omitempty"`
	FullCode  string `json:"-"`
}

type Metric struct {
	Value                 any          `json:"value"`
	Unit                  string       `json:"unit"`
	LookbackSessions      int          `json:"lookback_sessions"`
	CompletedSessionsUsed int          `json:"completed_sessions_used"`
	Precision             Precision    `json:"precision"`
	Availability          Availability `json:"availability"`
	MissingReason         string       `json:"missing_reason,omitempty"`
}

type Error struct {
	FullCode string `json:"full_code,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Message  string `json:"message"`
}

type DailyBar struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
	Amount float64
}

type AuctionAmount struct {
	Date   time.Time
	Amount float64
}

type Loader interface {
	LoadDailyBars(ctx context.Context, instrument Instrument, before time.Time, count int) ([]DailyBar, error)
	LoadAuctionAmounts(ctx context.Context, instrument Instrument, before time.Time, count int) ([]AuctionAmount, error)
}

type InvalidArgumentError struct {
	Message string
}

func (e *InvalidArgumentError) Error() string {
	return fmt.Sprintf("INVALID_ARGUMENT: %s", e.Message)
}
