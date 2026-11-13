package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

type preparedRequestPipeline interface {
	Prepare(context.Context, *httpclient.Request) (*llm.Request, error)
	Process(context.Context, *httpclient.Request) (*pipeline.Result, error)
	ProcessPrepared(context.Context, *llm.Request, ...pipeline.Option) (*pipeline.Result, error)
}

// requestPipeline creates attempt-local middleware and persistence state.
func (processor *ChatCompletionOrchestrator) requestPipeline(state *PersistenceState, retryPolicy *biz.RetryPolicy) (preparedRequestPipeline, *PersistentOutboundTransformer, *healthGateAttemptTracker) {
	var pipelineOpts []pipeline.Option
	// Only apply retry if policy is enabled
	if retryPolicy.Enabled {
		pipelineOpts = append(pipelineOpts, pipeline.WithRetry(
			retryPolicy.MaxChannelRetries,
			retryPolicy.MaxSingleChannelRetries,
			time.Duration(retryPolicy.RetryDelayMs)*time.Millisecond,
		))

		if retryPolicy.EmptyResponseDetection {
			pipelineOpts = append(pipelineOpts, pipeline.WithEmptyResponseDetection())
		}

		pipelineOpts = append(pipelineOpts, pipeline.WithResponseTimeouts(
			time.Duration(retryPolicy.StreamFirstEventTimeoutSeconds)*time.Second,
			time.Duration(retryPolicy.NonStreamResponseTimeoutSeconds)*time.Second,
		))
	} else {
		// Retries are switched off globally: a channel-level override must not
		// turn same-channel retries back on.
		pipelineOpts = append(pipelineOpts, pipeline.WithRetriesDisabled())
	}

	var middlewares []pipeline.Middleware

	// Add global middlewares
	middlewares = append(middlewares, processor.Middlewares...)
	middlewares = append(middlewares, newBillingSystemMessageMiddleware(state))
	middlewares = append(middlewares, state.PluginHooks.requestStage()...)

	inbound, outbound := NewPersistentTransformers(state, processor.Inbound, middlewares...)
	healthGateTracker := withHealthGate(outbound, processor.ChannelService.HealthGate(), retryPolicy.HealthGateOrDefault())

	// Add inbound middlewares (executed after inbound.TransformRequest)
	middlewares = append(middlewares,
		registerRequest(inbound),
		enforceQuota(inbound, processor.QuotaService),
		applyAutoReasoningEffort(state.settings()),
		checkApiKeyModelAccess(inbound),
		applyModelMapping(inbound),
		selectCandidates(inbound, processor.quotaProvider),
		injectPrompts(inbound),
		protectPrompts(inbound),
		// Response pass-through middlewares run before persistRequest so the raw provider
		// response is saved when pass-through is enabled.
		applyPassThroughResponse(outbound),
		applyPassThroughStream(outbound),
		persistRequest(inbound),
	)

	// Add outbound middlewares (executed after outbound.TransformRequest)
	middlewares = append(middlewares,
		// applyPassThroughBody runs first so that override operations can still modify the pass-through body.
		applyPassThroughRequestBody(outbound, processor.Plugins.Hooks),
		// Responses HTTP/WS adaptation reuses the effective body before continuation and overrides.
		applyResponsesTransportRequestBody(outbound),
		// Codex Responses metadata must travel with a pass-through body so compatible
		// upstreams preserve the client's protocol behavior.
		applyPassThroughRequestHeaders(outbound),
		// Set Anthropic cache TTLs after pass-through and before channel body overrides.
		applyForceOneHourPromptCache(outbound),
		applyResponsesContinuationBody(outbound),
	)
	middlewares = append(middlewares, state.PluginHooks.attemptStage(outbound)...)
	middlewares = append(middlewares,
		applyOverrideRequestBody(outbound),
		// applyUserAgentPassThrough runs before header overrides to set the initial
		// User-Agent value (either from client pass-through or default "axonhub/1.0").
		// A provider-required User-Agent already set by the outbound transformer
		// (e.g. GitHubCopilotChat on Copilot channels) is preserved when
		// pass-through is disabled. Override headers can still modify the
		// User-Agent if configured.
		applyUserAgentPassThrough(outbound),
		applyOverrideRequestHeaders(outbound),
		// Remove transport-incompatible fields after pass-through and overrides,
		// so persistence and execution observe the same provider request.
		applyUpstreamAddress(outbound),
		verifyResponsesAttempt(outbound),
		finalizeTransportRequest(outbound),
		// Adapt only the selected channel's final Responses body, including restored
		// history and pass-through, before recording or sending the attempt.
		applyCodexPageContextCompatibility(outbound),
		optimizeRequestImages(outbound),

		// Unified performance tracking middleware.
		withoutResponsesRecoveryPenalty(withPerformanceRecording(outbound)),

		withoutResponsesRecoveryPenalty(withModelCircuitBreaker(outbound, processor.modelCircuitBreaker)),
		healthGateTracker,

		// The request execution middleware must be the final middleware
		// to ensure that the request execution is created with the correct request bodys.
		persistRequestExecution(outbound),

		// Forward the events to the live streaming.
		withLivePreview(state, processor.LiveStreamRegistry),

		// Per-channel concurrency admission runs before RPM admission so a locally
		// rejected queue attempt does not consume RPM for a request that never
		// reached upstream.
		withChannelLimiter(outbound, processor.channelLimiterManager, processor.channelLimiterMetrics),
		// Strict single-instance RPM admission for every outbound attempt.
		withBudgetAdmission(outbound),
		withRateLimitAdmission(outbound, processor.rateLimitTracker),
		// Rate limit tracking middleware for TPM and provider cooldown signals.
		withRateLimitTracking(outbound, processor.rateLimitTracker),

		// Response pass-through capture middlewares must be last in the outbound list
		// so they run first in reverse order (before any other OnOutboundRawResponse/OnOutboundRawStream handlers).
		captureRawProviderResponse(outbound),
		captureRawProviderStream(outbound),
		captureResponsesProvider(outbound),
		// Run first on raw responses (reverse order), before capture fan-out.
		closeCompletedAnthropicStream(outbound),
	)

	pipelineOpts = append(pipelineOpts, pipeline.WithMiddlewares(middlewares...))

	pipe := processor.PipelineFactory.Pipeline(
		inbound,
		outbound,
		pipelineOpts...,
	)

	return pipe, outbound, healthGateTracker
}
