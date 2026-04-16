package collector

import (
	"context"
	"errors"
)

func (r *Runtime) ReconcileKlineGaps(ctx context.Context, opts KlineGapReconcileOptions) (*KlineGapReconcileReport, error) {
	if r == nil || r.kline == nil {
		return nil, errors.New("collector runtime kline service not initialized")
	}
	return r.kline.ReconcileCollectGaps(ctx, opts)
}
