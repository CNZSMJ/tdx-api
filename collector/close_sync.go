package collector

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type CloseSyncFailure struct {
	Domain     string      `json:"domain"`
	Date       string      `json:"date"`
	Instrument string      `json:"instrument,omitempty"`
	Period     KlinePeriod `json:"period,omitempty"`
	Reason     string      `json:"reason"`
}

func (r *Runtime) ResolveRecentTradingDates(ctx context.Context, limit int) ([]string, error) {
	return r.ResolveRecentTradingDatesAt(ctx, r.cfg.Now(), limit)
}

func (r *Runtime) ResolveRecentTradingDatesAt(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 2
	}
	items, err := r.provider.TradingDays(ctx, TradingDayQuery{
		Start: now.AddDate(0, 0, -14),
		End:   now.Add(24 * time.Hour),
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Time.Equal(items[j].Time) {
			return items[i].Date < items[j].Date
		}
		return items[i].Time.Before(items[j].Time)
	})

	today := now.Format("20060102")
	seen := make(map[string]struct{}, len(items))
	dates := make([]string, 0, len(items))
	for _, item := range items {
		if item.Date == "" || item.Date > today {
			continue
		}
		if _, ok := seen[item.Date]; ok {
			continue
		}
		seen[item.Date] = struct{}{}
		dates = append(dates, item.Date)
	}
	if len(dates) <= limit {
		return dates, nil
	}
	return append([]string(nil), dates[len(dates)-limit:]...), nil
}

func (r *Runtime) ExecuteDailyCloseSync(ctx context.Context, dates []string) ([]CloseSyncFailure, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	instruments, err := r.provider.Instruments(ctx, InstrumentQuery{
		AssetTypes: []AssetType{AssetTypeStock, AssetTypeETF, AssetTypeIndex},
	})
	if err != nil {
		return nil, err
	}
	instruments = normalizeInstruments(instruments)

	failures := make([]CloseSyncFailure, 0)
	latestWindow := dates[len(dates)-1]
	for _, date := range dates {
		targetTime, err := parseTradeCursor(date)
		if err != nil {
			return nil, err
		}
		tradingDay, err := r.provider.IsTradingDay(ctx, targetTime)
		if err != nil {
			return nil, err
		}
		for _, instrument := range instruments {
			for _, period := range r.cfg.KlinePeriods {
				if err := r.kline.ReconcileDate(ctx, KlineCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Period:    period,
				}, date); err != nil {
					if isFatalCtxError(err) {
						return nil, err
					}
					failures = append(failures, CloseSyncFailure{
						Domain:     "kline",
						Date:       date,
						Instrument: instrument.Code,
						Period:     period,
						Reason:     fmt.Sprintf("%s: %v", period, err),
					})
				}
			}

			if !tradingDay {
				continue
			}
			if instrument.AssetType == AssetTypeStock || instrument.AssetType == AssetTypeETF {
				if err := r.trade.RefreshDay(ctx, TradeCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					if isFatalCtxError(err) {
						return nil, err
					}
					failures = append(failures, CloseSyncFailure{
						Domain:     tradeHistoryDomain,
						Date:       date,
						Instrument: instrument.Code,
						Reason:     err.Error(),
					})
				}

				if err := r.live.ReconcileDay(ctx, SessionCaptureQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					if isFatalCtxError(err) {
						return nil, err
					}
					failures = append(failures, CloseSyncFailure{
						Domain:     liveCaptureDomain,
						Date:       date,
						Instrument: instrument.Code,
						Reason:     err.Error(),
					})
				}
			}

			if instrument.AssetType == AssetTypeStock {
				if err := r.orderHistory.RefreshDay(ctx, OrderHistoryCollectQuery{
					Code:      instrument.Code,
					AssetType: instrument.AssetType,
					Date:      date,
				}); err != nil {
					if isFatalCtxError(err) {
						return nil, err
					}
					failures = append(failures, CloseSyncFailure{
						Domain:     "order_history",
						Date:       date,
						Instrument: instrument.Code,
						Reason:     err.Error(),
					})
				}
			}
		}
	}

	for _, instrument := range instruments {
		if instrument.AssetType != AssetTypeStock {
			continue
		}

		if _, _, err := r.fundamentals.RefreshFinanceIfUpdated(ctx, instrument.Code); err != nil {
			if isFatalCtxError(err) {
				return nil, err
			}
			failures = append(failures, CloseSyncFailure{
				Domain:     "finance",
				Date:       latestWindow,
				Instrument: instrument.Code,
				Reason:     err.Error(),
			})
		}

		if _, _, err := r.fundamentals.SyncF10IfChanged(ctx, instrument.Code); err != nil {
			if isFatalCtxError(err) {
				return nil, err
			}
			failures = append(failures, CloseSyncFailure{
				Domain:     "f10",
				Date:       latestWindow,
				Instrument: instrument.Code,
				Reason:     err.Error(),
			})
		}
	}
	return failures, nil
}

func (r *Runtime) RepairCloseSyncFailure(ctx context.Context, failure CloseSyncFailure) error {
	if r == nil {
		return fmt.Errorf("collector runtime is nil")
	}
	switch failure.Domain {
	case "kline":
		if failure.Period == "" {
			failure.Period = PeriodDay
		}
		return r.kline.ReconcileDate(ctx, KlineCollectQuery{
			Code:      failure.Instrument,
			AssetType: detectAssetType(failure.Instrument),
			Period:    failure.Period,
		}, failure.Date)
	case tradeHistoryDomain:
		return r.trade.RefreshDay(ctx, TradeCollectQuery{
			Code:      failure.Instrument,
			AssetType: detectAssetType(failure.Instrument),
			Date:      failure.Date,
		})
	case liveCaptureDomain:
		return r.live.ReconcileDay(ctx, SessionCaptureQuery{
			Code:      failure.Instrument,
			AssetType: detectAssetType(failure.Instrument),
			Date:      failure.Date,
		})
	case "order_history":
		return r.orderHistory.RefreshDay(ctx, OrderHistoryCollectQuery{
			Code:      failure.Instrument,
			AssetType: detectAssetType(failure.Instrument),
			Date:      failure.Date,
		})
	case "finance":
		_, _, err := r.fundamentals.RefreshFinanceIfUpdated(ctx, failure.Instrument)
		return err
	case "f10":
		_, _, err := r.fundamentals.SyncF10IfChanged(ctx, failure.Instrument)
		return err
	default:
		return fmt.Errorf("unsupported close sync repair domain: %s", failure.Domain)
	}
}
