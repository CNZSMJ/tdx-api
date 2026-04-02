package collector

import (
	"context"
	"testing"
	"time"
)

type throttleTestProvider struct {
	callTimes []time.Time
}

func (p *throttleTestProvider) record() {
	p.callTimes = append(p.callTimes, time.Now())
}

func (p *throttleTestProvider) Instruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) TradingDays(ctx context.Context, query TradingDayQuery) ([]TradingDay, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	p.record()
	return false, nil
}

func (p *throttleTestProvider) Quotes(ctx context.Context, codes []string) ([]QuoteSnapshot, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) Minutes(ctx context.Context, query MinuteQuery) ([]MinutePoint, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) Klines(ctx context.Context, query KlineQuery) ([]KlineBar, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) TradeHistory(ctx context.Context, query TradeHistoryQuery) ([]TradeTick, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) OrderHistory(ctx context.Context, query OrderHistoryQuery) (*OrderHistorySnapshot, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) Finance(ctx context.Context, code string) (*FinanceSnapshot, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) F10Categories(ctx context.Context, code string) ([]F10Category, error) {
	p.record()
	return nil, nil
}

func (p *throttleTestProvider) F10Content(ctx context.Context, query F10ContentQuery) (*F10Content, error) {
	p.record()
	return nil, nil
}

func TestThrottledProviderSpacesRequests(t *testing.T) {
	upstream := &throttleTestProvider{}
	provider := newThrottledProvider(upstream, 20*time.Millisecond)

	if _, err := provider.Instruments(context.Background(), InstrumentQuery{}); err != nil {
		t.Fatalf("first instruments call: %v", err)
	}
	if _, err := provider.TradingDays(context.Background(), TradingDayQuery{}); err != nil {
		t.Fatalf("second trading-days call: %v", err)
	}

	if len(upstream.callTimes) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(upstream.callTimes))
	}
	if delay := upstream.callTimes[1].Sub(upstream.callTimes[0]); delay < 18*time.Millisecond {
		t.Fatalf("expected throttled provider delay >= 18ms, got %s", delay)
	}
}
