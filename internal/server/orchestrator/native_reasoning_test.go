package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestNativeResponsesReasoningWithoutPassThrough(t *testing.T) {
	const reasoning = `{"type":"reasoning","id":"rs_native","summary":[],"content":[{"type":"reasoning_text","text":"native full text"}],"encrypted_content":"opaque-native"}`
	const input = `[` + reasoning + `,{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"result"}]`
	for _, passThrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("pass-through=%v", passThrough), func(t *testing.T) {
			outbound, err := responses.NewOutboundTransformer("https://example.invalid", "synthetic")
			require.NoError(t, err)
			state := &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{
				ID: 1, Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(passThrough)},
			}}}}
			persistent := &PersistentOutboundTransformer{wrapped: outbound, state: state}
			capture := pipeline.OnLlmRequest("capture-request", func(_ context.Context, req *llm.Request) (*llm.Request, error) {
				state.LlmRequest = req
				state.OriginalRequestStream = req.Stream
				return req, nil
			})
			executor := pipeline.NewMockExecutor(t)
			executor.EXPECT().DoStream(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, req *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
				var body struct {
					Input json.RawMessage `json:"input"`
				}
				require.NoError(t, json.Unmarshal(req.Body, &body))
				require.JSONEq(t, input, string(body.Input))
				require.Equal(t, passThrough, state.PassThroughApplied)
				require.Equal(t, "openai/responses", req.APIFormat)
				return streams.SliceStream([]*httpclient.StreamEvent{
					{Data: []byte(`{"type":"response.created","response":{"id":"resp_native","model":"deepseek-v4-pro","status":"in_progress","output":[]}}`)},
					{Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_native","summary":[],"content":[]}}`)},
					{Data: []byte(`{"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_native","content_index":0,"delta":"native full text"}`)},
					{Data: []byte(`{"type":"response.output_item.done","output_index":0,"item":` + reasoning + `}`)},
					{Data: []byte(`{"type":"response.completed","response":{"id":"resp_native","model":"deepseek-v4-pro","status":"completed","output":[` + reasoning + `]}}`)},
				}), nil
			}).Once()
			pipe := pipeline.NewFactory(executor).Pipeline(responses.NewInboundTransformer(), outbound, pipeline.WithMiddlewares(
				capture, applyPassThroughRequestBody(persistent, nil), captureRawProviderStream(persistent), applyPassThroughStream(persistent),
			))
			result, err := pipe.Process(t.Context(), &httpclient.Request{Body: []byte(`{"model":"deepseek-v4-pro","stream":true,"input":` + input + `}`)})
			require.NoError(t, err)
			defer result.EventStream.Close()
			chunks, err := streams.All(result.EventStream)
			require.NoError(t, err)
			var doneItem json.RawMessage
			var reasoningTextDelta bool
			for _, chunk := range chunks {
				if string(chunk.Data) == "[DONE]" {
					continue
				}
				var event struct {
					Type string          `json:"type"`
					Item json.RawMessage `json:"item"`
				}
				require.NoError(t, json.Unmarshal(chunk.Data, &event))
				if event.Type == "response.output_item.done" {
					doneItem = event.Item
				}
				if event.Type == "response.reasoning_text.delta" {
					reasoningTextDelta = true
				}
			}
			require.True(t, reasoningTextDelta, "native full-text events must not turn into summary events")
			require.JSONEq(t, reasoning, string(doneItem))
		})
	}
}
