package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	entchannel "github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/stretchr/testify/require"
)

type addressStreamErrorExecutor struct{ sequenceExecutor }

func (e *addressStreamErrorExecutor) DoStream(ctx context.Context, req *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	_, err := e.Do(ctx, req)
	return nil, err
}

func TestAddressFailoverThroughStreamPipeline(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	ch.Settings = &objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{
		{ID: "main", URL: ch.BaseURL, Enabled: true},
		{ID: "backup", URL: "https://backup.example/v1", Priority: 1, Enabled: true},
	}}
	executor := &addressStreamErrorExecutor{sequenceExecutor: sequenceExecutor{steps: []executorStep{
		{err: &httpclient.Error{StatusCode: 524, Body: []byte(`{"error":{"message":"upstream timed out"}}`)}},
		{err: &httpclient.Error{StatusCode: 401, Body: []byte(`{"error":{"message":"invalid fixture credential"}}`)}},
	}}}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.MaxSingleChannelRetries = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", true))
	require.Equal(t, 401, ExtractStatusCodeFromError(err))
	require.Len(t, executor.requests, 2)
	require.Contains(t, executor.requests[1].URL, "backup.example")
	require.Equal(t, executor.requests[0].Body, executor.requests[1].Body)
	require.Equal(t, executor.requests[0].Auth, executor.requests[1].Auth)
	executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
	require.Len(t, executions, 2)
	require.Equal(t, "next_address", executions[0].RetryDecision)
	require.Equal(t, "upstream_timeout", executions[0].Failure.Code)
	require.Equal(t, 524, *executions[0].ResponseStatusCode)
}

func TestAddressFailoverRecognizesNormalizedErrors(t *testing.T) {
	for _, status := range []int{502, 503, 504, 524, 400, 401, 403, 429} {
		for _, normalized := range []bool{false, true} {
			for _, bound := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/normalized=%t/bound=%t", status, normalized, bound), func(t *testing.T) {
					var failure error = &httpclient.Error{StatusCode: status}
					if normalized {
						failure = pipeline.WrapUpstreamError(&llm.ResponseError{StatusCode: status})
					}
					ch := &biz.Channel{Channel: &ent.Channel{BaseURL: "https://main.example/v1", Settings: &objects.ChannelSettings{
						AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{
							{ID: "a", URL: "https://main.example/v1", Enabled: true},
							{ID: "b", URL: "https://backup.example/v1", Priority: 1, Enabled: true},
						},
					}}}
					p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: ch, continuationBound: bound}}}
					require.Equal(t, status >= 500, p.CanRetry(fmt.Errorf("wrapped: %w", failure)))
					if status >= 500 {
						require.True(t, p.retryAddress)
						require.NoError(t, p.PrepareForRetry(context.Background()))
						require.False(t, p.CanRetry(failure), "备用地址耗尽后不能重复原地址")
					}
				})
			}
		}
	}
}

func TestAddressFailoverKeepsModelAndRequest(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{BaseURL: "https://main.example/v1", Settings: &objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{{ID: "b", URL: "https://backup.example/api/v1", Priority: 20, Enabled: true}, {ID: "off", URL: "https://off.example", Priority: 0, Enabled: false}, {ID: "a", URL: "https://main.example/v1", Priority: 10, Enabled: true}}}}}
	p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: ch, TraceSticky: true}}}
	raw := &httpclient.Request{URL: "https://main.example/v1/responses?test=1", Method: http.MethodPost, Body: []byte(`{"model":"same"}`), Headers: http.Header{"Authorization": []string{"Bearer fixture"}}}
	middleware := applyUpstreamAddress(p)
	first, err := middleware.OnOutboundRawRequest(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, raw.URL, first.URL)
	require.True(t, p.CanRetry(&httpclient.Error{StatusCode: 503}))
	require.NoError(t, p.PrepareForRetry(context.Background()))
	second, err := middleware.OnOutboundRawRequest(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "https://backup.example/api/v1/responses?test=1", second.URL)
	require.Equal(t, raw.Headers, second.Headers)
	require.Equal(t, raw.Body, second.Body)
	require.Equal(t, 0, p.state.CurrentModelIndex)
	require.False(t, p.CanRetry(&httpclient.Error{StatusCode: 503}), "地址已耗尽，交回渠道切换")
	for _, code := range []int{401, 403, 429} {
		require.False(t, p.CanRetry(&httpclient.Error{StatusCode: code}))
	}
	require.False(t, isUpstreamAddressFailure(context.Canceled))
	require.False(t, isUpstreamAddressFailure(errors.New("quota exhausted")))
}

type addressRetryKeys struct{ calls int }

func (p *addressRetryKeys) Get(context.Context) string {
	p.calls++
	if p.calls == 1 {
		return "fixture-first-account"
	}
	return "fixture-another-account"
}

func TestAddressFailoverKeepsSelectedKeyThroughTransform(t *testing.T) {
	keys := &addressRetryKeys{}
	outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType: openai.PlatformOpenAI, BaseURL: "https://main.example/v1", APIKeyProvider: biz.NewChannelAPIKeyContextProvider(keys),
	})
	require.NoError(t, err)
	ch := &biz.Channel{Channel: &ent.Channel{Type: entchannel.TypeOpenai, BaseURL: "https://main.example/v1", Settings: &objects.ChannelSettings{
		AddressFailover: true, DisableKeySessionReuse: true,
		UpstreamAddresses: []objects.ChannelUpstreamAddress{{ID: "a", URL: "https://main.example/v1", Enabled: true}, {ID: "b", URL: "https://backup.example/v1", Priority: 1, Enabled: true}},
	}}, Outbound: outbound}
	candidate := &ChannelModelsCandidate{Channel: ch, APIFormat: llm.APIFormatOpenAIChatCompletion.String(), Models: []biz.ChannelModelEntry{{ActualModel: "same-model"}}}
	p := &PersistentOutboundTransformer{state: &PersistenceState{ChannelModelsCandidates: []*ChannelModelsCandidate{candidate}}}
	ctx := contexts.EnsureContainer(context.Background())
	request := &llm.Request{Model: "alias", Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: new("hello")}}}}
	first, err := p.TransformRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "fixture-first-account", first.Auth.APIKey)
	for _, code := range []int{401, 403, 429} {
		require.False(t, p.CanRetryAfterTimeout(&httpclient.Error{StatusCode: code}))
	}
	require.True(t, p.CanRetryAfterTimeout(pipeline.ErrNonStreamResponseTimeout))
	require.NoError(t, p.PrepareForRetry(ctx))
	second, err := p.TransformRequest(ctx, request)
	require.NoError(t, err)
	second, err = applyUpstreamAddress(p).OnOutboundRawRequest(ctx, second)
	require.NoError(t, err)
	require.Equal(t, "https://backup.example/v1/chat/completions", second.URL)
	require.Equal(t, first.Auth.APIKey, second.Auth.APIKey)
	require.Equal(t, first.Body, second.Body)
	require.Equal(t, 1, keys.calls, "切换地址不重新选择账号 Key")
	require.False(t, p.CanRetryAfterTimeout(pipeline.ErrStreamFirstEventTimeout))
}
