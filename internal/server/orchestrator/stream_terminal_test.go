package orchestrator

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestFailedTerminalPersistsFailureAfterDone(t *testing.T) {
	ctx, db, _, service, _, _, key := setupBudgetRequest(t)
	for _, status := range []string{"failed", "incomplete"} {
		req := db.Request.Create().SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).SetModelID("gpt-4").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusProcessing).SaveX(ctx)
		state := &PersistenceState{}
		events := &sliceEventStream{events: []*httpclient.StreamEvent{
			{Data: []byte(`{"type":"response.` + status + `","response":{"id":"terminal-test","status":"` + status + `"}}`)},
			&llm.DoneStreamEvent,
		}}
		stream := NewInboundPersistentStream(ctx, events, req, nil, service, &mockInboundTransformer{}, nil, state)
		for stream.Next() {
			stream.Current()
		}
		require.Error(t, stream.Err())
		require.NoError(t, stream.Close())
		require.Equal(t, request.StatusFailed, db.Request.GetX(ctx, req.ID).Status)
	}
}

func TestFailedTerminalCannotBecomeSuccessfulAtDone(t *testing.T) {
	for _, data := range []string{
		`{"type":"response.failed","response":{"status":"failed"}}`,
		`{"type":"response.completed","response":{"status":"failed"}}`,
		`{"type":"response.incomplete","response":{"status":"incomplete"}}`,
		`{"choices":[{"finish_reason":"error"}]}`,
	} {
		event := &httpclient.StreamEvent{Data: []byte(data)}
		state := &PersistenceState{}
		state.observeStreamTerminal(event)
		require.Error(t, state.StreamTerminalError)
		require.False(t, state.StreamCompleted)
		require.True(t, IsTerminalStreamEvent(event), "failure is still a terminal marker for the client writer")
		state.observeStreamTerminal(&llm.DoneStreamEvent)
		require.False(t, state.StreamCompleted)
	}
	require.False(t, isCompletedAggregated(llm.ResponseMeta{Status: "failed", Usage: &llm.Usage{CompletionTokens: 10}}))
	require.True(t, isCompletedAggregated(llm.ResponseMeta{Status: "completed"}))
}

func TestStreamTerminalPreservesUpstreamErrorDetails(t *testing.T) {
	data := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"provider_error","code":"tool_arguments_stalled","message":"tool arguments stalled","param":"output[1]"}}}`)
	err := streamTerminalError(&httpclient.StreamEvent{Data: data})
	var responseErr *llm.ResponseError
	require.ErrorAs(t, err, &responseErr)
	require.Equal(t, llm.ErrorDetail{Type: "provider_error", Code: "tool_arguments_stalled", Message: "tool arguments stalled", Param: "output[1]"}, responseErr.Detail)
}

func TestIncompleteToolStreamEmitsSingleTerminal(t *testing.T) {
	for _, status := range []string{"incomplete", "cancelled", "failed"} {
		for _, withUsage := range []bool{true, false} {
			for _, kind := range []string{"response.completed", "response." + status} {
				t.Run(fmt.Sprintf("%s/usage=%t/%s", status, withUsage, kind), func(t *testing.T) {
					testAbnormalToolStreamSingleTerminal(t, kind, status, withUsage)
				})
			}
		}
	}
}

func testAbnormalToolStreamSingleTerminal(t *testing.T, kind, status string, withUsage bool) {
	terminal := map[string]any{
		"id": "resp_incomplete", "status": status,
	}
	if status == "incomplete" {
		terminal["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	}
	if status == "failed" {
		terminal["error"] = map[string]string{"type": "upstream_error", "code": "fixture_failure", "message": "fixture failure"}
	}
	if withUsage {
		terminal["usage"] = map[string]int{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}
	}
	data, err := json.Marshal(map[string]any{"type": kind, "response": terminal})
	require.NoError(t, err)
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created","response":{"id":"resp_incomplete","model":"gpt-test","status":"in_progress"}}`)},
		{Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":"function_call","call_id":"call_1","name":"fixture","arguments":""}}`)},
		{Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"tool_1","output_index":0,"delta":"{\"path\":"}`)},
		{Data: data},
	}
	outbound := new(responses.OutboundTransformer)
	persistent := NewOutboundPersistentStream(t.Context(), streams.SliceStream(events), nil, nil, nil, nil, outbound, nil, &PersistenceState{})
	unified, err := outbound.TransformStream(t.Context(), nil, persistent)
	require.NoError(t, err)
	inbound, err := responses.NewInboundTransformer().TransformStream(t.Context(), unified)
	require.NoError(t, err)
	defer inbound.Close()
	var terminals []responses.StreamEvent
	for inbound.Next() {
		event := inbound.Current()
		var value responses.StreamEvent
		require.NoError(t, json.Unmarshal(event.Data, &value))
		require.NotEqual(t, responses.StreamEventTypeFunctionCallArgumentsDone, value.Type)
		if IsTerminalStreamEvent(event) {
			terminals = append(terminals, value)
		}
	}
	require.Len(t, terminals, 1)
	require.Equal(t, responses.StreamEventType("response."+status), terminals[0].Type)
	if status == "incomplete" {
		require.Equal(t, "max_output_tokens", terminals[0].Response.IncompleteDetails.Reason)
	}
	if withUsage {
		require.EqualValues(t, 3, terminals[0].Response.Usage.TotalTokens)
	} else {
		require.Nil(t, terminals[0].Response.Usage)
	}
	require.Error(t, persistent.state.StreamTerminalError)
	require.NoError(t, persistent.Err(), "semantic failure is forwarded in the terminal event")
	require.NoError(t, inbound.Err(), "the wire terminal already represents the incomplete response")
}

func TestFailedToolStreamAcrossTwoGateways(t *testing.T) {
	for _, tool := range []struct {
		name  string
		added string
		delta string
	}{
		{
			name:  "function_call",
			added: `{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":"function_call","call_id":"call_1","name":"fixture","arguments":""}}`,
			delta: `{"type":"response.function_call_arguments.delta","item_id":"tool_1","output_index":0,"delta":"{\"path\":"}`,
		},
		{
			name:  "custom_tool_call",
			added: `{"type":"response.output_item.added","output_index":0,"item":{"id":"tool_1","type":"custom_tool_call","call_id":"call_1","name":"exec","input":""}}`,
			delta: `{"type":"response.custom_tool_call_input.delta","item_id":"tool_1","output_index":0,"delta":"await tools."}`,
		},
	} {
		t.Run(tool.name, func(t *testing.T) {
			events := []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"resp_fixture","model":"gpt-test","status":"in_progress"}}`)},
				{Data: []byte(tool.added)},
				{Data: []byte(tool.delta)},
				{Data: []byte(`{"type":"response.failed","response":{"id":"resp_fixture","status":"failed","error":{"type":"upstream_error","code":"basispoints_tool_arguments_stalled","message":"tool arguments stalled","param":"output[0]"}}}`)},
			}
			for hop := range 2 {
				outbound := new(responses.OutboundTransformer)
				persistent := NewOutboundPersistentStream(t.Context(), streams.SliceStream(events), nil, nil, nil, nil, outbound, nil, &PersistenceState{})
				unified, err := outbound.TransformStream(t.Context(), nil, persistent)
				require.NoError(t, err)
				inbound, err := responses.NewInboundTransformer().TransformStream(t.Context(), unified)
				require.NoError(t, err)
				var forwarded []*httpclient.StreamEvent
				failures := 0
				for inbound.Next() {
					event := inbound.Current()
					forwarded = append(forwarded, event)
					var value responses.StreamEvent
					require.NoError(t, json.Unmarshal(event.Data, &value))
					require.NotEqual(t, responses.StreamEventTypeFunctionCallArgumentsDone, value.Type)
					require.NotEqual(t, responses.StreamEventTypeCustomToolCallInputDone, value.Type)
					if value.Type == responses.StreamEventTypeResponseFailed {
						failures++
						require.Equal(t, "tool arguments stalled", value.Response.Error.Message)
						require.Equal(t, "basispoints_tool_arguments_stalled", value.Response.Error.Code)
						require.Equal(t, "upstream_error", value.Response.Error.Type)
						require.Equal(t, "output[0]", value.Response.Error.Param)
						require.Len(t, value.Response.Output, 1)
						require.NotNil(t, value.Response.Output[0].Status)
						require.NotEqual(t, "completed", *value.Response.Output[0].Status)
					}
				}
				require.Equal(t, 1, failures, "hop %d must emit one terminal failure", hop)
				require.Error(t, persistent.state.StreamTerminalError)
				require.NoError(t, persistent.Err(), "semantic failure is forwarded in the terminal event")
				require.NoError(t, inbound.Err(), "the wire error already represents the failure")
				require.NoError(t, inbound.Close())
				events = forwarded
			}
		})
	}
}
