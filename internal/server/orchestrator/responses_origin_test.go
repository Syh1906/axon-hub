package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// Exercise output recording, the real database resolver, and routing together.
// Mocking a preselected origin would miss the original multi-model regression.
func TestResponsesOriginEndToEnd(t *testing.T) {
	for _, sameModel := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_model=%t", sameModel), func(t *testing.T) {
			ctx, client := setupTest(t)
			first := createTestChannel(t, ctx, client)
			first = client.Channel.UpdateOneID(first.ID).SetStatus("enabled").SaveX(ctx)
			second := client.Channel.Create().SetType(first.Type).SetName("second-source").SetBaseURL("https://second.example/v1").
				SetCredentials(objects.ChannelCredentials{APIKey: "fixture-second"}).SetSupportedModels([]string{"gpt-luna", "gpt-4"}).SetDefaultTestModel("gpt-4").SetStatus("enabled").SaveX(ctx)
			executor := &mockExecutor{}
			ctx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
			projectID, _ := contexts.GetProjectID(ctx)
			thread := client.Thread.Create().SetProjectID(projectID).SetThreadID("origin-thread").SaveX(ctx)
			trace := client.Trace.Create().SetProjectID(projectID).SetThreadID(thread.ID).SetTraceID("origin-trace").SaveX(ctx)
			ctx = contexts.WithTrace(shared.WithResponsesAPI(ctx), trace)
			processor.Inbound = responses.NewInboundTransformer()
			processor.responsesSessions = newResponsesSessionStore(processor.RequestService.LoadCompletedResponsesSession)
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.TraceStickyMode = biz.TraceStickyDisabled
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			candidate := func(ch *ent.Channel, model string) *ChannelModelsCandidate {
				outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{
					BaseURL:        ch.BaseURL,
					APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider(ch.Credentials.APIKey)),
				})
				require.NoError(t, err)
				return &ChannelModelsCandidate{
					Channel:   &biz.Channel{Channel: ch, Outbound: outbound},
					APIFormat: string(llm.APIFormatOpenAIResponse), Models: []biz.ChannelModelEntry{{RequestModel: model, ActualModel: model}},
				}
			}
			firstCandidate := candidate(first, "gpt-4")
			otherModel := "gpt-luna"
			if sameModel {
				otherModel = "gpt-4"
			}
			secondCandidate := candidate(second, otherModel)
			call := func(id, model, input string, candidates ...*ChannelModelsCandidate) ChatCompletionResult {
				executor.response = &httpclient.Response{
					StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
					Body: []byte(fmt.Sprintf(`{"id":%q,"object":"response","status":"completed","model":%q,"output":[{"id":"rs_%s","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_cipher-%s"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id, model, id, id)),
				}
				processor.channelSelector = &staticChannelSelector{candidates: candidates}
				result, err := processor.Process(ctx, &httpclient.Request{
					Method: "POST", URL: "/v1/responses",
					Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(fmt.Sprintf(`{"model":%q,"input":%s}`, model, input)),
				})
				require.NoError(t, err)
				return result
			}
			call("resp_first", "gpt-4", `"first branch"`, firstCandidate)
			call("resp_auxiliary", otherModel, `"another branch"`, secondCandidate)
			result := call("resp_resume", "gpt-4", `[{"id":"rs_resp_first","type":"reasoning","encrypted_content":"gAAAA_cipher-resp_first","summary":[]},{"role":"user","content":"continue"}]`, secondCandidate, firstCandidate)
			require.Contains(t, executor.lastRequest.URL, "api.openai.com", "the latest auxiliary or sibling call must not steal the origin")
			require.Contains(t, string(executor.lastRequest.Body), "gAAAA_cipher-resp_first", "routing must preserve the actual encrypted history")
			require.EqualValues(t, 3, executor.requestCalls.Load())
			require.Equal(t, first.ID, client.Request.GetX(ctx, result.RequestID).ChannelID)
			require.NotZero(t, client.ResponsesOrigin.Query().CountX(ctx))
			// A disabled source is replaced without removing its history.
			client.Channel.UpdateOneID(first.ID).SetStatus("disabled").SaveX(ctx)
			result = call("resp_switched", "gpt-4", `[{"type":"reasoning","encrypted_content":"gAAAA_cipher-resp_first","summary":[]}]`, secondCandidate)
			require.Equal(t, second.ID, client.Request.GetX(ctx, result.RequestID).ChannelID)
			require.Contains(t, string(executor.lastRequest.Body), "gAAAA_cipher-resp_first")
			// Once A returns, mixed A/B history must prefer the latest actual source B.
			client.Channel.UpdateOneID(first.ID).SetStatus("enabled").SaveX(ctx)
			firstCandidate.Priority, secondCandidate.Priority = 0, 9
			result = call("resp_mixed", "gpt-4", `[{"type":"reasoning","encrypted_content":"gAAAA_cipher-resp_first","summary":[]},{"type":"reasoning","encrypted_content":"gAAAA_cipher-resp_switched","summary":[]}]`, firstCandidate, secondCandidate)
			require.Equal(t, second.ID, client.Request.GetX(ctx, result.RequestID).ChannelID)
			require.Contains(t, string(executor.lastRequest.Body), "gAAAA_cipher-resp_first")
			require.Contains(t, string(executor.lastRequest.Body), "gAAAA_cipher-resp_switched")
			require.EqualValues(t, 5, executor.requestCalls.Load())
		})
	}
}

func TestResponsesOriginStreamRecordsOnlyCompletedItems(t *testing.T) {
	var recorded [][]byte
	stream := &responsesOriginStream{Stream: streams.SliceStream([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.output_item.added","item":{"encrypted_content":"partial"}}`)},
		{Data: []byte(`{"type":"response.output_item.done","item":{"type":"reasoning","status":"completed","encrypted_content":"finished"}}`)},
		{Data: []byte(`{"type":"response.failed","response":{"id":"failed","status":"failed"}}`)},
	}), record: func(body []byte) error { recorded = append(recorded, body); return nil }}
	require.True(t, stream.Next())
	require.Empty(t, recorded)
	require.True(t, stream.Next())
	require.Len(t, recorded, 1, "origin must be retained before handing a finished item to the caller")
	require.Contains(t, string(recorded[0]), "finished")
	require.True(t, stream.Next())
	require.False(t, stream.Next())
	require.NoError(t, stream.Close())
	require.Len(t, recorded, 1)
}

func responsesOriginGateway(t *testing.T, executor pipeline.Executor) (context.Context, *ChatCompletionOrchestrator, *ent.Client, *ChannelModelsCandidate) {
	t.Helper()
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	ch = client.Channel.UpdateOneID(ch.ID).SetStatus("enabled").SaveX(ctx)
	ctx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
	processor.Inbound = responses.NewInboundTransformer()
	processor.responsesSessions = newResponsesSessionStore(processor.RequestService.LoadCompletedResponsesSession)
	processor.responsesSessions.save = processor.RequestService.SaveResponsesContext
	outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{
		BaseURL:        ch.BaseURL,
		APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider(ch.Credentials.APIKey)),
	})
	require.NoError(t, err)
	candidate := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: ch, Outbound: outbound}, APIFormat: string(llm.APIFormatOpenAIResponse),
		Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
	}
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{candidate}}
	return shared.WithSessionScope(shared.WithResponsesAPI(ctx), "origin-fixture"), processor, client, candidate
}

func TestResponsesOriginInterruptedHistoryRestoresAndRoutesTogether(t *testing.T) {
	executor := &mockExecutor{streamEvents: []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created","response":{"id":"resp_interrupted","object":"response","status":"in_progress","model":"gpt-4","output":[]}}`)},
		{Data: []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_done","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_finished-cipher"}}`)},
	}}
	ctx, processor, client, source := responsesOriginGateway(t, executor)
	projectID, _ := contexts.GetProjectID(ctx)
	key := client.APIKey.Create().SetProjectID(projectID).SetName("origin-owner").SetKey("fixture-origin-owner").SaveX(ctx)
	ctx = contexts.WithAPIKey(ctx, key)
	require.NoError(t, processor.SystemService.SetStoragePolicy(ctx, &biz.StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
	canceledCtx, cancel := context.WithCancel(shared.WithResponsesWebSocket(ctx))
	result, err := processor.Process(canceledCtx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","stream":true,"input":"first"}`)})
	require.NoError(t, err)
	require.NotNil(t, result.RetainResponsesContinuation)
	require.True(t, result.ChatCompletionStream.Next())
	cancel()
	require.NoError(t, result.RetainResponsesContinuation([]byte(`{"id":"resp_interrupted","status":"incomplete","incomplete_details":{"reason":"interrupted"},"output":[{"id":"rs_done","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_finished-cipher"}]}`)))
	require.NoError(t, result.ChatCompletionStream.Close())
	require.Equal(t, "canceled", string(client.Request.GetX(ctx, result.RequestID).Status))
	// Reconstruct memory to prove that the persisted history and origin agree.
	processor.responsesSessions = newResponsesSessionStore(processor.RequestService.LoadCompletedResponsesSession)
	executor.response = &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_resumed","object":"response","status":"completed","model":"gpt-4","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`)}
	resumed, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","previous_response_id":"resp_interrupted","input":"continue"}`)})
	require.NoError(t, err)
	require.Contains(t, string(executor.lastRequest.Body), "gAAAA_finished-cipher")
	require.NotContains(t, string(executor.lastRequest.Body), "previous_response_id")
	require.Equal(t, source.Channel.ID, client.Request.GetX(ctx, resumed.RequestID).ChannelID)
	route, found, err := processor.RequestService.GetResponsesContinuationRoute(ctx, "resp_interrupted", "gAAAA_finished-cipher")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, source.Channel.ID, route.ChannelID)
}

type responsesGatewayExecutor struct {
	mockExecutor

	forward func(*httpclient.Request) (ChatCompletionResult, error)
}

func (e *responsesGatewayExecutor) Do(_ context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	e.lastRequest = request
	result, err := e.forward(request)
	return result.ChatCompletion, err
}

func TestResponsesOriginAcrossTwoGateways(t *testing.T) {
	provider := &mockExecutor{response: &httpclient.Response{
		StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"id":"resp_vendor","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_vendor","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_vendor-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
	}}
	innerCtx, inner, _, innerSource := responsesOriginGateway(t, provider)
	bridge := &responsesGatewayExecutor{forward: func(req *httpclient.Request) (ChatCompletionResult, error) {
		copy := *req
		return inner.Process(innerCtx, &copy)
	}}
	outerCtx, outer, outerDB, outerSource := responsesOriginGateway(t, bridge)
	outerSource.Channel.Channel = outerDB.Channel.UpdateOneID(outerSource.Channel.ID).
		SetBaseURL("https://inner-gateway.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-inner-gateway-key"}).SaveX(outerCtx)
	var err error
	outerSource.Channel.Outbound, err = responses.NewOutboundTransformerWithConfig(&responses.Config{
		BaseURL:        outerSource.Channel.BaseURL,
		APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider(outerSource.Channel.Credentials.APIKey)),
	})
	require.NoError(t, err)
	for _, input := range []string{`"first"`, `[{"type":"reasoning","summary":[],"encrypted_content":"gAAAA_vendor-cipher"},{"role":"user","content":"continue"}]`} {
		_, err := outer.Process(outerCtx, &httpclient.Request{Method: "POST", Body: []byte(fmt.Sprintf(`{"model":"gpt-4","input":%s}`, input))})
		require.NoError(t, err)
	}
	for _, gateway := range []struct {
		ctx       context.Context
		processor *ChatCompletionOrchestrator
		source    *ChannelModelsCandidate
	}{{innerCtx, inner, innerSource}, {outerCtx, outer, outerSource}} {
		route, found, err := gateway.processor.RequestService.GetResponsesContinuationRoute(gateway.ctx, "resp_vendor", "gAAAA_vendor-cipher")
		require.NoError(t, err)
		require.True(t, found, "each gateway must retain its own upstream origin")
		require.Equal(t, gateway.source.Channel.ID, route.ChannelID)
		require.NotEmpty(t, route.KeyRef)
		require.NotEmpty(t, route.UpstreamRef)
	}
	require.EqualValues(t, 2, provider.requestCalls.Load())
	require.Contains(t, string(provider.lastRequest.Body), "gAAAA_vendor-cipher")
	require.Contains(t, bridge.lastRequest.URL, "inner-gateway.example")
	require.Contains(t, provider.lastRequest.URL, "api.openai.com")
	innerRoute, _, _ := inner.RequestService.GetResponsesContinuationRoute(innerCtx, "resp_vendor")
	outerRoute, _, _ := outer.RequestService.GetResponsesContinuationRoute(outerCtx, "resp_vendor")
	require.NotEqual(t, innerRoute.KeyRef, outerRoute.KeyRef)
	require.NotEqual(t, innerRoute.UpstreamRef, outerRoute.UpstreamRef)
}

func TestResponsesOriginCompactToResponses(t *testing.T) {
	executor := &mockExecutor{response: &httpclient.Response{
		StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
		Request: &httpclient.Request{RequestType: string(llm.RequestTypeCompact)},
		Body:    []byte(`{"id":"cmp_origin","object":"response.compaction","model":"gpt-4","output":[{"id":"cmp_item","type":"compaction","encrypted_content":"gAAAA_compact"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`),
	}}
	ctx, processor, client, source := responsesOriginGateway(t, executor)
	// The compact endpoint does not install the ordinary Responses session flag.
	projectID, _ := contexts.GetProjectID(ctx)
	compactCtx := contexts.WithProjectID(ent.NewContext(authz.WithTestBypass(t.Context()), client), projectID)
	processor.Inbound = responses.NewCompactInboundTransformer()
	source.APIFormat = string(llm.APIFormatOpenAIResponseCompact)
	compacted, err := processor.Process(compactCtx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":[{"role":"user","content":"first"}]}`)})
	require.NoError(t, err)
	require.Equal(t, string(llm.APIFormatOpenAIResponseCompact), client.Request.GetX(ctx, compacted.RequestID).Format)
	require.Contains(t, executor.lastRequest.URL, "/responses/compact")
	route, found, err := processor.RequestService.GetResponsesContinuationRoute(ctx, "", "gAAAA_compact")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, source.Channel.ID, route.ChannelID)
	processor.Inbound = responses.NewInboundTransformer()
	source.APIFormat = string(llm.APIFormatOpenAIResponse)
	executor.response = &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_after_compact","object":"response","status":"completed","model":"gpt-4","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`)}
	_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":[{"id":"cmp_item","type":"compaction","encrypted_content":"gAAAA_compact"},{"role":"user","content":"continue"}]}`)})
	require.NoError(t, err)
	require.Contains(t, string(executor.lastRequest.Body), "gAAAA_compact")
	require.NotContains(t, executor.lastRequest.URL, "/compact")
	require.EqualValues(t, 2, executor.requestCalls.Load())
}

func rejectResponsesOriginWrites(client *ent.Client) {
	client.ResponsesOrigin.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, fmt.Errorf("fixture origin write failure")
		})
	})
}

func TestResponsesOriginWriteFailurePreservesNonStreamDelivery(t *testing.T) {
	executor := &mockExecutor{response: &httpclient.Response{
		StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"id":"resp_write_failure","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_write_failure","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_unrecorded-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
	}}
	ctx, processor, client, _ := responsesOriginGateway(t, executor)
	rejectResponsesOriginWrites(client)
	result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
	require.NoError(t, err)
	require.Contains(t, string(result.ChatCompletion.Body), "gAAAA_unrecorded-cipher")
	require.NotNil(t, processor.responsesSessions.lookup(ctx, "resp_write_failure"))
	require.Zero(t, client.ResponsesOrigin.Query().CountX(ctx))
	require.EqualValues(t, 1, executor.requestCalls.Load(), "an already completed generation must not be replayed")
	require.Equal(t, "completed", string(client.RequestExecution.Query().OnlyX(ctx).Status))
	require.Equal(t, "completed", string(client.Request.GetX(ctx, result.RequestID).Status))
}

func TestResponsesOriginNonStreamWithoutStatus(t *testing.T) {
	for _, rejectWrites := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject_writes=%t", rejectWrites), func(t *testing.T) {
			executor := &mockExecutor{response: &httpclient.Response{
				StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
				Body: []byte(`{"id":"resp_no_status","object":"response","model":"gpt-4","output":[{"id":"rs_no_status","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_no-status-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
			}}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			passthrough := true
			source.Channel.Settings = &objects.ChannelSettings{PassThroughBody: &passthrough}
			if rejectWrites {
				rejectResponsesOriginWrites(client)
			}
			result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
			require.EqualValues(t, 1, executor.requestCalls.Load(), "origin recording must not replay generation")
			if rejectWrites {
				require.NoError(t, err)
				require.Equal(t, executor.response.Body, result.ChatCompletion.Body)
				require.NotNil(t, processor.responsesSessions.lookup(ctx, "resp_no_status"))
				require.Zero(t, client.ResponsesOrigin.Query().CountX(ctx))
				require.Equal(t, "completed", string(client.Request.GetX(ctx, result.RequestID).Status))
				return
			}
			require.NoError(t, err)
			require.Equal(t, executor.response.Body, result.ChatCompletion.Body, "passthrough payload must remain unchanged")
			require.Equal(t, 2, client.ResponsesOrigin.Query().CountX(ctx))
			for _, evidence := range []struct{ responseID, cipher string }{{"resp_no_status", ""}, {"", "gAAAA_no-status-cipher"}} {
				route, found, err := processor.RequestService.GetResponsesContinuationRoute(ctx, evidence.responseID, evidence.cipher)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, source.Channel.ID, route.ChannelID)
			}
		})
	}
}

func TestResponsesOriginWriteFailurePreservesStreamDelivery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			executor := &mockExecutor{streamEvents: []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_stream_write_failure","object":"response","status":"in_progress","model":"gpt-4","output":[]}}`)},
				{Data: []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_done","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_unrecorded-cipher"}}`)},
				{Data: []byte(`{"type":"response.completed","response":{"id":"resp_stream_write_failure","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_done","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_unrecorded-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)},
			}}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			source.Channel.Settings = &objects.ChannelSettings{PassThroughBody: &passthrough}
			rejectResponsesOriginWrites(client)
			result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","stream":true,"input":"first"}`)})
			require.NoError(t, err)
			deliveredCipher := false
			for result.ChatCompletionStream.Next() {
				deliveredCipher = deliveredCipher || strings.Contains(string(result.ChatCompletionStream.Current().Data), "gAAAA_unrecorded-cipher")
			}
			require.True(t, deliveredCipher)
			require.NoError(t, result.ChatCompletionStream.Err())
			require.NoError(t, result.ChatCompletionStream.Close())
			require.NotNil(t, processor.responsesSessions.lookup(ctx, "resp_stream_write_failure"))
			require.Zero(t, client.ResponsesOrigin.Query().CountX(ctx))
			require.EqualValues(t, 1, executor.requestCalls.Load())
			require.Eventually(t, func() bool {
				return string(client.Request.GetX(ctx, result.RequestID).Status) == "completed"
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestResponsesOriginDoneTerminal(t *testing.T) {
	for _, status := range []string{`,"status":"completed"`, ""} {
		t.Run(fmt.Sprintf("explicit_status=%t", status != ""), func(t *testing.T) {
			executor := &mockExecutor{streamEvents: []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_done","object":"response","status":"in_progress","model":"gpt-4","output":[]}}`)},
				{Data: []byte(fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_done","object":"response"%s,"model":"gpt-4","output":[{"id":"rs_done","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_done-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, status))},
			}}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			passthrough := true
			source.Channel.Settings = &objects.ChannelSettings{PassThroughBody: &passthrough}
			result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","stream":true,"input":"first"}`)})
			require.NoError(t, err)
			deliveredCipher := false
			for result.ChatCompletionStream.Next() {
				if strings.Contains(string(result.ChatCompletionStream.Current().Data), "gAAAA_done-cipher") {
					deliveredCipher = true
					_, found, err := processor.RequestService.GetResponsesContinuationRoute(ctx, "resp_done", "gAAAA_done-cipher")
					require.NoError(t, err)
					require.True(t, found, "the origin must exist before the complete item is delivered")
				}
			}
			require.NoError(t, result.ChatCompletionStream.Err())
			require.NoError(t, result.ChatCompletionStream.Close())
			require.True(t, deliveredCipher)
			require.Equal(t, 2, client.ResponsesOrigin.Query().CountX(ctx))
		})
	}
}

func TestResponsesOriginRetryUsesConfiguredBackupAddress(t *testing.T) {
	response := &httpclient.Response{
		StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"id":"resp_retry","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_retry","type":"reasoning","status":"completed","summary":[],"encrypted_content":"gAAAA_retry-cipher"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
	}
	for _, scenario := range []struct{ status, sourcePriority int }{{503, 0}, {504, 0}, {524, 0}, {504, 9}} {
		status := scenario.status
		t.Run(fmt.Sprintf("status=%d/source_priority=%d", status, scenario.sourcePriority), func(t *testing.T) {
			executor := &sequenceExecutor{steps: []executorStep{{resp: response}, {err: &httpclient.Error{StatusCode: status}}, {resp: response}}}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			source.Channel.Settings = &objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{
				{ID: "main", URL: source.Channel.BaseURL, Priority: 0, Enabled: true},
				{ID: "backup", URL: "https://backup.example/v1", Priority: 1, Enabled: true},
			}}
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.MaxSingleChannelRetries = 2
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
			require.NoError(t, err)
			source.Channel.Settings.UpstreamAddresses[0].Priority = scenario.sourcePriority
			_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":[{"type":"reasoning","summary":[],"encrypted_content":"gAAAA_retry-cipher"},{"role":"user","content":"continue"}]}`)})

			require.NoError(t, err)
			require.Len(t, executor.requests, 3)
			require.NotEqual(t, executor.requests[1].URL, executor.requests[2].URL)
			require.NotEmpty(t, executor.requests[1].Headers.Get("Authorization"))
			require.Equal(t, executor.requests[1].Headers.Get("Authorization"), executor.requests[2].Headers.Get("Authorization"))
			require.Equal(t, executor.requests[1].Body, executor.requests[2].Body)
			require.Contains(t, executor.requests[2].URL, "backup.example")
			executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
			require.Equal(t, "next_address", executions[1].RetryDecision)
		})
	}
}
