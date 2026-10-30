package orchestrator

import (
	"context"
	"errors"
	"net"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/pipeline"
)

func healthGatePipelineTimeout(ctx context.Context) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	cause := context.Cause(ctx)
	return errors.Is(cause, pipeline.ErrNonStreamResponseTimeout) || errors.Is(cause, pipeline.ErrStreamFirstEventTimeout)
}

func healthGateClientCanceled(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil && !healthGatePipelineTimeout(ctx)
}

func classifyHealthGateOutcome(ctx context.Context, err error) biz.HealthGateOutcome {
	if err == nil {
		return biz.HealthGateOutcomeSuccess
	}
	if errors.Is(err, errSkipCandidateByCircuitBreaker) || errors.Is(err, errSkipCandidateByHealthGate) ||
		isChannelQueueError(err) || isLocalRPMExhaustedError(err) || biz.IsBudgetDenied(err) || biz.IsBudgetDenied(err) {
		return biz.HealthGateOutcomeNeutral
	}
	if healthGatePipelineTimeout(ctx) {
		return biz.HealthGateOutcomeFailure
	}
	if healthGateClientCanceled(ctx) || errors.Is(err, context.Canceled) {
		return biz.HealthGateOutcomeNeutral
	}
	if errors.Is(err, pipeline.ErrEmptyResponse) || errors.Is(err, pipeline.ErrEmptyStreamChunks) ||
		errors.Is(err, pipeline.ErrEmptyAggregatedBody) || errors.Is(err, pipeline.ErrStreamFirstEventTimeout) ||
		errors.Is(err, pipeline.ErrNonStreamResponseTimeout) || errors.Is(err, context.DeadlineExceeded) ||
		isRetryableTransportError(err) {
		return biz.HealthGateOutcomeFailure
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return biz.HealthGateOutcomeFailure
	}
	status := ExtractStatusCodeFromError(err)
	if status >= 500 || status == 401 || status == 403 || status == 408 {
		return biz.HealthGateOutcomeFailure
	}
	return biz.HealthGateOutcomeNeutral
}
