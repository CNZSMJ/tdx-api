package collector

import (
	"context"
	"sync"
	"time"
)

type throttledProvider struct {
	upstream    Provider
	minInterval time.Duration
	mu          sync.Mutex
	lastCallAt  time.Time
}

func newThrottledProvider(provider Provider, minInterval time.Duration) Provider {
	if provider == nil || minInterval <= 0 {
		return provider
	}
	return &throttledProvider{
		upstream:    provider,
		minInterval: minInterval,
	}
}

func (p *throttledProvider) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.lastCallAt.IsZero() {
		p.lastCallAt = time.Now()
		return nil
	}

	nextAllowed := p.lastCallAt.Add(p.minInterval)
	wait := time.Until(nextAllowed)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	p.lastCallAt = time.Now()
	return nil
}

func (p *throttledProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.Instruments(ctx, query)
}

func (p *throttledProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.TradingDays(ctx, query)
}

func (p *throttledProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	if err := p.wait(ctx); err != nil {
		return false, err
	}
	return p.upstream.IsTradingDay(ctx, day)
}

func (p *throttledProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.Quotes(ctx, codes)
}

func (p *throttledProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.Minutes(ctx, query)
}

func (p *throttledProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.Klines(ctx, query)
}

func (p *throttledProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.TradeHistory(ctx, query)
}

func (p *throttledProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.OrderHistory(ctx, query)
}

func (p *throttledProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.Finance(ctx, code)
}

func (p *throttledProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.F10Categories(ctx, code)
}

func (p *throttledProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	return p.upstream.F10Content(ctx, query)
}
