package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func healthGateOrchestratorFixture(t *testing.T, ctx context.Context, client *ent.Client, executor pipeline.Executor, channels ...*ent.Channel) (context.Context, *ChatCompletionOrchestrator, *biz.HealthGate) {
	t.Helper()
	project := createTestProject(t, ctx, client)
	ctx = contexts.WithProjectID(ctx, project.ID)
	channelService, requestService, systemService, usageLogService := setupTestServices(t, client)
	gate := channelService.HealthGate()
	candidates := make([]*ChannelModelsCandidate, 0, len(channels))
	for index, ch := range channels {
		outbound, err := openai.NewOutboundTransformer(ch.BaseURL, ch.Credentials.APIKey)
		require.NoError(t, err)
		candidates = append(candidates, &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: ch, Outbound: outbound}, Priority: index,
			Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
		})
	}
	loaded := make([]*biz.Channel, 0, len(candidates))
	for _, candidate := range candidates {
		loaded = append(loaded, candidate.Channel)
	}
	channelService.SetEnabledChannelsForTest(loaded)
	policy := &biz.RetryPolicy{
		Enabled: true, MaxChannelRetries: len(channels) - 1, MaxSingleChannelRetries: 0,
		LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated, HealthGate: &biz.HealthGatePolicy{
			FailureThreshold: 2, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600,
			ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300,
		},
	}
	require.NoError(t, systemService.SetRetryPolicy(ctx, policy))
	processor := &ChatCompletionOrchestrator{
		channelSelector: &staticChannelSelector{candidates: candidates},
		Inbound:         openai.NewInboundTransformer(), RequestService: requestService,
		ChannelService: channelService, PromptProvider: &stubPromptProvider{},
		SystemService: systemService, UsageLogService: usageLogService,
		PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(),
		channelLimiterManager:   NewChannelLimiterManager(),
		Middlewares:             []pipeline.Middleware{stream.EnsureUsage()},
		healthGatedLoadBalancer: NewLoadBalancer(channelService, channelService).WithHealthGate(gate),
	}
	return ctx, processor, gate
}

func healthGateServerError() error {
	return &httpclient.Error{StatusCode: 500, Body: []byte(`{"error":{"message":"secret upstream failure","type":"api_error"}}`)}
}

func healthGateResponse() *httpclient.Response {
	return &httpclient.Response{StatusCode: 200, Body: buildMockOpenAIResponse("completion", "gpt-4", "hello", 1, 2), Headers: http.Header{"Content-Type": {"application/json"}}}
}

func TestHealthGateOrchestrator_OpensAndSkipsBrokenChannel(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	second, err := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Backup Channel").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "backup-key"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").Save(ctx)
	require.NoError(t, err)
	executor := &sequenceExecutor{steps: []executorStep{
		{err: healthGateServerError()}, {resp: healthGateResponse()},
		{err: healthGateServerError()}, {resp: healthGateResponse()},
		{resp: healthGateResponse()},
	}}
	processCtx, orchestrator, gate := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
	for range 3 {
		result, err := orchestrator.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
		require.NoError(t, err)
		require.NotNil(t, result.ChatCompletion)
	}
	require.Len(t, executor.requests, 5, "the opened first channel must be skipped on the third request")
	cfg := biz.ResolveHealthGateConfig(orchestrator.SystemService.RetryPolicyOrDefault(processCtx).HealthGateOrDefault(), &biz.Channel{Channel: first})
	view := healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: first.ID, ActualModel: "gpt-4"}, healthGateResolver(cfg))
	require.Equal(t, biz.HealthGateStateOpen, view.State)
}

func TestHealthGateOrchestrator_OwnerOpensDuringFailoverAndMigratesImmediately(t *testing.T) {
	ctx, client := setupTest(t)
	primary := createTestChannel(t, ctx, client)
	backup, err := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Backup Channel").
		SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "backup-key"}).
		SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").Save(ctx)
	require.NoError(t, err)
	executor := &sequenceExecutor{steps: []executorStep{{err: healthGateServerError()}, {resp: healthGateResponse()}}}
	processCtx, processor, gate := healthGateOrchestratorFixture(t, ctx, client, executor, primary, backup)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.TraceStickyMode = biz.TraceStickyPreferPreviousChannel
	policy.HealthGate.FailureThreshold = 1
	policy.HealthGate.OwnerFailoverThreshold = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	processCtx = contexts.WithTrace(processCtx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(processor.RequestService, processCtx, 20, 10, biz.SessionOwner{ChannelID: primary.ID})
	result, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.NotNil(t, result.ChatCompletion)
	require.Len(t, executor.requests, 2)
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), &biz.Channel{Channel: primary})
	require.Equal(t, biz.HealthGateStateOpen, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: primary.ID, ActualModel: "gpt-4"}, healthGateResolver(cfg)).State)
	owner, found, err := processor.RequestService.GetSessionOwner(processCtx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, backup.ID, owner.ChannelID)
	require.Zero(t, owner.ConsecutiveFailovers)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), owner.ExpiresAt, 5*time.Second)
	requests, err := client.Request.Query().All(processCtx)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.NotNil(t, requests[0].RoutingDecision)
	require.Equal(t, objects.RoutingMigrationReasonOwnerOpen, requests[0].RoutingDecision.Migration.Reason)
	require.False(t, requests[0].RoutingDecision.TemporaryFailover)
}

type healthGateTimeoutExecutor struct {
	requests []*httpclient.Request
}

func (e *healthGateTimeoutExecutor) Do(ctx context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	e.requests = append(e.requests, request)
	if strings.Contains(request.URL, "api.openai.com") {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return healthGateResponse(), nil
}

func (e *healthGateTimeoutExecutor) DoStream(context.Context, *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, errors.New("streaming not supported")
}

func TestHealthGateOrchestrator_NonStreamTimeoutCountsBeforeFailover(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	backup, err := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Backup Channel").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "backup-key"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").Save(ctx)
	require.NoError(t, err)
	executor := &healthGateTimeoutExecutor{}
	processCtx, orchestrator, gate := healthGateOrchestratorFixture(t, ctx, client, executor, first, backup)
	policy := orchestrator.SystemService.RetryPolicyOrDefault(processCtx)
	policy.NonStreamResponseTimeoutSeconds = 1
	require.NoError(t, orchestrator.SystemService.SetRetryPolicy(processCtx, policy))
	for range policy.HealthGate.FailureThreshold + 1 {
		result, processErr := orchestrator.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
		require.NoError(t, processErr)
		require.NotNil(t, result.ChatCompletion)
	}
	require.Len(t, executor.requests, 5, "after two timeouts the first channel must be gated")
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), &biz.Channel{Channel: first})
	require.Equal(t, biz.HealthGateStateOpen, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: first.ID, ActualModel: "gpt-4"}, healthGateResolver(cfg)).State)
}

func TestHealthGateOrchestrator_LastResortErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastErr   error
		expect503 bool
	}{
		{"failure", healthGateServerError(), true},
		{"bad request", &httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"message":"bad input"}}`)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ch := createTestChannel(t, ctx, client)
			executor := &sequenceExecutor{steps: []executorStep{{err: healthGateServerError()}, {err: healthGateServerError()}, {err: tc.lastErr}}}
			processCtx, processor, gate := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
			for range 2 {
				_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
				require.Error(t, err)
			}
			_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
			require.Error(t, err)
			var responseErr *llm.ResponseError
			require.ErrorAs(t, err, &responseErr)
			if tc.expect503 {
				require.Equal(t, 503, responseErr.StatusCode)
				require.Equal(t, "service_unavailable", responseErr.Detail.Code)
				require.NotContains(t, err.Error(), ch.Name)
				require.NotContains(t, err.Error(), "secret upstream failure")
				executions, queryErr := client.RequestExecution.Query().All(processCtx)
				require.NoError(t, queryErr)
				require.Len(t, executions, 3)
				require.Contains(t, executions[2].ErrorMessage, "secret upstream failure")
			} else {
				require.Equal(t, 400, responseErr.StatusCode)
			}
			cfg := biz.ResolveHealthGateConfig(processor.SystemService.RetryPolicyOrDefault(processCtx).HealthGateOrDefault(), &biz.Channel{Channel: ch})
			expectedState := biz.HealthGateStateProbing
			if tc.expect503 {
				expectedState = biz.HealthGateStateOpen
			}
			require.Equal(t, expectedState, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: ch.ID, ActualModel: "gpt-4"}, healthGateResolver(cfg)).State)
		})
	}
}

func TestHealthGateOrchestrator_StreamAfterFirstTokenIsUnstable(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	streamErr := errors.New("upstream disconnected")
	executor := &mockExecutorWithErrorStream{
		events:    []*httpclient.StreamEvent{{Data: []byte(`{"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"hello"}}]}`)}},
		streamErr: streamErr,
	}
	processCtx, processor, gate := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
	result, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", true))
	require.NoError(t, err)
	require.NotNil(t, result.ChatCompletionStream)
	for result.ChatCompletionStream.Next() {
		_ = result.ChatCompletionStream.Current()
	}
	require.Error(t, result.ChatCompletionStream.Err())
	require.NoError(t, result.ChatCompletionStream.Close())
	cfg := biz.ResolveHealthGateConfig(processor.SystemService.RetryPolicyOrDefault(processCtx).HealthGateOrDefault(), &biz.Channel{Channel: ch})
	snapshots := healthGateSnapshot(t, gate, ch.ID, healthGateResolver(cfg))
	require.Len(t, snapshots, 1)
	require.Equal(t, 0, snapshots[0].ConsecutiveFailures)
	require.Equal(t, biz.HealthGateStateUnstable, snapshots[0].State)
}

func TestHealthGateOrchestrator_OtherStrategiesDoNotCreateEntries(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	processCtx, processor, gate := healthGateOrchestratorFixture(t, ctx, client, &sequenceExecutor{steps: []executorStep{{err: healthGateServerError()}}}, ch)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.LoadBalancerStrategy = biz.LoadBalancerStrategyAdaptive
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.Error(t, err)
	require.Empty(t, gate.ChannelIDsWithEntries())
}

func TestHealthGateOrchestrator_BusyLastResortReturns503WithoutCounting(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	executor := &sequenceExecutor{}
	processCtx, processor, gate := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
	cfg := biz.ResolveHealthGateConfig(processor.SystemService.RetryPolicyOrDefault(processCtx).HealthGateOrDefault(), &biz.Channel{Channel: ch})
	key := biz.HealthGateKey{ChannelID: ch.ID, ActualModel: "gpt-4"}
	for range 2 {
		ticket, ok := healthGateBegin(t, gate, key, healthGateResolver(cfg), false, false)
		require.True(t, ok)
		healthGateFinish(t, gate, ticket, healthGateResolver(cfg), biz.HealthGateOutcomeFailure, biz.HealthGateErrorInfo{StatusCode: 500})
	}
	holder, ok := healthGateBegin(t, gate, key, healthGateResolver(cfg), false, true)
	require.True(t, ok)
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	var responseErr *llm.ResponseError
	require.ErrorAs(t, err, &responseErr)
	require.Equal(t, 503, responseErr.StatusCode)
	require.Equal(t, "service_unavailable", responseErr.Detail.Code)
	require.Empty(t, executor.requests)
	require.Equal(t, 2, healthGateSnapshot(t, gate, ch.ID, healthGateResolver(cfg))[0].ConsecutiveFailures)
	healthGateFinish(t, gate, holder, healthGateResolver(cfg), biz.HealthGateOutcomeNeutral, biz.HealthGateErrorInfo{})
}

func TestHealthGateOrchestrator_SameModelRetriesCountOnceAndModelSwitchFinishesPrevious(t *testing.T) {
	channel := healthGateSelectorCandidate(1, 0, "first", "second")
	gate := biz.NewHealthGate(nil)
	policy := biz.HealthGatePolicy{FailureThreshold: 1, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600, ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300}
	state := &PersistenceState{
		RoutingPolicy:    EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
		CurrentCandidate: channel,
	}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy)
	ctx := context.Background()
	request := &httpclient.Request{}
	_, err := tracker.OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	tracker.OnOutboundRawError(ctx, healthGateServerError())
	_, err = tracker.OnOutboundRawRequest(ctx, request)
	require.NoError(t, err, "a same-model retry must reuse its ticket")
	require.NoError(t, tracker.finalize(ctx, nil, false))
	cfg := biz.ResolveHealthGateConfig(policy, channel.Channel)
	first := biz.HealthGateKey{ChannelID: channel.Channel.ID, ActualModel: "first"}
	require.Equal(t, biz.HealthGateStateHealthy, healthGateInspect(t, gate, first, healthGateResolver(cfg)).State)
	require.Zero(t, healthGateSnapshot(t, gate, channel.Channel.ID, healthGateResolver(cfg))[0].ConsecutiveFailures)

	tracker = withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy)
	_, err = tracker.OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	tracker.OnOutboundRawError(ctx, healthGateServerError())
	state.CurrentModelIndex = 1
	_, err = tracker.OnOutboundRawRequest(ctx, request)

	require.NoError(t, err)
	require.Equal(t, biz.HealthGateStateOpen, healthGateInspect(t, gate, first, healthGateResolver(cfg)).State)
	require.NoError(t, tracker.finalize(ctx, nil, false))
}

func TestHealthGateOrchestrator_CanceledLastResortPreservesCancellation(t *testing.T) {
	candidate := healthGateSelectorCandidate(1, 0, "model")
	candidate.healthGate = &healthGateCandidateInfo{lastResort: true}
	gate := biz.NewHealthGate(nil)
	state := &PersistenceState{RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated}, CurrentCandidate: candidate}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, biz.DefaultHealthGatePolicy())
	ctx, cancel := context.WithCancel(context.Background())
	_, err := tracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	tracker.OnOutboundRawError(ctx, healthGateServerError())
	cancel()
	require.ErrorIs(t, tracker.finalize(ctx, context.Canceled, false), context.Canceled)
	cfg := biz.ResolveHealthGateConfig(biz.DefaultHealthGatePolicy(), candidate.Channel)
	require.Equal(t, biz.HealthGateStateHealthy, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: 1, ActualModel: "model"}, healthGateResolver(cfg)).State)
}

func TestHealthGateOrchestrator_DisablingThresholdInvalidatesInFlightTicket(t *testing.T) {
	candidate := healthGateSelectorCandidate(1, 0, "model")
	policy := &biz.RetryPolicy{HealthGate: &biz.HealthGatePolicy{
		FailureThreshold: 1, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600,
		ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300,
	}}
	provider := &mockRetryPolicyProvider{policy: policy}
	gate := biz.NewHealthGate(nil)
	state := &PersistenceState{
		RoutingPolicy:       EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
		RetryPolicyProvider: provider, CurrentCandidate: candidate,
	}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy.HealthGateOrDefault())
	ctx := context.Background()
	_, err := tracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	policy.HealthGate.FailureThreshold = 0
	tracker.OnOutboundRawError(ctx, healthGateServerError())
	require.Error(t, tracker.finalize(ctx, healthGateServerError(), false))
	policy.HealthGate.FailureThreshold = 1
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), candidate.Channel)
	view := healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: candidate.Channel.ID, ActualModel: "model"}, healthGateResolver(cfg))
	require.Equal(t, biz.HealthGateStateHealthy, view.State)
	require.Zero(t, healthGateSnapshot(t, gate, candidate.Channel.ID, healthGateResolver(cfg))[0].ConsecutiveFailures)
}

func TestHealthGateOrchestrator_OldDisabledSelectionCannotResetNewCycle(t *testing.T) {
	now := time.Unix(1000, 0)
	candidate := healthGateSelectorCandidate(1, 0, "model")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{candidate}, biz.TraceStickyDisabled, nil)
	policy.HealthGate.FailureThreshold = 0
	ctx := context.Background()
	oldCandidates, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	oldPolicy := policy.HealthGateOrDefault()
	policy.HealthGate.FailureThreshold = 1
	newCandidates, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	makeTracker := func(candidates []*ChannelModelsCandidate, fallback biz.HealthGatePolicy) *healthGateAttemptTracker {
		state := &PersistenceState{
			RoutingPolicy:    EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
			CurrentCandidate: candidates[0], RetryPolicyProvider: selector.policy,
		}
		return withHealthGate(&PersistentOutboundTransformer{state: state}, gate, fallback)
	}
	newTracker := makeTracker(newCandidates, policy.HealthGateOrDefault())
	_, err = newTracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	oldTracker := makeTracker(oldCandidates, oldPolicy)
	_, err = oldTracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	oldTracker.OnOutboundRawError(ctx, errors.New("local conversion failed"))
	require.Error(t, oldTracker.finalize(ctx, errors.New("local conversion failed"), false))
	newTracker.OnOutboundRawError(ctx, healthGateServerError())
	require.Error(t, newTracker.finalize(ctx, healthGateServerError(), false))
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), candidate.Channel)
	require.Equal(t, biz.HealthGateStateOpen, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: candidate.Channel.ID, ActualModel: "model"}, healthGateResolver(cfg)).State)
}

func TestHealthGateOrchestrator_StreamInternalTimeoutIsFailure(t *testing.T) {
	candidate := healthGateSelectorCandidate(1, 0, "model")
	policy := biz.HealthGatePolicy{FailureThreshold: 1, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600, ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300}
	gate := biz.NewHealthGate(nil)
	state := &PersistenceState{RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated}, CurrentCandidate: candidate}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy)
	_, err := tracker.OnOutboundRawRequest(context.Background(), &httpclient.Request{})
	require.NoError(t, err)
	timeoutCtx, cancel := context.WithCancelCause(context.Background())
	wrapper, err := tracker.OnOutboundLlmStream(timeoutCtx, streams.SliceStream([]*llm.Response{}))
	require.NoError(t, err)
	require.NoError(t, tracker.finalize(context.Background(), nil, true))
	cancel(pipeline.ErrStreamFirstEventTimeout)
	require.NoError(t, wrapper.Close())
	cfg := biz.ResolveHealthGateConfig(policy, candidate.Channel)
	require.Equal(t, biz.HealthGateStateOpen, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: 1, ActualModel: "model"}, healthGateResolver(cfg)).State)
}
