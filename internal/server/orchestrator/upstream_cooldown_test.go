package orchestrator

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestChannelCooldownReleasesBudgetWithoutUpstreamEffects(t *testing.T) {
	ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
	ch.Channel = db.Channel.UpdateOneID(ch.ID).SetPolicies(objects.ChannelPolicies{
		APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{{StatusCodes: []int{429}, Times: 1, Action: objects.APIKeyAutoDisableActionPermanentDelete}},
	}).SaveX(ctx)
	usage.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{ch})
	executor := &mockExecutor{}
	manager := NewChannelLimiterManager()
	until := time.Now().Add(30 * time.Second)
	manager.requestTracker.SetCooldown(ch.ID, until)
	processor := &ChatCompletionOrchestrator{
		channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch}, "gpt-4")},
		Inbound:         openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService,
		PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage,
		PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(),
		channelLimiterManager: manager, rateLimitTracker: manager.requestTracker,
	}
	_, err := processor.Process(ctx, buildTestRequest("gpt-4", "hello", false))
	require.True(t, isChannelCooldownError(err))
	require.False(t, executor.requestCalled)
	require.Zero(t, manager.requestTracker.GetRequestCount(ch.ID))
	inFlight, _ := manager.Usage(ch.ID)
	require.Zero(t, inFlight)
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.Equal(t, budgetcharge.StatusReleased, charge.Status)
	require.Nil(t, charge.Actual)
	require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
	require.Zero(t, db.UsageLog.Query().CountX(ctx))
	updated := db.Channel.GetX(ctx, ch.ID)
	require.Equal(t, ch.Status, updated.Status)
	require.Equal(t, ch.Credentials, updated.Credentials)
	require.Empty(t, updated.DisabledAPIKeys, "本地冷却不能触发上游凭据失败规则")
	execution := db.RequestExecution.Query().OnlyX(ctx)
	require.Equal(t, "gateway", execution.Failure.Source)
	require.Equal(t, channelCooldownCode, execution.Failure.Code)
	require.Nil(t, execution.ResponseStatusCode)
}

func TestChannelCooldownDoesNotRecordProviderFailures(t *testing.T) {
	channel := channelWithRPM(9, "cooldown", 0)
	outbound := newTestOutbound(channel)
	outbound.state.OriginalModel = "gpt-4"
	outbound.state.RoutingPolicy.LoadBalancerStrategy = biz.LoadBalancerStrategyCircuitBreaker
	outbound.state.Perf = &biz.PerformanceRecord{}
	err := newChannelCooldownError(time.Now().Add(time.Second))
	cb := biz.NewModelCircuitBreaker()
	withModelCircuitBreaker(outbound, cb).OnOutboundRawError(t.Context(), err)
	require.Zero(t, cb.GetModelCircuitBreakerStats(t.Context(), channel.ID, "gpt-4").ConsecutiveFailures)
	withPerformanceRecording(outbound).OnOutboundRawError(t.Context(), err)
	require.False(t, outbound.state.Perf.RequestCompleted)
	require.Zero(t, outbound.state.Perf.ResponseStatusCode)
}

func TestChannelCooldownFallsBackToAvailableCandidate(t *testing.T) {
	ctx, db := setupTest(t)
	first := createTestChannel(t, ctx, db)
	backup := db.Channel.Create().SetType(first.Type).SetName("cooldown-backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture-backup"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	executor := &mockExecutor{response: healthGateResponse()}
	ctx, processor, _ := healthGateOrchestratorFixture(t, ctx, db, executor, first, backup)
	policy := processor.SystemService.RetryPolicyOrDefault(ctx)
	policy.LoadBalancerStrategy = biz.LoadBalancerStrategyAdaptive
	require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
	processor.rateLimitTracker = processor.channelLimiterManager.requestTracker
	processor.rateLimitTracker.SetCooldown(first.ID, time.Now().Add(time.Minute))
	result, err := processor.Process(ctx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.NotNil(t, result.ChatCompletion)
	require.EqualValues(t, 1, executor.requestCalls.Load())
	require.Contains(t, executor.lastRequest.URL, "backup.example")
	require.Zero(t, processor.rateLimitTracker.GetRequestCount(first.ID))
	require.EqualValues(t, 1, processor.rateLimitTracker.GetRequestCount(backup.ID))
	executions := db.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
	require.Len(t, executions, 2)
	require.Equal(t, "next_channel", executions[0].RetryDecision)
	require.Equal(t, channelCooldownCode, executions[0].Failure.Code)
}

func TestResponsesOriginCooldownSwitchesSource(t *testing.T) {
	executor := &mockExecutor{response: &httpclient.Response{
		StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"id":"resp_cooldown_origin","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_origin","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_cooldown-origin"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
	}}
	ctx, processor, db, source := responsesOriginGateway(t, executor)
	processor.rateLimitTracker = processor.channelLimiterManager.requestTracker
	policy := processor.SystemService.RetryPolicyOrDefault(ctx)
	policy.LoadBalancerStrategy = biz.LoadBalancerStrategyAdaptive
	policy.MaxChannelRetries = 1
	policy.MaxSingleChannelRetries = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
	_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
	require.NoError(t, err)
	backup := db.Channel.Create().SetType(source.Channel.Type).SetName("origin-cooldown-backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture-backup"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	backupOutbound, err := responses.NewOutboundTransformer(backup.BaseURL, backup.Credentials.APIKey)
	require.NoError(t, err)

	executor.err = &httpclient.Error{StatusCode: http.StatusTooManyRequests, Headers: http.Header{"Retry-After": {"30"}}, Body: []byte(`{"error":{"type":"rate_limit_error","message":"please retry later"}}`)}
	body := []byte(`{"model":"gpt-4","input":[{"type":"reasoning","summary":[],"encrypted_content":"gAAAA_cooldown-origin"},{"role":"user","content":"continue"}]}`)
	_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body})
	require.Error(t, err)
	require.True(t, httpclient.HasRetryAfterHeader(err), "协议转换后仍保留上游的恢复时间")
	require.EqualValues(t, 2, executor.requestCalls.Load())
	until, cooling := processor.rateLimitTracker.GetCooldownUntil(source.Channel.ID)
	require.True(t, cooling)
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{
		source,
		{
			Channel: &biz.Channel{Channel: backup, Outbound: backupOutbound}, APIFormat: string(llm.APIFormatOpenAIResponse),
			Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
		},
	}}
	executor.err = nil
	_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body})
	require.NoError(t, err)
	require.EqualValues(t, 3, executor.requestCalls.Load())
	require.Contains(t, executor.lastRequest.URL, "backup.example")
	require.Contains(t, string(executor.lastRequest.Body), "gAAAA_cooldown-origin")
	currentUntil, cooling := processor.rateLimitTracker.GetCooldownUntil(source.Channel.ID)
	require.True(t, cooling)
	require.Equal(t, until, currentUntil)
}
