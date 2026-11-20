package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
)

func (s *PersistenceState) observeRequestDelivery(ctx context.Context) {
	clock := contexts.GetRequestClock(ctx)
	if clock == nil || s.Request == nil || !s.Request.Stream || s.RequestExec == nil || s.Perf == nil {
		return
	}
	requestID, execID := s.Request.ID, s.RequestExec.ID
	perf := s.Perf
	clock.OnFinish(func(t contexts.RequestTiming, _ bool) {
		d := &objects.RequestStreamTiming{Attempts: len(t.Attempts)}
		if t.Delivered != nil {
			d.SingleAnswerBatch, d.AnswerTokensPerSecond = t.SingleAnswerBatch, t.AnswerTokensPerSecond
		}
		for _, a := range t.Attempts {
			if a.ExecutionID == execID && a.Semantic != nil && !perf.StartTime.IsZero() {
				d.FirstEvent = a.Semantic.Type
				ms := a.Semantic.At.Sub(perf.StartTime).Milliseconds()
				d.FirstEventMs = &ms
			}
		}
		if !perf.EndTime.IsZero() && !perf.StartTime.IsZero() {
			ms := perf.EndTime.Sub(perf.StartTime).Milliseconds()
			d.DurationMs = &ms
		}
		if w := t.FirstWriteContent; w != nil {
			d.ContentEvent = w.Type
			ms := w.At.Sub(t.BodyRead).Milliseconds()
			d.ContentWriteMs = &ms
			if t.Delivered != nil && !t.Delivered.Before(w.At) {
				tail := t.Delivered.Sub(w.At).Milliseconds()
				d.AfterContentMs = &tail
			}
		}
		if t.FirstAnswer != nil {
			ms := t.FirstAnswer.Sub(t.BodyRead).Milliseconds()
			d.AnswerWriteMs = &ms
		}
		writeCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := s.RequestService.SaveStreamTiming(writeCtx, requestID, d); err != nil {
			log.Warn(writeCtx, "保存请求交付计时失败", log.Int("request_id", requestID), log.Cause(err))
		}
	})
}
