package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/metrics"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/cc"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func NewChatCompletionOrchestrator(
	channelService *biz.ChannelService,
	defaultSelector *DefaultSelector,
	requestService *biz.RequestService,
	httpClient *httpclient.HttpClient,
	inbound transformer.Inbound,
	systemService *biz.SystemService,
	usageLogService *biz.UsageLogService,
	promptService *biz.PromptService,
	quotaService *biz.QuotaService,
	promptProtectionRuleService *biz.PromptProtectionRuleService,
	liveStreamRegistry *biz.LiveStreamRegistry,
	channelLimiterManager *ChannelLimiterManager,
	quotaProvider ProviderQuotaStatusProvider,
) *ChatCompletionOrchestrator {
	// All protocol entry points share the channel's usage and rate-limit window.
	rateLimitTracker := channelLimiterManager.requestTracker

	channelService.SetChannelLimiterForgetter(channelLimiterManager)

	channelLimiterMetrics, err := NewChannelLimiterMetrics(metrics.Meter, channelLimiterManager)
	if err != nil {
		log.Warn(context.Background(), "failed to register channel limiter metrics, continuing without them", log.Cause(err))
		channelLimiterMetrics = nil
	}

	// Initialize model circuit breaker
	modelCircuitBreaker := biz.NewModelCircuitBreaker()

	rateLimitStrategy := NewRateLimitAwareStrategy(rateLimitTracker, channelLimiterManager)

	adaptiveLoadBalancer := NewLoadBalancer(systemService, channelService,
		NewErrorAwareStrategy(channelService),
		NewWeightRoundRobinStrategy(channelService),
		NewLatencyAwareStrategy(channelService),
		rateLimitStrategy,
	)

	failoverLoadBalancer := NewLoadBalancer(systemService, channelService,
		NewWeightStrategy(), NewRandomStrategy(), rateLimitStrategy)

	circuitBreakerLoadBalancer := NewLoadBalancer(systemService, channelService,
		NewWeightStrategy(), NewModelAwareCircuitBreakerStrategy(modelCircuitBreaker), rateLimitStrategy)
	healthGatedLoadBalancer := NewLoadBalancer(systemService, channelService,
		NewWeightRoundRobinStrategy(channelService),
		NewLatencyAwareStrategy(channelService),
		rateLimitStrategy,
	).WithHealthGate(channelService.HealthGate())

	roundRobinHealthFilter := NewRoundRobinHealthStrategy(channelService)
	roundRobinLoadBalancer := NewLoadBalancer(systemService, channelService,
		NewRoundRobinStrategy(channelService),
		rateLimitStrategy,
	).WithStableTieOrder().WithRoundRobinHealthFilter(roundRobinHealthFilter)

	sessions := newResponsesSessionStore(requestService.LoadCompletedResponsesSession)
	sessions.save = requestService.SaveResponsesContext
	sessions.known = requestService.HasCompletedResponsesOrigin
	return &ChatCompletionOrchestrator{
		Inbound:            inbound,
		RequestService:     requestService,
		ChannelService:     channelService,
		SystemService:      systemService,
		UsageLogService:    usageLogService,
		QuotaService:       quotaService,
		LiveStreamRegistry: liveStreamRegistry,
		PromptProvider:     promptService,
		PromptProtecter:    promptProtectionRuleService,
		Middlewares: []pipeline.Middleware{
			cc.SystemCacheCompatibility(),
			stream.EnsureUsage(),
		},
		PipelineFactory:            pipeline.NewFactory(httpClient),
		ModelMapper:                NewModelMapper(),
		channelSelector:            defaultSelector,
		channelLimiterManager:      channelLimiterManager,
		channelLimiterMetrics:      channelLimiterMetrics,
		rateLimitTracker:           rateLimitTracker,
		adaptiveLoadBalancer:       adaptiveLoadBalancer,
		failoverLoadBalancer:       failoverLoadBalancer,
		circuitBreakerLoadBalancer: circuitBreakerLoadBalancer,
		healthGatedLoadBalancer:    healthGatedLoadBalancer,
		roundRobinLoadBalancer:     roundRobinLoadBalancer,
		modelCircuitBreaker:        modelCircuitBreaker,
		quotaProvider:              quotaProvider,
		proxy:                      nil,
		responsesSessions:          sessions,
	}
}

type ChatCompletionOrchestrator struct {
	Inbound            transformer.Inbound
	RequestService     *biz.RequestService
	ChannelService     *biz.ChannelService
	SystemService      *biz.SystemService
	UsageLogService    *biz.UsageLogService
	QuotaService       *biz.QuotaService
	LiveStreamRegistry *biz.LiveStreamRegistry
	PromptProvider     PromptProvider
	PromptProtecter    PromptProtecter
	Middlewares        []pipeline.Middleware
	PipelineFactory    *pipeline.Factory
	ModelMapper        *ModelMapper

	// The runtime fields.

	// The default channel selector.
	channelSelector CandidateSelector
	// The load balancer for channel load balancing.
	adaptiveLoadBalancer       *LoadBalancer
	failoverLoadBalancer       *LoadBalancer
	circuitBreakerLoadBalancer *LoadBalancer
	healthGatedLoadBalancer    *LoadBalancer
	roundRobinLoadBalancer     *LoadBalancer
	// channelLimiterManager owns per-channel concurrency admission control and
	// supplies in-flight / queue stats to the rate-limit-aware load-balancer strategy.
	channelLimiterManager *ChannelLimiterManager
	// channelLimiterMetrics emits OTel metrics for the limiter (gauges + counters
	// + histogram). May be nil in test setups that skip metric registration.
	channelLimiterMetrics *ChannelLimiterMetrics
	// The rate limit tracker for rate limit aware load balancing.
	rateLimitTracker *ChannelRequestTracker
	// The model circuit breaker for circuit-breaker load balancing.
	modelCircuitBreaker *biz.ModelCircuitBreaker
	// The provider quota status provider for quota-aware load balancing and selection.
	quotaProvider ProviderQuotaStatusProvider

	// proxy is the proxy configuration for testing
	// If set, it will override the channel's default proxy configuration
	proxy *httpclient.ProxyConfig

	responsesSessions *responsesSessionStore
}

func (processor *ChatCompletionOrchestrator) WithChannelSelector(selector CandidateSelector) *ChatCompletionOrchestrator {
	c := *processor
	c.channelSelector = selector

	return &c
}

func (processor *ChatCompletionOrchestrator) WithAllowedChannels(allowedChannelIDs []int) *ChatCompletionOrchestrator {
	c := *processor
	c.channelSelector = WithSelectedChannelsSelector(processor.channelSelector, allowedChannelIDs)

	return &c
}

func (processor *ChatCompletionOrchestrator) WithProxy(proxy *httpclient.ProxyConfig) *ChatCompletionOrchestrator {
	c := *processor
	c.proxy = proxy

	return &c
}

// ResponseRoute identifies the committed upstream attempt without retaining
// mutable persistence state or exposing provider credentials.
type ResponseRoute struct {
	ChannelID int
	Model     string
	APIFormat string
}

type ChatCompletionResult struct {
	RequestID            int
	ChatCompletion       *httpclient.Response
	ChatCompletionStream streams.Stream[*httpclient.StreamEvent]
	// 保存网关结束当前生成时的完整项目，供 HTTP 上游的下一轮恢复历史。
	RetainResponsesContinuation func(responseBody []byte) error
	Route ResponseRoute
	CodexResponseHeadersSupported bool
}

func (processor *ChatCompletionOrchestrator) Process(ctx context.Context, request *httpclient.Request) (completion ChatCompletionResult, processErr error) {
	lifecycleOwner, executionCtx := newStreamLifecycle(ctx)
	ctx = executionCtx
	var preparedResponsesBody []byte
	var continuation *responsesContinuation
 defer func() { if continuation != nil { continuation.releaseRetention(); continuation.releaseCapture() } }()
 if shared.IsResponsesAPI(ctx) { ctx = shared.WithResponsesAttempt(ctx, &shared.ResponsesAttempt{}) }

	// API key providers cannot return a derived context, so install the shared
	// request container before provider selection mutates it.
	ctx = contexts.EnsureContainer(ctx)

	// The context is system bypassed to allow the orchestrator to access the system settings.
	ctx = authz.WithSystemBypass(ctx, "process-chat-completion")

	apiKey, _ := contexts.GetAPIKey(ctx)

	// Get retry policy from system settings
	retryPolicy := processor.SystemService.RetryPolicyOrDefault(ctx)
	var healthGateDecision *healthGateRequestDecision
	ctx = context.WithValue(ctx, healthGateDecisionContextKey{}, &healthGateDecision)

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "chat request received",
			inboundRequestDebugFields(request, retryPolicy)...,
		)
	}

	state := &PersistenceState{
		ResponsesContinuation: continuation,
		APIKey:                apiKey,
		RequestService:        processor.RequestService,
		UsageLogService:       processor.UsageLogService,
		SystemService:         processor.SystemService,
		ChannelService:        processor.ChannelService,
		PromptProvider:        processor.PromptProvider,
		PromptProtecter:       processor.PromptProtecter,
		RetryPolicyProvider:   processor.SystemService,
		CandidateSelector:     processor.channelSelector,
		LoadBalancers: map[string]*LoadBalancer{
			biz.LoadBalancerStrategyAdaptive:       processor.adaptiveLoadBalancer,
			biz.LoadBalancerStrategyFailover:       processor.failoverLoadBalancer,
			biz.LoadBalancerStrategyCircuitBreaker: processor.circuitBreakerLoadBalancer,
			biz.LoadBalancerStrategyRoundRobin:     processor.roundRobinLoadBalancer,
			biz.LoadBalancerStrategyHealthGated:    processor.healthGatedLoadBalancer,
		},
		RoutingPolicy:         deriveRoutingPolicy(retryPolicy, apiKey, nil),
		ModelMapper:           processor.ModelMapper,
		Proxy:                 processor.proxy,
		CurrentCandidateIndex: 0,
	}
	defer func() {
		if processErr != nil && state.Request != nil {
			completion.RequestID = state.Request.ID
			persistRequestFailure(ctx, state, processErr)
		}
	}()
	if shared.IsResponsesAPI(ctx) && request != nil {
		// Register the original envelope before history restoration can reject it.
		// Normal protocols register immediately after parsing, before admission.
		metadata := &llm.Request{
			Model:           gjson.GetBytes(request.Body, "model").String(),
			Stream:          lo.ToPtr(gjson.GetBytes(request.Body, "stream").Bool()),
			ReasoningEffort: gjson.GetBytes(request.Body, "reasoning.effort").String(),
		}
		var err error
		state.Request, err = processor.RequestService.CreateRequest(ctx, metadata, request, persistedRequestAPIFormat(ctx, llm.APIFormatOpenAIResponse))
		if err != nil {
			return ChatCompletionResult{}, err
		}
		ctx = context.WithValue(ctx, responsesPreviousIDContextKey{}, responsePreviousID(request.Body))
		if processor.responsesSessions != nil && !shared.IsResponsesWebSocket(ctx) {
			var sessionID string
			var restoreErr error
			preparedResponsesBody, sessionID, restoreErr = processor.responsesSessions.prepare(ctx, request.Body)
			if restoreErr != nil {
				if ctx.Err() != nil {
					return ChatCompletionResult{}, ctx.Err()
				}
				ctx = shared.WithResponsesRestoreError(ctx, restoreErr)
			}
			request.Body = preparedResponsesBody
			ctx = shared.WithSessionID(ctx, sessionID)
		}
		continuation = newResponsesContinuation(processor.responsesSessions, request.Body)
		state.ResponsesContinuation = continuation
	}

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

	inbound, outbound := NewPersistentTransformers(state, processor.Inbound, middlewares...)
	healthGateTracker := withHealthGate(outbound, processor.ChannelService.HealthGate(), retryPolicy.HealthGateOrDefault())

	// Add inbound middlewares (executed after inbound.TransformRequest)
	middlewares = append(middlewares,
		registerRequest(inbound),
		enforceQuota(inbound, processor.QuotaService),
		applyAutoReasoningEffort(processor.SystemService),
		checkApiKeyModelAccess(inbound),
		applyModelMapping(inbound),
		selectCandidates(inbound, processor.quotaProvider, processor.SystemService),
		injectPrompts(inbound),
		protectPrompts(inbound),
		// Response pass-through middlewares run before persistRequest so the raw provider
		// response is saved when pass-through is enabled.
		applyPassThroughResponse(outbound, processor.SystemService),
		applyPassThroughStream(outbound, processor.SystemService),
		persistRequest(inbound),
	)

	// Add outbound middlewares (executed after outbound.TransformRequest)
	middlewares = append(middlewares,
		// applyPassThroughBody runs first so that override operations can still modify the pass-through body.
		applyPassThroughRequestBody(outbound, processor.SystemService),
		// Responses HTTP/WS adaptation reuses the effective body before continuation and overrides.
		applyResponsesTransportRequestBody(outbound, processor.SystemService),
		// Codex Responses metadata must travel with a pass-through body so compatible
		// upstreams preserve the client's protocol behavior.
		applyPassThroughRequestHeaders(outbound),
		// Set Anthropic cache TTLs after pass-through and before channel body overrides.
		applyForceOneHourPromptCache(outbound),
		applyResponsesContinuationBody(outbound),
		applyOverrideRequestBody(outbound),
		// applyUserAgentPassThrough runs before header overrides to set the initial
		// User-Agent value (either from client pass-through or default "axonhub/1.0").
		// A provider-required User-Agent already set by the outbound transformer
		// (e.g. GitHubCopilotChat on Copilot channels) is preserved when
		// pass-through is disabled. Override headers can still modify the
		// User-Agent if configured.
		applyUserAgentPassThrough(outbound, processor.SystemService),
		applyOverrideRequestHeaders(outbound),
		// Remove transport-incompatible fields after pass-through and overrides,
		// so persistence and execution observe the same provider request.
		applyUpstreamAddress(outbound),
		verifyResponsesAttempt(outbound),
		finalizeTransportRequest(outbound),

		// Unified performance tracking middleware.
		withoutResponsesRecoveryPenalty(withPerformanceRecording(outbound)),

		withoutResponsesRecoveryPenalty(withModelCircuitBreaker(outbound, processor.modelCircuitBreaker)),
		healthGateTracker,

		// The request execution middleware must be the final middleware
		// to ensure that the request execution is created with the correct request bodys.
		persistRequestExecution(outbound),

		// Forward the events to the live streaming.
		withLivePreview(state, processor.SystemService, processor.LiveStreamRegistry),

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
		captureRawProviderResponse(outbound, processor.SystemService),
		captureRawProviderStream(outbound, processor.SystemService),
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

	result, err := pipe.Process(ctx, request)
	// Process owns non-stream attempts; streaming attempts transfer ownership to
	// budgetStream.Close, which may run long after this method returns.
	if err == nil && !result.Stream && state.BudgetAttempt != nil {
		state.BudgetAttempt.Stop()
		state.BudgetAttempt = nil
	}
	if err != nil {
		err = classifyRequestFailure(ctx, err)
		if endErr := outbound.endBudgetAttempt(ctx); endErr != nil {
			log.Error(ctx, "close budget attempt", log.Cause(endErr))
		}
		defer lifecycleOwner.Close()
		persistCtx, cancel := xcontext.DetachWithTimeout(ctx, time.Second*10)
		defer cancel()

		// Update the last request execution status based on error if it exists
		// This ensures that when retry fails completely, the last execution is properly marked
		if requestExec := outbound.GetRequestExecution(); requestExec != nil {
			if updateErr := persistRequestExecutionFailure(
				persistCtx,
				processor.RequestService,
				requestExec.ID,
				err,
				nil,
				nil,
			); updateErr != nil {
				log.Warn(persistCtx, "Failed to update request execution status from error", log.Cause(updateErr))
			}
			if !errors.Is(err, shared.ErrResponsesContinuationUnavailable) {
				maybeEvaluateChannelAPIKeyRulesOnFailure(persistCtx, outbound, err)
			}
		}

		requestID := 0
		if state.Request != nil {
			requestID = state.Request.ID
		}
		return ChatCompletionResult{RequestID: requestID}, healthGateTracker.finalize(ctx, err, false)
	}
	// Record the provider outcome without replacing the already received response.
	_ = healthGateTracker.finalize(ctx, state.ResponseTerminalError, result.Stream)

	var route ResponseRoute
 if ch := outbound.GetCurrentChannel(); ch != nil { route.ChannelID = ch.ID }
 if state.RawProviderRequest != nil { route.Model = gjson.GetBytes(state.RawProviderRequest.Body, "model").String(); route.APIFormat = state.RawProviderRequest.APIFormat }
 if route.Model == "" && state.RequestExec != nil { route.Model = state.RequestExec.ModelID }
 if continuation != nil && shared.IsResponsesWebSocket(ctx) { preparedResponsesBody = continuation.snapshotBody }
 if result.Stream && continuation != nil {
  source := result.EventStream
  accepted := &responsesAcceptedStream{Stream: source, ctx: ctx, continuation: continuation}
  result.EventStream = httpclient.WithResponseHeaders(preserveDeliveryCapabilities(source, accepted), httpclient.GetResponseHeaders(source))
  continuation = nil
 } else if continuation != nil && result.Response != nil { continuation.accept(ctx) }
 source := result.EventStream
 completion, processErr = processor.prepareResponseDelivery(ctx, state, request, preparedResponsesBody, result)
 if result.Stream && completion.ChatCompletionStream != nil { completion.ChatCompletionStream = httpclient.WithResponseHeaders(preserveDeliveryCapabilities(source, completion.ChatCompletionStream), httpclient.GetResponseHeaders(source)) } else { lifecycleOwner.Close() }
 completion.Route = route
 completion.CodexResponseHeadersSupported = outbound.SupportsCodexResponseHeaders()
 return completion, processErr
}

func maybeEvaluateChannelAPIKeyRulesOnFailure(
	ctx context.Context,
	outbound *PersistentOutboundTransformer,
	rawErr error,
) {
	if biz.IsBudgetDenied(rawErr) || isChannelCooldownError(rawErr) {
		return
	}
	if outbound == nil || outbound.state == nil || outbound.state.ChannelService == nil {
		return
	}
	if outbound.state.Perf != nil && outbound.state.Perf.RequestCompleted {
		return
	}

	channel := outbound.GetCurrentChannel()
	if channel == nil {
		return
	}

	apiKey, ok := contexts.GetChannelAPIKey(ctx)
	if !ok && outbound.state.Perf != nil {
		apiKey = outbound.state.Perf.APIKey
	}
	if apiKey == "" {
		return
	}

	outbound.state.ChannelService.EvaluateAPIKeyRulesForFailure(
		ctx,
		channel.ID,
		apiKey,
		ExtractErrorCode(rawErr),
		extractErrorMessageForMatching(rawErr),
	)
}
