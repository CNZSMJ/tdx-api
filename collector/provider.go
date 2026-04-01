package collector

import (
	"context"
	"time"
)

type UniverseProvider interface {
	Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error)
}

type CalendarProvider interface {
	TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error)
	IsTradingDay(ctx context.Context, day time.Time) (bool, error)
}

type QuoteProvider interface {
	Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error)
}

type MinuteProvider interface {
	Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error)
}

type KlineProvider interface {
	Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error)
}

type TradeHistoryProvider interface {
	TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error)
}

type OrderHistoryProvider interface {
	OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error)
}

type FinanceProvider interface {
	Finance(ctx context.Context, code string) (*FinanceSnapshot, error)
}

type F10Provider interface {
	F10Categories(ctx context.Context, code string) ([]F10Category, error)
	F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error)
}

type Provider interface {
	UniverseProvider
	CalendarProvider
	QuoteProvider
	MinuteProvider
	KlineProvider
	TradeHistoryProvider
	OrderHistoryProvider
	FinanceProvider
	F10Provider
}
