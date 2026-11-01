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
