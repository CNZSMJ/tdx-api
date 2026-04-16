package collector

import (
	"context"
	"errors"
)

func (r *Runtime) CleanupKlineGaps(ctx context.Context, opts KlineGapCleanupOptions) (*KlineGapCleanupReport, error) {
	if r == nil || r.kline == nil {
		return nil, errors.New("collector runtime kline service not initialized")
	}
	return r.kline.CleanupCollectGaps(ctx, opts)
}
