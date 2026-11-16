package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesServiceTierOverrideModesAndScope(t *testing.T) {
	for _, mode := range []objects.ResponsesServiceTierOverride{objects.ResponsesServiceTierInherit, objects.ResponsesServiceTierStandard, objects.ResponsesServiceTierFast, objects.ResponsesServiceTierUltrafast} {
		for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact, llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
			for _, body := range []string{`{"model":"test","input":[]}`, `{"model":"test","input":[],"service_tier":"flex"}`} {
				t.Run(fmt.Sprintf("%s/%s/%s", mode, format, body), func(t *testing.T) {
					outbound := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: mode}}}}}}
					request := &httpclient.Request{APIFormat: string(format), Body: []byte(body)}
					got, err := applyResponsesServiceTierOverride(outbound).OnOutboundRawRequest(t.Context(), request)
					require.NoError(t, err)
					if mode == objects.ResponsesServiceTierInherit || (format != llm.APIFormatOpenAIResponse && format != llm.APIFormatOpenAIResponseWebSocket) {
						require.Same(t, request, got)
						require.JSONEq(t, body, string(got.Body))
					} else {
						require.Equal(t, string(mode), objects.ServiceTierValue(got.Body))
						require.Equal(t, "test", gjson.GetBytes(got.Body, "model").String())
					}
					require.Equal(t, body, string(request.Body), "the input request must stay reusable for another attempt")
				})
			}
		}
	}
}

func TestResponsesServiceTierOverrideWebSocketFinalization(t *testing.T) {
	ctx := shared.WithResponsesWebSocket(t.Context())
	provider, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: "https://fixture.invalid/v1", APIKeyProvider: auth.NewStaticKeyProvider("fixture"), Transport: responses.TransportWebSocket})
	require.NoError(t, err)
	raw := []byte(`{"model":"test","input":"hello","service_tier":"priority"}`)
	req, err := responses.NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Body: raw})
	require.NoError(t, err)
	wire, err := provider.TransformRequest(ctx, req)
	require.NoError(t, err)
	require.Equal(t, string(llm.APIFormatOpenAIResponseWebSocket), wire.APIFormat)
	outbound := &PersistentOutboundTransformer{wrapped: provider, state: &PersistenceState{RawRequest: &httpclient.Request{Body: raw}, CurrentCandidate: &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard}}}}}}
	wire, err = applyResponsesServiceTierOverride(outbound).OnOutboundRawRequest(t.Context(), wire)
	require.NoError(t, err)
	wire, err = finalizeTransportRequest(outbound).OnOutboundRawRequest(t.Context(), wire)
	require.NoError(t, err)
	require.Equal(t, "default", objects.ServiceTierValue(wire.Body))
	evidence := requestServiceTier(outbound.state, wire, llm.APIFormatOpenAIResponseWebSocket)
	require.Equal(t, "priority", evidence.Client)
	require.Equal(t, "default", evidence.Outbound)
}

type serviceTierCaptureExecutor struct {
	mockExecutor
	requests []*httpclient.Request
}

func (e *serviceTierCaptureExecutor) Do(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	copy := *req
	copy.Body = slices.Clone(req.Body)
	e.requests = append(e.requests, &copy)
	if !strings.Contains(req.URL, "backup.example") {
		return nil, &httpclient.Error{StatusCode: 502, Body: []byte(`{"error":{"message":"fixture unavailable"}}`)}
	}
	return &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_tier","object":"response","status":"completed","model":"gpt-4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)}, nil
}

func TestResponsesServiceTierOverrideProductionPipeline(t *testing.T) {
	for _, passThrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passThrough), func(t *testing.T) {
			executor := &serviceTierCaptureExecutor{}
			ctx, processor, client, preferred := responsesOriginGateway(t, executor)
			preferred.Channel.Settings = &objects.ChannelSettings{
				PassThroughBody:              lo.ToPtr(passThrough),
				ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard,
				BodyOverrideOperations:       []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier", Value: "ultrafast"}},
			}
			backupRow := client.Channel.Create().SetName("tier-backup").SetType("openai_responses").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
			backupOut, err := responses.NewOutboundTransformer(backupRow.BaseURL, "fixture")
			require.NoError(t, err)
			backup := *preferred
			backup.Channel = &biz.Channel{Channel: backupRow, Outbound: backupOut}
			backup.Channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(passThrough)}
			processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{preferred, &backup}}
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.Enabled, policy.MaxChannelRetries, policy.MaxSingleChannelRetries, policy.RetryDelayMs = true, 1, 0, 0
			policy.LoadBalancerStrategy, policy.TraceStickyMode = biz.LoadBalancerStrategyAdaptive, biz.TraceStickyDisabled
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			require.NoError(t, processor.SystemService.SetStoragePolicy(ctx, &biz.StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
			body := []byte(`{"model":"gpt-4","input":"hello","service_tier":"priority"}`)
			_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body, JSONBody: body})
			require.NoError(t, err)
			require.Len(t, executor.requests, 2)
			require.Equal(t, "default", objects.ServiceTierValue(executor.requests[0].Body))
			require.Equal(t, "priority", objects.ServiceTierValue(executor.requests[1].Body))
			require.Equal(t, "priority", objects.ServiceTierValue(body))
			executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
			require.Len(t, executions, 2)
			for i, execution := range executions {
				stored, err := processor.RequestService.LoadRequestExecutionRequestBody(ctx, execution)
				require.NoError(t, err)
				require.JSONEq(t, string(executor.requests[i].Body), string(stored))
				require.Equal(t, "priority", execution.CostSnapshot.ServiceTier.Client)
				require.Equal(t, objects.ServiceTierValue(stored), execution.CostSnapshot.ServiceTier.Outbound)
				// The override does not enable tier billing or change its basis.
				require.False(t, execution.CostSnapshot.ServiceTier.Settings.Enabled)
			}
		})
	}
}
