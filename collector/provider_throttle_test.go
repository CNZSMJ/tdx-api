package collector

import (
	"context"
	"sync"
	"testing"
	"time"
)

type throttleTestProvider struct {
	mu        sync.Mutex
	callTimes []time.Time
}

func (p *throttleTestProvider) record() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callTimes = append(p.callTimes, time.Now())
}

func (p *throttleTestProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.callTimes)
}

func (p *throttleTestProvider) snapshotCallTimes() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.callTimes...)
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

func (p *throttleTestProvider) BlockGroups(ctx context.Context, filename string) ([]BlockInfo, error) {
	p.record()
	return nil, nil
}

func TestThrottledProviderSpacesRequests(t *testing.T) {
	upstream := &throttleTestProvider{}
	provider := newThrottledProvider(upstream, 20*time.Millisecond, 1)

	if _, err := provider.Instruments(context.Background(), InstrumentQuery{}); err != nil {
		t.Fatalf("first instruments call: %v", err)
	}
	if _, err := provider.TradingDays(context.Background(), TradingDayQuery{}); err != nil {
		t.Fatalf("second trading-days call: %v", err)
	}

	callTimes := upstream.snapshotCallTimes()
	if len(callTimes) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(callTimes))
	}
	if delay := callTimes[1].Sub(callTimes[0]); delay < 18*time.Millisecond {
		t.Fatalf("expected throttled provider delay >= 18ms, got %s", delay)
	}
}

func TestThrottledProviderConcurrentSlots(t *testing.T) {
	upstream := &throttleTestProvider{}
	provider := newThrottledProvider(upstream, 50*time.Millisecond, 4)

	done := make(chan struct{}, 4)
	start := time.Now()
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			provider.Instruments(context.Background(), InstrumentQuery{})
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	elapsed := time.Since(start)

	if upstream.callCount() != 4 {
		t.Fatalf("expected 4 provider calls, got %d", upstream.callCount())
	}
	if elapsed > 40*time.Millisecond {
		t.Fatalf("expected 4 concurrent calls to complete within ~0ms (first batch), took %s", elapsed)
	}
}
