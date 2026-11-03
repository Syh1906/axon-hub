package responses

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/stretchr/testify/require"
)

func TestNonStreamingTerminalRoundTripPreservesOutcomeWithToolCall(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete", "canceled", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"id":"resp-outcome","object":"response","model":"test","status":%q,"output":[{"id":"fc1","type":"function_call","call_id":"call1","name":"fixture","arguments":"{}"}]}`, status))
			response, err := new(OutboundTransformer).TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200, Body: body})
			require.NoError(t, err)
			converted, err := NewInboundTransformer().TransformResponse(t.Context(), response)
			require.NoError(t, err)
			var actual Response
			require.NoError(t, json.Unmarshal(converted.Body, &actual))
			expected := status
			if status == "canceled" {
				expected = "cancelled"
			}
			require.Equal(t, expected, *actual.Status)
			require.Len(t, actual.Output, 1)
			require.Equal(t, "function_call", actual.Output[0].Type)
		})
	}
}

func TestTerminalRoundTripPreservesOutcome(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			kind := "response." + status
			created := &httpclient.StreamEvent{Data: []byte(`{"type":"response.created","response":{"id":"resp_test","object":"response","status":"in_progress","model":"test","output":[]}}`)}
			data, err := json.Marshal(map[string]any{"type": kind, "response": map[string]any{"id": "resp_test", "status": status, "output": []any{}}})
			require.NoError(t, err)
			events := []*httpclient.StreamEvent{created, {Type: kind, Data: data}}
			unified, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream(events))
			require.NoError(t, err)
			stream, err := NewInboundTransformer().TransformStream(t.Context(), unified)
			require.NoError(t, err)
			defer stream.Close()
			var terminal StreamEvent
			for stream.Next() {
				var event StreamEvent
				require.NoError(t, json.Unmarshal(stream.Current().Data, &event))
				if event.Response != nil && event.Response.Status != nil && *event.Response.Status != "in_progress" {
					terminal = event
				}
			}
			require.NoError(t, stream.Err())
			require.Equal(t, StreamEventType(kind), terminal.Type)
			require.Equal(t, status, *terminal.Response.Status)
			_, meta, err := AggregateStreamChunks(t.Context(), events)
			require.NoError(t, err)
			require.Equal(t, status, meta.Status)
			require.Equal(t, status == "completed", meta.Completed)
		})
	}
}

func TestFailedToolGenerationDoesNotEmitCompletedArguments(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		t.Run(kind, func(t *testing.T) {
			deltaType := "response.function_call_arguments.delta"
			if kind == "custom_tool_call" {
				deltaType = "response.custom_tool_call_input.delta"
			}
			events := []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_failed_tool","model":"gpt-test","status":"in_progress","output":[]}}`)},
				{Data: []byte(fmt.Sprintf(`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":%q,"call_id":"call_1","name":"fixture","arguments":""}}`, kind))},
				{Data: []byte(fmt.Sprintf(`{"type":%q,"output_index":0,"item_id":"tool_1","delta":"{\"path\":"}`, deltaType))},
				{Data: []byte(`{"type":"response.failed","response":{"id":"resp_failed_tool","status":"failed","error":{"code":"tool_arguments_stalled","message":"tool arguments stopped making progress"}}}`)},
			}
			unified, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream(events))
			require.NoError(t, err)
			converted, err := NewInboundTransformer().TransformStream(t.Context(), unified)
			require.NoError(t, err)
			defer converted.Close()
			var terminalSeen bool
			for converted.Next() {
				var event StreamEvent
				require.NoError(t, json.Unmarshal(converted.Current().Data, &event))
				require.NotEqual(t, StreamEventTypeFunctionCallArgumentsDone, event.Type)
				require.NotEqual(t, StreamEventTypeCustomToolCallInputDone, event.Type)
				if event.Type == StreamEventTypeOutputItemDone && event.Item != nil && event.Item.Type == kind {
					t.Fatal("failed tool generation was marked completed")
				}
				terminalSeen = terminalSeen || event.Type == StreamEventTypeResponseFailed
				if event.Type == StreamEventTypeResponseFailed {
					require.NotNil(t, event.Response.Error)
					require.Equal(t, "tool_arguments_stalled", event.Response.Error.Code)
					require.Equal(t, "tool arguments stopped making progress", event.Response.Error.Message)
				}
			}
			require.NoError(t, converted.Err())
			require.True(t, terminalSeen)
		})
	}
}

func TestInterleavedToolsPreserveCompletionState(t *testing.T) {
	for _, status := range []string{"failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			events := []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_parallel","model":"gpt-test","status":"in_progress"}}`)},
				{Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":"custom_tool_call","call_id":"call_1","name":"exec","input":""}}`)},
				{Data: []byte(`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"tool_1","delta":"await tools."}`)},
				{Data: []byte(`{"type":"response.output_item.added","output_index":1,"item":{"id":"tool_2","type":"function_call","call_id":"call_2","name":"fixture","arguments":""}}`)},
				{Data: []byte(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"tool_2","delta":"{\"path\":"}`)},
			}
			if status == "completed" {
				events = append(events,
					&httpclient.StreamEvent{Data: []byte(`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"tool_1","delta":"noop()"}`)},
					&httpclient.StreamEvent{Data: []byte(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"tool_2","delta":"\"fixture\"}"}`)},
				)
			}
			events = append(events, &httpclient.StreamEvent{Data: []byte(fmt.Sprintf(
				`{"type":"response.%s","response":{"id":"resp_parallel","status":%q}}`, status, status,
			))})
			unified, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream(events))
			require.NoError(t, err)
			converted, err := NewInboundTransformer().TransformStream(t.Context(), unified)
			require.NoError(t, err)
			defer converted.Close()
			var completedInputs []string
			var completedItems int
			for converted.Next() {
				var event StreamEvent
				require.NoError(t, json.Unmarshal(converted.Current().Data, &event))
				switch event.Type {
				case StreamEventTypeCustomToolCallInputDone:
					completedInputs = append(completedInputs, event.Input)
				case StreamEventTypeFunctionCallArgumentsDone:
					completedInputs = append(completedInputs, event.Arguments)
				case StreamEventTypeOutputItemDone:
					completedItems++
				}
			}
			require.NoError(t, converted.Err())
			if status == "failed" {
				require.Empty(t, completedInputs)
				require.Zero(t, completedItems)
			} else {
				require.ElementsMatch(t, []string{"await tools.noop()", `{"path":"fixture"}`}, completedInputs)
				require.Equal(t, 2, completedItems)
			}
		})
	}
}

func TestCompletedToolSurvivesLaterFailure(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		for _, itemStatus := range []string{"completed", "in_progress"} {
			t.Run(kind+"/"+itemStatus, func(t *testing.T) {
				item := Item{ID: "tool_1", Type: kind, CallID: "call_1", Name: "fixture", Status: &itemStatus}
				deltaType, partial, finalInput := StreamEventTypeFunctionCallArgumentsDelta, `{"path":`, `{"path":"fixture"}`
				item.Arguments = finalInput
				if kind == "custom_tool_call" {
					deltaType, partial, finalInput = StreamEventTypeCustomToolCallInputDelta, "await tools.", "await tools.noop()"
					item.Arguments, item.Input = "", &finalInput
				}
				done, err := json.Marshal(StreamEvent{Type: StreamEventTypeOutputItemDone, Item: &item})
				require.NoError(t, err)
				events := []*httpclient.StreamEvent{
					{Data: []byte(`{"type":"response.created","response":{"id":"resp_partial","model":"gpt-test","status":"in_progress"}}`)},
					{Data: []byte(fmt.Sprintf(`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":%q,"call_id":"call_1","name":"fixture"}}`, kind))},
					{Data: []byte(fmt.Sprintf(`{"type":%q,"output_index":0,"item_id":"tool_1","delta":%q}`, deltaType, partial))},
					{Data: done},
					{Data: []byte(`{"type":"response.output_item.added","output_index":1,"item":{"id":"tool_2","type":"function_call","call_id":"call_2","name":"fixture"}}`)},
					{Data: []byte(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"tool_2","delta":"{"}`)},
					{Data: []byte(`{"type":"response.failed","response":{"id":"resp_partial","status":"failed"}}`)},
				}
				unified, err := new(OutboundTransformer).TransformStream(t.Context(), nil, streams.SliceStream(events))
				require.NoError(t, err)
				chunks, err := streams.All(unified)
				require.NoError(t, err)
				if kind == "function_call" {
					var names, ids string
					for _, chunk := range chunks {
						for _, choice := range chunk.Choices {
							if choice.Delta == nil {
								continue
							}
							for _, tc := range choice.Delta.ToolCalls {
								if tc.Index == 0 {
									names += tc.Function.Name
									ids += tc.ID
								}
							}
						}
					}
					require.Equal(t, "fixture", names, "completion metadata must not repeat the function name in chat deltas")
					require.Equal(t, "call_1", ids, "completion metadata must not repeat the call ID in chat deltas")
				}
				converted, err := NewInboundTransformer().TransformStream(t.Context(), streams.SliceStream(chunks))
				require.NoError(t, err)
				defer converted.Close()
				var completedInputs []string
				var failed *Response
				for converted.Next() {
					var event StreamEvent
					require.NoError(t, json.Unmarshal(converted.Current().Data, &event))
					switch event.Type {
					case StreamEventTypeFunctionCallArgumentsDone:
						completedInputs = append(completedInputs, event.Arguments)
					case StreamEventTypeCustomToolCallInputDone:
						completedInputs = append(completedInputs, event.Input)
					case StreamEventTypeResponseFailed:
						failed = event.Response
					}
				}
				require.NoError(t, converted.Err())
				require.NotNil(t, failed)
				require.Len(t, failed.Output, 2)
				require.Equal(t, itemStatus, *failed.Output[0].Status)
				require.Equal(t, "in_progress", *failed.Output[1].Status)
				if itemStatus == "completed" {
					require.Equal(t, []string{finalInput}, completedInputs)
				} else {
					require.Empty(t, completedInputs)
				}
			})
		}
	}
}
