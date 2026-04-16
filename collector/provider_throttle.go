package collector

import (
	"context"
	"time"
)

type throttledProvider struct {
	upstream    Provider
	minInterval time.Duration
	slots       chan time.Time
}

func newThrottledProvider(provider Provider, minInterval time.Duration, concurrency int) Provider {
	if provider == nil || minInterval <= 0 {
		return provider
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	slots := make(chan time.Time, concurrency)
	for i := 0; i < concurrency; i++ {
		slots <- time.Time{}
	}
	return &throttledProvider{
		upstream:    provider,
		minInterval: minInterval,
		slots:       slots,
	}
}

func (p *throttledProvider) acquire(ctx context.Context) (release func(), err error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case lastUsed := <-p.slots:
		nextAllowed := lastUsed.Add(p.minInterval)
		wait := time.Until(nextAllowed)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				p.slots <- lastUsed
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		return func() { p.slots <- time.Now() }, nil
	}
}

func (p *throttledProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.Instruments(ctx, query)
}

func (p *throttledProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.TradingDays(ctx, query)
}

func (p *throttledProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return p.upstream.IsTradingDay(ctx, day)
}

func (p *throttledProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.Quotes(ctx, codes)
}

func (p *throttledProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.Minutes(ctx, query)
}

func (p *throttledProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.Klines(ctx, query)
}

func (p *throttledProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.TradeHistory(ctx, query)
}

func (p *throttledProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.OrderHistory(ctx, query)
}

func (p *throttledProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.Finance(ctx, code)
}

func (p *throttledProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.F10Categories(ctx, code)
}

func (p *throttledProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.F10Content(ctx, query)
}

func (p *throttledProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.upstream.BlockGroups(ctx, filename)
}
