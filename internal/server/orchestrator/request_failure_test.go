package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/stretchr/testify/require"
)

const imageLimitMessage = "image relay inline images exceed the configured 32 MiB request limit"

type nonStreamDeadlineExecutor struct{ channelDeadlineExecutor }

func (e *nonStreamDeadlineExecutor) Do(ctx context.Context, _ *httpclient.Request) (*httpclient.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNonStreamDeadlineClassificationMatchesFirstPersistedFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		outer time.Duration
		want  string
	}{
		{"channel limit", 5 * time.Second, "upstream_response_timeout"},
		{"overall limit", 300 * time.Millisecond, "request_deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ch := createTestChannel(t, ctx, client)
			ch.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: new(1)}
			processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, &nonStreamDeadlineExecutor{}, ch)
			processCtx, cancel := context.WithTimeout(processCtx, tc.outer)
			defer cancel()
			_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
			require.Error(t, err)
			require.Equal(t, tc.want, ExtractErrorInfo(err).Failure.Code)
			executions := client.RequestExecution.Query().AllX(ctx)
			require.Len(t, executions, 1)
			require.Equal(t, tc.want, executions[0].Failure.Code)
			require.Nil(t, executions[0].ResponseStatusCode)
		})
	}
}

func TestRequestSizeLimitSkipsSameChannel(t *testing.T) {
	for _, failure := range []error{
		&httpclient.Error{StatusCode: 413},
		&httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"message":"` + imageLimitMessage + `","code":"basispoints_request_invalid"}}`)},
		pipeline.WrapUpstreamError(&llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Message: imageLimitMessage}}),
	} {
		for _, bound := range []bool{false, true} {
			ch := &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{RetryableStatusCodes: []int{400, 413}}}}
			candidate := &ChannelModelsCandidate{Channel: ch, continuationBound: bound, Models: []biz.ChannelModelEntry{{ActualModel: "one"}, {ActualModel: "two"}}}
			p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: candidate}}
			require.False(t, p.CanRetry(failure), "容量拒绝不能靠更换模型或重复请求解决")
		}
	}
	require.False(t, isRequestSizeLimitError(&llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Message: "invalid image"}}))
}

func TestRequestFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"first event", pipeline.ErrStreamFirstEventTimeout, "upstream_first_event_timeout", 504},
		{"response", pipeline.ErrNonStreamResponseTimeout, "upstream_response_timeout", 504},
		{"upstream 524", pipeline.WrapUpstreamError(&llm.ResponseError{StatusCode: 524}), "upstream_timeout", 524},
		{"size", &llm.ResponseError{StatusCode: 413}, "upstream_request_too_large", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyRequestFailure(context.Background(), tc.err)
			require.ErrorIs(t, err, tc.err)
			info := ExtractErrorInfo(err)
			require.Equal(t, tc.code, info.Failure.Code)
			wire := responses.NewInboundTransformer().TransformError(context.Background(), err)
			require.Equal(t, tc.status, wire.StatusCode)
			require.Contains(t, string(wire.Body), tc.code)
			if pipeline.IsUpstreamError(tc.err) {
				require.True(t, pipeline.IsUpstreamError(err))
			}
		})
	}
	original := &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "basispoints_request_invalid", Type: "invalid_request_error", Param: "input", Message: imageLimitMessage}}
	require.Same(t, original, classifyRequestFailure(context.Background(), original))
	noCode := &llm.ResponseError{StatusCode: 413, Detail: llm.ErrorDetail{Type: "invalid_request_error", Param: "input", Message: "too big"}}
	classified := classifyRequestFailure(context.Background(), noCode)
	require.Empty(t, noCode.Detail.Code, "不能修改共享的原错误")
	wire := responses.NewInboundTransformer().TransformError(context.Background(), classified)
	require.JSONEq(t, `{"error":{"type":"invalid_request_error","param":"input","message":"too big","code":"upstream_request_too_large"}}`, string(wire.Body))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	deadline := classifyRequestFailure(ctx, pipeline.WrapUpstreamError(context.DeadlineExceeded))
	require.ErrorIs(t, deadline, context.DeadlineExceeded)
	require.Equal(t, "request_deadline_exceeded", ExtractErrorInfo(deadline).Failure.Code)
	require.Equal(t, 504, responses.NewInboundTransformer().TransformError(ctx, deadline).StatusCode)
	require.Same(t, original, classifyRequestFailure(ctx, original), "截止时间到达不应掩盖无关的上游拒绝")
	require.Equal(t, context.DeadlineExceeded, classifyRequestFailure(context.Background(), context.DeadlineExceeded))
	canceled, stop := context.WithCancel(context.Background())
	stop()
	require.Equal(t, pipeline.ErrNonStreamResponseTimeout, classifyRequestFailure(canceled, pipeline.ErrNonStreamResponseTimeout))
	require.Equal(t, context.Canceled, classifyRequestFailure(ctx, context.Canceled))
	ordinary := errors.New("ordinary")
	require.Same(t, ordinary, classifyRequestFailure(context.Background(), ordinary))
	require.NoError(t, classifyRequestFailure(ctx, nil))
}

func TestRequestSizeLimitUsesExistingChannelFallback(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	first.Settings = &objects.ChannelSettings{RetryableStatusCodes: []int{400}}
	second := client.Channel.Create().SetType("openai").SetName("backup").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	executor := &sequenceExecutor{steps: []executorStep{
		{err: &httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"message":"` + imageLimitMessage + `","code":"basispoints_request_invalid"}}`)}},
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
			require.Equal(t, "basispoints_request_invalid", execution.Failure.Code)
		}
	}
}

func TestRequestDeadlineClassifiedByProcess(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, &channelDeadlineExecutor{}, ch)
	processCtx, cancel := context.WithTimeout(processCtx, 300*time.Millisecond)
	defer cancel()
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", true))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, "request_deadline_exceeded", ExtractErrorInfo(err).Failure.Code)
	executions := client.RequestExecution.Query().AllX(ctx)
	require.Len(t, executions, 1)
	require.Equal(t, "request_deadline_exceeded", executions[0].Failure.Code)
	require.Nil(t, executions[0].ResponseStatusCode, "本地超时不能伪造上游 HTTP 状态")
	require.Equal(t, 504, responses.NewInboundTransformer().TransformError(ctx, err).StatusCode)
}

func TestUpstreamTimeoutSkipsRepeatedOrigin(t *testing.T) {
	for _, status := range []int{http.StatusGatewayTimeout, 524} {
		for _, bound := range []bool{false, true} {
			ch := &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{RetryableStatusCodes: []int{status, 503}}}}
			candidate := &ChannelModelsCandidate{Channel: ch, continuationBound: bound, Models: []biz.ChannelModelEntry{{ActualModel: "one"}, {ActualModel: "two"}}}
			p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: candidate}}
			for _, err := range []error{&httpclient.Error{StatusCode: status}, pipeline.WrapUpstreamError(&llm.ResponseError{StatusCode: status})} {
				require.False(t, p.CanRetry(err), "明确的上游超时应交给安全备用渠道，不再重复同一来源")
			}
			require.True(t, p.CanRetry(&httpclient.Error{StatusCode: 503}), "普通暂时不可用仍沿用原重试策略")
		}
	}
}

func TestUpstreamTimeoutUsesExistingChannelFallback(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	second := client.Channel.Create().SetType("openai").SetName("timeout-backup").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	executor := &sequenceExecutor{steps: []executorStep{
		{err: &httpclient.Error{StatusCode: 524, Body: []byte("<!DOCTYPE html><html><title>A timeout occurred</title></html>")}},
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
}

func TestUpstreamTimeoutReplacesHTMLMessage(t *testing.T) {
	for _, code := range []string{"", "provider_timeout"} {
		original := &llm.ResponseError{StatusCode: 524, Detail: llm.ErrorDetail{
			Code: code, Type: "api_error", Message: "<!DOCTYPE html><html><title>A timeout occurred</title></html>",
		}}
		err := classifyRequestFailure(context.Background(), pipeline.WrapUpstreamError(original))
		require.ErrorIs(t, err, original)
		wire := responses.NewInboundTransformer().TransformError(context.Background(), err)
		require.Equal(t, 524, wire.StatusCode)
		require.NotContains(t, string(wire.Body), "<!DOCTYPE")
		require.Contains(t, string(wire.Body), "timed out")
		if code != "" {
			require.Contains(t, string(wire.Body), code)
		}
		require.Contains(t, original.Detail.Message, "<!DOCTYPE", "原上游错误仍可供诊断")
	}
}
