package orchestrator

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestEncryptedContentNeverRetriesSameChannel(t *testing.T) {
	for _, bound := range []bool{false, true} {
		for _, status := range []int{0, 400, 502} {
			candidate := stickyTestCandidate(1, 0)
			candidate.continuationBound = bound
			candidate.Models = []biz.ChannelModelEntry{{ActualModel: "one"}, {ActualModel: "two"}}
			candidate.Channel.Settings = &objects.ChannelSettings{RetryableStatusCodes: []int{0, 400, 502},
				RetryableErrorPatterns: []objects.RetryableErrorPattern{{Pattern: "encrypted"}}}
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: candidate,
				ChannelModelsCandidates: []*ChannelModelsCandidate{candidate, stickyTestCandidate(2, 1)}}}
			err := pipeline.WrapUpstreamError(&llm.ResponseError{StatusCode: status,
				Detail: llm.ErrorDetail{Code: "invalid_encrypted_content", Type: "invalid_request_error", Message: "Invalid encrypted content."}})
			require.False(t, outbound.CanRetry(err), "bound=%v status=%d", bound, status)
			require.True(t, outbound.HasMoreChannels(), "unknown ciphertext may still need a compatible eligible origin")
			require.Equal(t, biz.HealthGateOutcomeNeutral, classifyHealthGateOutcome(t.Context(), err))
		}
	}
	for _, message := range []string{
		"Invalid encrypted content.",
		"The encrypted content for item amsg_fixture could not be verified. Reason: encrypted content hydration failed: Encrypted content could not be decrypted or parsed.",
	} {
		require.True(t, invalidResponsesContinuation(&llm.ResponseError{Detail: llm.ErrorDetail{Message: message}}))
	}
	require.False(t, invalidResponsesContinuation(errors.New("connection refused")))
	require.False(t, invalidResponsesContinuation(&llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{Message: "temporary provider error"}}))
}

func TestEncryptedContentFailedStreamUsesBadRequest(t *testing.T) {
	event := &httpclient.StreamEvent{Type: "response.failed", Data: []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"Invalid encrypted content.","param":"input"}}}`)}
	err := streamTerminalError(event)
	require.Equal(t, http.StatusBadRequest, ExtractStatusCodeFromError(err))
	wire := responses.NewInboundTransformer().TransformError(t.Context(), err)
	require.Equal(t, http.StatusBadRequest, wire.StatusCode)
	require.Equal(t, "invalid_encrypted_content", gjson.GetBytes(wire.Body, "error.code").String())
	require.Equal(t, "input", gjson.GetBytes(wire.Body, "error.param").String())
	require.Equal(t, biz.HealthGateOutcomeNeutral, classifyHealthGateOutcome(t.Context(), err))
	ordinary := responseStatusError("failed", llm.ErrorDetail{Code: "server_error", Message: "temporary provider error"})
	require.Equal(t, http.StatusBadGateway, ExtractStatusCodeFromError(ordinary))
	require.Equal(t, biz.HealthGateOutcomeFailure, classifyHealthGateOutcome(t.Context(), ordinary))
}

func TestPassThroughRepairsOnlyProvenPlaintextAgentHistory(t *testing.T) {
	body := []byte(`{"model":"alias","input":[
		{"type":"function_call","namespace":"collaboration","name":"send_message","call_id":"call_test","arguments":"{\"message\":\"Known plaintext\",\"target\":\"worker\"}","encrypted_function_args":[]},
		{"type":"agent_message","id":"amsg_test","author":"/root","recipient":"/root/worker","content":[{"type":"encrypted_content","encrypted_content":"Known plaintext"},{"type":"encrypted_content","encrypted_content":"gAAAA_real_ciphertext"}]}
	]}`)
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseCompact} {
		actual, err := mergePassThroughRequestBody(body, format, "actual-model")
		require.NoError(t, err)
		require.Equal(t, "actual-model", gjson.GetBytes(actual, "model").String())
		require.Equal(t, "Known plaintext", gjson.GetBytes(actual, "input.1.content.0.text").String())
		require.Equal(t, "gAAAA_real_ciphertext", gjson.GetBytes(actual, "input.1.content.1.encrypted_content").String())
		require.Equal(t, "Known plaintext", gjson.GetBytes(body, "input.1.content.0.encrypted_content").String())
	}
	other, err := mergePassThroughRequestBody(body, llm.APIFormatAnthropicMessage, "actual-model")
	require.NoError(t, err)
	require.Equal(t, "Known plaintext", gjson.GetBytes(other, "input.1.content.0.encrypted_content").String())
}

func TestEncryptedContentChannelFallbackWithoutSameChannelRetry(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	first.Settings = &objects.ChannelSettings{RetryableStatusCodes: []int{400}}
	second := client.Channel.Create().SetType("openai").SetName("backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	executor := &sequenceExecutor{steps: []executorStep{
		{err: &httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"message":"Invalid encrypted content.","type":"invalid_request_error","code":"invalid_encrypted_content"}}`)}},
		{resp: healthGateResponse()},
	}}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.MaxSingleChannelRetries = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.Len(t, executor.requests, 2)
	require.Contains(t, executor.requests[1].URL, "backup.example")
	executions := client.RequestExecution.Query().AllX(ctx)
	require.Len(t, executions, 2)
	for _, execution := range executions {
		if execution.ChannelID == first.ID {
			require.Equal(t, "next_channel", execution.RetryDecision)
			require.Equal(t, "invalid_encrypted_content", execution.Failure.Code)
		}
	}
}

func TestEncryptedContentSingleChannelStopsAfterOneAttempt(t *testing.T) {
	ctx, client := setupTest(t)
	channel := createTestChannel(t, ctx, client)
	channel.Settings = &objects.ChannelSettings{RetryableStatusCodes: []int{400}}
	executor := &sequenceExecutor{steps: []executorStep{
		{err: &httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"message":"Invalid encrypted content.","type":"invalid_request_error","code":"invalid_encrypted_content"}}`)}},
		{resp: healthGateResponse()},
	}}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, channel)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.MaxSingleChannelRetries = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.Error(t, err)
	require.Equal(t, "invalid_encrypted_content", ExtractErrorInfo(err).Failure.Code)
	require.Len(t, executor.requests, 1, "do not consume a synthetic successful second attempt")
	executions := client.RequestExecution.Query().AllX(ctx)
	require.Len(t, executions, 1)
	require.Equal(t, "candidates_exhausted", executions[0].RetryDecision)
}

func TestEncryptedContentCommittedStreamRemainsNeutral(t *testing.T) {
	for _, deliveredText := range []bool{false, true} {
		name := "before_text"
		if deliveredText {
			name = "after_text"
		}
		t.Run(name, func(t *testing.T) {
			candidate := healthGateSelectorCandidate(1, 0, "model")
			policy := biz.HealthGatePolicy{FailureThreshold: 1, OpenDurationSeconds: 300,
				MaxOpenDurationSeconds: 600, ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300}
			gate := biz.NewHealthGate(nil)
			state := &PersistenceState{RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated}, CurrentCandidate: candidate}
			tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy)
			_, err := tracker.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
			require.NoError(t, err)
			text := "already delivered"
			var chunks []*llm.Response
			if deliveredText {
				chunks = []*llm.Response{{Choices: []llm.Choice{{Delta: &llm.Message{Content: llm.MessageContent{Content: &text}}}}}}
			}
			wrapper, err := tracker.OnOutboundLlmStream(t.Context(), streams.SliceStream(chunks))
			require.NoError(t, err)
			require.NoError(t, tracker.finalize(t.Context(), nil, true))
			for wrapper.Next() {
				wrapper.Current()
			}
			// Structured terminal errors can be retained in state while the
			// converted stream ends cleanly, without returning Err().
			state.observeStreamTerminal(&httpclient.StreamEvent{Type: "response.failed", Data: []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"Invalid encrypted content."}}}`)})
			require.NoError(t, wrapper.Err())
			require.NoError(t, wrapper.Close())
			require.Equal(t, biz.HealthGateOutcomeNeutral, tracker.lastOutcome)
			require.Equal(t, "Invalid encrypted content.", tracker.lastInfo.Message)
			cfg := biz.ResolveHealthGateConfig(policy, candidate.Channel)
			require.Equal(t, biz.HealthGateStateHealthy, healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: 1, ActualModel: "model"}, healthGateResolver(cfg)).State)
		})
	}
}

func TestEncryptedContentDoesNotTripCircuitBreaker(t *testing.T) {
	channel := stickyTestCandidate(42, 0).Channel
	outbound := newTestOutbound(channel)
	outbound.state.OriginalModel = "gpt-4"
	outbound.state.RoutingPolicy.LoadBalancerStrategy = biz.LoadBalancerStrategyCircuitBreaker
	cb := biz.NewModelCircuitBreaker()
	tracker := withModelCircuitBreaker(outbound, cb).(*modelCircuitBreakerTracker)
	for _, err := range []error{
		&llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "invalid_encrypted_content"}},
		&llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{Code: "invalid_encrypted_content"}},
		&httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"code":"invalid_encrypted_content","message":"Invalid encrypted content."}}`)},
		&httpclient.Error{StatusCode: 502, Body: []byte(`{"error":{"code":"invalid_encrypted_content","message":"Invalid encrypted content."}}`)},
	} {
		tracker.probeActive, tracker.probeChannelID, tracker.probeModelID = true, channel.ID, "gpt-4"
		tracker.OnOutboundRawError(t.Context(), err)
		require.False(t, tracker.probeActive, "request errors must release the probe lease")
		require.Zero(t, cb.GetModelCircuitBreakerStats(t.Context(), channel.ID, "gpt-4").ConsecutiveFailures)
	}
	tracker.OnOutboundRawError(t.Context(), &httpclient.Error{StatusCode: 503})
	require.Equal(t, 1, cb.GetModelCircuitBreakerStats(t.Context(), channel.ID, "gpt-4").ConsecutiveFailures)
}
