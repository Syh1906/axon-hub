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
	"github.com/looplj/axonhub/internal/server/events"
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
	plugins PluginIntegration,
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

	adaptiveLoadBalancer := NewLoadBalancer(channelService, channelService,
		NewErrorAwareStrategy(),
		NewWeightRoundRobinStrategy(),
		NewLatencyAwareStrategy(),
		rateLimitStrategy,
	)

	// Failover and circuit-breaker strategies never read channel metrics, so
	// these load balancers skip the per-candidate metrics fetch.
	failoverLoadBalancer := NewLoadBalancer(nil, channelService,
		NewWeightStrategy(), NewRandomStrategy(), rateLimitStrategy)

	circuitBreakerLoadBalancer := NewLoadBalancer(nil, channelService,
		NewWeightStrategy(), NewModelAwareCircuitBreakerStrategy(modelCircuitBreaker), rateLimitStrategy)
	healthGatedLoadBalancer := NewLoadBalancer(channelService, channelService,
		NewWeightRoundRobinStrategy(),
		NewLatencyAwareStrategy(),
		rateLimitStrategy,
	).WithHealthGate(channelService.HealthGate())

	roundRobinHealthFilter := NewRoundRobinHealthStrategy()
	roundRobinLoadBalancer := NewLoadBalancer(channelService, channelService,
		NewRoundRobinStrategy(),
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
		Plugins:                    plugins,
		proxy:                      nil,
		responsesSessions:          sessions,
		EventBus:                   channelService.EventBus,
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

	// Plugins carries request hooks. A zero value adds no middleware.
	Plugins PluginIntegration

	// proxy is the proxy configuration for testing
	// If set, it will override the channel's default proxy configuration
	proxy *httpclient.ProxyConfig

	responsesSessions *responsesSessionStore

	// EventBus publishes plugin events. Nil skips publication. FX threads the
	// process bus through ChannelService, and the constructor copies it here.
	EventBus *events.Bus
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

// WithEventBus returns a copy that publishes plugin events to bus.
// A nil bus skips publication. The bus is passed on the orchestrator, not on context.
func (processor *ChatCompletionOrchestrator) WithEventBus(bus *events.Bus) *ChatCompletionOrchestrator {
	c := *processor
	c.EventBus = bus

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
	RetainResponsesContinuation   func(responseBody []byte) error
	Route                         ResponseRoute
	CodexResponseHeadersSupported bool
}

func (processor *ChatCompletionOrchestrator) Process(ctx context.Context, request *httpclient.Request) (ChatCompletionResult, error) {
	return processor.processWithTitleHedgeDelay(ctx, request, titleHedgeDelay)
}

func (processor *ChatCompletionOrchestrator) processWithTitleHedgeDelay(ctx context.Context, request *httpclient.Request, hedgeDelay time.Duration) (completion ChatCompletionResult, processErr error) {
	lifecycleOwner, executionCtx := newStreamLifecycle(ctx)
	ctx = executionCtx
	var preparedResponsesBody []byte
	var continuation *responsesContinuation
	defer func() {
		if continuation != nil {
			continuation.releaseRetention()
			continuation.releaseCapture()
		}
	}()
	if shared.IsResponsesAPI(ctx) {
		ctx = shared.WithResponsesAttempt(ctx, &shared.ResponsesAttempt{})
	}

	// API key providers cannot return a derived context, so install the shared
	// request container before provider selection mutates it.
	ctx = contexts.EnsureContainer(ctx)

	// The context is system bypassed to allow the orchestrator to access the system settings.
	ctx = authz.WithSystemBypass(ctx, "process-chat-completion")

	apiKey, _ := contexts.GetAPIKey(ctx)

	settings := processor.loadRequestSettings(ctx)
	ctx = withRequestSettings(ctx, settings)
	retryPolicy := &settings.RetryPolicy
	var healthGateDecision *healthGateRequestDecision
	ctx = context.WithValue(ctx, healthGateDecisionContextKey{}, &healthGateDecision)

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "chat request received",
			inboundRequestDebugFields(request, retryPolicy)...,
		)
	}

	state := &PersistenceState{
		titleTask:             isCodexTitleRequest(request),
		ResponsesContinuation: continuation,
		APIKey:                apiKey,
		RequestService:        processor.RequestService,
		UsageLogService:       processor.UsageLogService,
		ChannelService:        processor.ChannelService,
		PromptProvider:        processor.PromptProvider,
		PromptProtecter:       processor.PromptProtecter,
		CandidateSelector:     processor.channelSelector,
		LoadBalancers: map[string]*LoadBalancer{
			biz.LoadBalancerStrategyAdaptive:       processor.adaptiveLoadBalancer,
			biz.LoadBalancerStrategyFailover:       processor.failoverLoadBalancer,
			biz.LoadBalancerStrategyCircuitBreaker: processor.circuitBreakerLoadBalancer,
			biz.LoadBalancerStrategyRoundRobin:     processor.roundRobinLoadBalancer,
			biz.LoadBalancerStrategyHealthGated:    processor.healthGatedLoadBalancer,
		},
		RoutingPolicy:         deriveRoutingPolicy(retryPolicy, apiKey, nil),
		Settings:              settings,
		RetryPolicyProvider:   processor.SystemService,
		ModelMapper:           processor.ModelMapper,
		Proxy:                 processor.proxy,
		CurrentCandidateIndex: 0,
		EventBus:              processor.EventBus,
	}
	defer func() {
		if processErr != nil && state.Request != nil {
			completion.RequestID = state.Request.ID
			persistRequestFailure(ctx, state, processErr)
			publishRequestFailed(ctx, state, processErr)
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

	hooks, err := processor.Plugins.RequestHooks(ctx)
	if err != nil {
		lifecycleOwner.Close()
		return ChatCompletionResult{}, err
	}
	state.PluginHooks = hooks
	pipe, outbound, healthGateTracker := processor.requestPipeline(state, retryPolicy)

	prepared, err := pipe.Prepare(ctx, request)
	var result *pipeline.Result
	if err == nil && canHedgeTitle(ctx, state, prepared, retryPolicy) {
		outcome := processor.raceTitle(ctx, state, prepared, pipe, outbound, healthGateTracker, retryPolicy, hedgeDelay)
		state, outbound, healthGateTracker, ctx = outcome.state, outcome.outbound, outcome.tracker, outcome.ctx
		result, err = outcome.result, outcome.err
		continuation = state.ResponsesContinuation
		if err == nil && state.RequestExec != nil {
			err = state.RequestService.AdoptRequestExecution(ctx, state.RequestExec,
				state.RoutingPolicy.LoadBalancerStrategy != biz.LoadBalancerStrategyHealthGated)
			state.Request.ChannelID = state.RequestExec.ChannelID
			state.Request.ResultExecutionID = &state.RequestExec.ID
		}
		if result != nil && result.EventStream != nil {
			source := result.EventStream
			result.EventStream = httpclient.WithResponseHeaders(preserveDeliveryCapabilities(source, &titleOwnedStream{Stream: source, owner: lifecycleOwner}), httpclient.GetResponseHeaders(source))
			if err != nil {
				_ = result.EventStream.Close()
			} else {
				result.EventStream = withTitleWinnerPreview(ctx, state, processor.LiveStreamRegistry, result.EventStream)
			}
		} else if outcome.lifecycle != nil {
			outcome.lifecycle.Close()
		}
	} else if err == nil {
		result, err = pipe.ProcessPrepared(ctx, prepared)
	}
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

		// Finish the failed execution, unless plugin admission stopped this attempt
		// before persistence ran: this attempt has no execution record.
		if requestExec := outbound.GetRequestExecution(); requestExec != nil && !isPluginOverloaded(err) {
			if updateErr := persistRequestExecutionFailure(
				persistCtx,
				processor.RequestService,
				requestExec,
				err,
				nil,
				nil,
				"",
				state.executionResponseHeaders(requestExec.ID),
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
	if ch := outbound.GetCurrentChannel(); ch != nil {
		route.ChannelID = ch.ID
	}
	if state.RawProviderRequest != nil {
		route.Model = gjson.GetBytes(state.RawProviderRequest.Body, "model").String()
		route.APIFormat = state.RawProviderRequest.APIFormat
	}
	if route.Model == "" && state.RequestExec != nil {
		route.Model = state.RequestExec.ModelID
	}
	if continuation != nil && shared.IsResponsesWebSocket(ctx) {
		preparedResponsesBody = continuation.snapshotBody
	}
	retainedContinuation := continuation
	if result.Stream && continuation != nil {
		source := result.EventStream
		accepted := &responsesAcceptedStream{Stream: source, ctx: ctx, continuation: continuation}
		result.EventStream = httpclient.WithResponseHeaders(preserveDeliveryCapabilities(source, accepted), httpclient.GetResponseHeaders(source))
		continuation = nil
	} else if continuation != nil && result.Response != nil {
		continuation.accept(ctx)
	}
	source := result.EventStream
	completion, processErr = processor.prepareResponseDelivery(ctx, state, request, preparedResponsesBody, result, retainedContinuation)
	if result.Stream && completion.ChatCompletionStream != nil {
		completion.ChatCompletionStream = httpclient.WithResponseHeaders(preserveDeliveryCapabilities(source, completion.ChatCompletionStream), httpclient.GetResponseHeaders(source))
	} else {
		lifecycleOwner.Close()
	}
	completion.Route = route
	completion.CodexResponseHeadersSupported = outbound.SupportsCodexResponseHeaders()
	return completion, processErr
}

func maybeEvaluateChannelAPIKeyRulesOnFailure(
	ctx context.Context,
	outbound *PersistentOutboundTransformer,
	rawErr error,
) {
	if isLocalAdmissionError(rawErr) {
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
