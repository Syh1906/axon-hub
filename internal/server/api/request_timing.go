package api

import (
	"context"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
)

// A single structured record links all attempts to the persisted request and its
// existing scoped continuation_key. Header flushes, pings and warmups do not call Wrote.
func reportRequestTiming(ctx context.Context, clock *contexts.RequestClock, requestID int, failed bool) {
	if requestID == 0 {
		return
	}
	info := clock.ReportAtInfo(failed)
	if !info && !log.DebugEnabled(ctx) {
		return
	}
	timing := clock.Snapshot()
	fields := []log.Field{log.Int("axon_request_id", requestID), log.Any("timing", timing)}
	if info {
		log.Info(ctx, "请求交付分段计时", fields...)
	} else {
		log.Debug(ctx, "请求交付分段计时", fields...)
	}
}
