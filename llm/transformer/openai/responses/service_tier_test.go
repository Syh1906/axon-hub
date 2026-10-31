package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestServiceTierTerminalResponseSurvivesAggregationAndConversion(t *testing.T) {
	for _, kind := range []string{"response.completed", "response.done"} {
		for _, tier := range []string{"priority", "default", "ultrafast", ""} {
			t.Run(kind+"/"+tier, func(t *testing.T) {
				final := map[string]any{"type": kind, "response": map[string]any{"id": "resp_test", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}}}
				if tier != "" {
					final["response"].(map[string]any)["service_tier"] = tier
				}
				data, err := json.Marshal(final)
				require.NoError(t, err)
				chunks := []*httpclient.StreamEvent{
					{Type: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_test","service_tier":"default","output":[]}}`)},
					{Type: kind, Data: data},
				}
				body, _, err := AggregateStreamChunks(t.Context(), chunks)
				require.NoError(t, err)
				var response Response
				require.NoError(t, json.Unmarshal(body, &response))
				if tier == "" {
					require.Nil(t, response.ServiceTier)
				} else {
					require.Equal(t, tier, *response.ServiceTier)
				}
				trans, err := NewOutboundTransformer("http://localhost", "test")
				require.NoError(t, err)
				stream, err := trans.TransformStream(t.Context(), nil, streams.SliceStream(chunks))
				require.NoError(t, err)
				rows, err := streams.All(stream)
				require.NoError(t, err)
				var usage *llm.Response
				for _, row := range rows {
					if row != llm.DoneResponse && row.Usage != nil {
						usage = row
					}
				}
				require.NotNil(t, usage)
				require.Equal(t, tier, usage.ServiceTier)
			})
		}
	}
}

func TestServiceTierIncompleteStreamDoesNotPromoteInitialDeclaration(t *testing.T) {
	body, _, err := AggregateStreamChunks(t.Context(), []*httpclient.StreamEvent{{Type: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_test","service_tier":"priority","output":[]}}`)}})
	require.NoError(t, err)
	var response Response
	require.NoError(t, json.Unmarshal(body, &response))
	require.Nil(t, response.ServiceTier)
}

func TestServiceTierInvalidUpstreamPreservesResponseAndUsage(t *testing.T) {
	trans, err := NewOutboundTransformer("http://localhost", "test")
	require.NoError(t, err)
	for _, tier := range []string{`123`, `{}`, `[]`, `true`, `null`, `"priority"`} {
		t.Run(tier, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"id":"resp_test","status":"completed","service_tier":%s,"output":[{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`, tier))
			wantTier := ""
			if tier == `"priority"` {
				wantTier = "priority"
			}
			response, err := trans.TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200, Body: body})
			require.NoError(t, err)
			require.Equal(t, wantTier, response.ServiceTier)
			require.EqualValues(t, 10, response.Usage.PromptTokens)
			require.Equal(t, "hello", *response.Choices[0].Message.Content.Content)
			chunks := []*httpclient.StreamEvent{{Type: "response.completed", Data: []byte(`{"type":"response.completed","response":` + string(body) + `}`)}}
			stream, err := trans.TransformStream(t.Context(), nil, streams.SliceStream(chunks))
			require.NoError(t, err)
			rows, err := streams.All(stream)
			require.NoError(t, err)
			var usage *llm.Usage
			for _, row := range rows {
				if row.Usage != nil {
					usage = row.Usage
					require.Equal(t, wantTier, row.ServiceTier)
				}
			}
			require.NotNil(t, usage)
			require.EqualValues(t, 12, usage.TotalTokens)
			aggregated, _, err := AggregateStreamChunks(t.Context(), chunks)
			require.NoError(t, err)
			var result Response
			require.NoError(t, json.Unmarshal(aggregated, &result))
			require.NotNil(t, result.Usage)
			require.EqualValues(t, 12, result.Usage.TotalTokens)
			if tier != `null` && tier != `"priority"` {
				_, err = NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":"hello","service_tier":` + tier + `}`)})
				require.Error(t, err)
			}
		})
	}
	_, err = trans.TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200, Body: []byte(`{"id":"resp_test","service_tier":42,"usage":{"input_tokens":"bad"}}`)})
	require.Error(t, err)
}

func TestTransportRestoreFailureOnlyBlocksLocalContinuation(t *testing.T) {
	cause := errors.New("database unavailable")
	ctx := shared.WithResponsesRestoreError(context.Background(), cause)
	request := &httpclient.Request{Body: []byte(`{"previous_response_id":"resp_test","input":[]}`)}
	for _, local := range []bool{true, false} {
		inner := &httpTransportCaptureExecutor{}
		_, err := NewTransportExecutor(inner, nil, local).DoStream(ctx, request)
		if local {
			require.ErrorIs(t, err, httpclient.ErrContinuationRestoreFailed)
			require.ErrorIs(t, err, cause)
			require.Nil(t, inner.request)
		} else {
			require.NoError(t, err)
			require.NotNil(t, inner.request)
		}
	}
}
