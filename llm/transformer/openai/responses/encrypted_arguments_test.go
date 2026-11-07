package responses

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestEncryptedFunctionArgsRoundTrip(t *testing.T) {
	for _, marker := range []string{"", "[]", `["message"]`} {
		t.Run("marker="+marker, func(t *testing.T) {
			item := map[string]any{
				"type": "function_call", "id": "fc_test", "call_id": "call_test",
				"namespace": "collaboration", "name": "send_message", "status": "completed",
				"arguments": `{"target":"worker","message":"synthetic task"}`,
			}
			if marker != "" {
				item["encrypted_function_args"] = json.RawMessage(marker)
			}
			response := map[string]any{"id": "resp_test", "object": "response", "created_at": 1700000000,
				"model": "test", "status": "completed", "output": []any{item}}
			outbound, err := NewOutboundTransformer("https://example.invalid/v1", "unused")
			require.NoError(t, err)
			inbound := NewInboundTransformer()
			marshal := func(value any) []byte {
				body, err := json.Marshal(value)
				require.NoError(t, err)
				return body
			}
			check := func(t *testing.T, body []byte, path string) {
				t.Helper()
				require.Equal(t, "function_call", gjson.GetBytes(body, path+".type").String())
				require.Equal(t, marker, gjson.GetBytes(body, path+".encrypted_function_args").Raw)
			}
			t.Run("nonstream", func(t *testing.T) {
				unified, err := outbound.TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200,
					Body: marshal(response), Request: &httpclient.Request{}})
				require.NoError(t, err)
				// Exercise metadata after JSON persistence as well as live conversion.
				var restored llm.Response
				require.NoError(t, json.Unmarshal(marshal(unified), &restored))
				wire, err := inbound.TransformResponse(t.Context(), &restored)
				require.NoError(t, err)
				check(t, wire.Body, "output.0")
			})
			for _, reasoning := range []bool{false, true} {
				name := "history"
				if reasoning {
					name += "_with_reasoning"
				}
				t.Run(name, func(t *testing.T) {
					input := []any{item, map[string]any{"type": "function_call_output", "call_id": "call_test", "output": "ok"}}
					index := "0"
					if reasoning {
						input = append([]any{map[string]any{"id": "rs_test", "type": "reasoning", "encrypted_content": "gAAAA_fixture", "summary": []any{}}}, input...)
						index = "1"
					}
					unified, err := inbound.TransformRequest(t.Context(), &httpclient.Request{
						Body: marshal(map[string]any{"model": "test", "input": input})})
					require.NoError(t, err)
					wire, err := outbound.TransformRequest(t.Context(), unified)
					require.NoError(t, err)
					check(t, wire.Body, "input."+index)
				})
			}
			for _, streamCase := range []struct {
				name           string
				late           bool
				omitDoneStatus bool
			}{
				{"stream", false, false},
				{"stream_marker_at_done", true, false},
				{"stream_marker_at_done_without_status", true, true},
			} {
				t.Run(streamCase.name, func(t *testing.T) {
					added := map[string]any{}
					doneItem := map[string]any{}
					for k, v := range item {
						added[k] = v
						doneItem[k] = v
					}
					added["status"], added["arguments"] = "in_progress", ""
					if streamCase.late {
						delete(added, "encrypted_function_args")
					}
					if streamCase.omitDoneStatus {
						delete(doneItem, "status")
					}
					event := func(kind string, fields map[string]any) *httpclient.StreamEvent {
						fields["type"] = kind
						return &httpclient.StreamEvent{Type: kind, Data: marshal(fields)}
					}
					events := []*httpclient.StreamEvent{
						event("response.created", map[string]any{"response": map[string]any{"id": "resp_test", "created_at": 1700000000, "model": "test", "status": "in_progress", "output": []any{}}}),
						event("response.output_item.added", map[string]any{"output_index": 0, "item": added}),
						event("response.function_call_arguments.delta", map[string]any{"item_id": "fc_test", "output_index": 0, "delta": item["arguments"]}),
						event("response.output_item.done", map[string]any{"output_index": 0, "item": doneItem}),
						event("response.completed", map[string]any{"response": response}),
					}
					unified, err := outbound.TransformStream(t.Context(), nil, streams.SliceStream(events))
					require.NoError(t, err)
					wire, err := inbound.TransformStream(t.Context(), unified)
					require.NoError(t, err)
					actual, err := streams.All(wire)
					require.NoError(t, err)
					var done, completed int
					for _, ev := range actual {
						switch ev.Type {
						case "response.output_item.done":
							check(t, ev.Data, "item")
							// The final marker must not cause the function arguments to repeat.
							require.Equal(t, item["arguments"], gjson.GetBytes(ev.Data, "item.arguments").String())
							done++
						case "response.completed":
							check(t, ev.Data, "response.output.0")
							completed++
						}
					}
					require.Equal(t, 1, done)
					require.Equal(t, 1, completed)
					aggregated, _, err := AggregateStreamChunks(t.Context(), actual)
					require.NoError(t, err)
					check(t, aggregated, "output.0")
				})
			}
		})
	}
}

func TestEncryptedFunctionArgsMetadataOwnership(t *testing.T) {
	args := []string{"message"}
	tc := llm.ToolCall{TransformerMetadata: encryptedFunctionArgsMetadata(&args)}
	args[0] = "changed"
	require.Equal(t, []string{"message"}, *encryptedFunctionArgs(tc))
	copy := encryptedFunctionArgs(tc)
	(*copy)[0] = "another change"
	require.Equal(t, []string{"message"}, *encryptedFunctionArgs(tc))
	retainEncryptedFunctionArgs(&tc, lo.ToPtr([]string{}))
	retainEncryptedFunctionArgs(&tc, nil)
	require.Equal(t, []string{}, *encryptedFunctionArgs(tc), "missing later metadata does not erase explicit plaintext")
}

func TestInvalidEncryptedContentStatus(t *testing.T) {
	err := &llm.ResponseError{Detail: llm.ErrorDetail{Code: "invalid_encrypted_content", Type: "invalid_request_error", Message: "Invalid encrypted content."}}
	wire := NewInboundTransformer().TransformError(t.Context(), err)
	require.Equal(t, http.StatusBadRequest, wire.StatusCode)
	require.Equal(t, "invalid_encrypted_content", gjson.GetBytes(wire.Body, "error.code").String())
	require.Zero(t, err.StatusCode, "classification must not mutate the original failure")
}

func TestEncryptedFunctionArgsWithoutStatusDoesNotCompleteFailedTool(t *testing.T) {
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created","response":{"id":"resp_test","model":"test","status":"in_progress"}}`)},
		{Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","namespace":"collaboration","name":"send_message","arguments":""}}`)},
		{Data: []byte(`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_test","delta":"{\"message\":\"text\"}"}`)},
		{Data: []byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","encrypted_function_args":[]}}`)},
		{Data: []byte(`{"type":"response.failed","response":{"id":"resp_test","status":"failed","error":{"code":"server_error","message":"generation failed"}}}`)},
	}
	unified, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream(events))
	require.NoError(t, err)
	wire, err := NewInboundTransformer().TransformStream(t.Context(), unified)
	require.NoError(t, err)
	actual, err := streams.All(wire)
	require.NoError(t, err)
	var failures int
	for _, event := range actual {
		require.NotEqual(t, "response.function_call_arguments.done", event.Type)
		require.NotEqual(t, "response.output_item.done", event.Type)
		if event.Type == "response.failed" {
			failures++
		}
	}
	require.Equal(t, 1, failures)
}
