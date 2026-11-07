package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestRequestToSegmentUnavailablePayload(t *testing.T) {
	for _, protocol := range []struct {
		format       string
		requestBody  objects.JSONRawMessage
		responseBody objects.JSONRawMessage
	}{
		{
			format:       "openai/chat_completions",
			requestBody:  objects.JSONRawMessage(`{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`),
			responseBody: objects.JSONRawMessage(`{"id":"response-1","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
		},
		{
			format:       "openai/responses",
			requestBody:  objects.JSONRawMessage(`{"model":"test-model","input":"hello"}`),
			responseBody: objects.JSONRawMessage(`{"id":"resp-1","object":"response","model":"test-model","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello back","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`),
		},
	} {
		for _, state := range []string{"disabled", "expired", "evicted", "unavailable", "invalid", "too_large"} {
			for _, side := range []string{"request", "response", "both"} {
				t.Run(protocol.format+"/"+state+"/"+side, func(t *testing.T) {
					created := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
					req := &ent.Request{
						ID: 42, ModelID: "test-model", Format: protocol.format,
						CreatedAt: created, UpdatedAt: created.Add(time.Second),
						RequestBody: protocol.requestBody, ResponseBody: protocol.responseBody,
					}
					marker := payloadMarker(requestPayloadReference{State: state})
					if side == "request" || side == "both" {
						req.RequestBody = marker
					}
					if side == "response" || side == "both" {
						req.ResponseBody = marker
					}
					segment, err := requestToSegment(context.Background(), req)
					require.NoError(t, err)
					require.Equal(t, req.ID, segment.ID)
					require.Equal(t, int64(1000), segment.Duration)
					if side == "response" {
						require.NotNil(t, findSpanByType(segment.RequestSpans, "user_query"))
					} else {
						require.Empty(t, segment.RequestSpans)
					}
					if side == "request" {
						require.NotNil(t, findSpanByType(segment.ResponseSpans, "text"))
						require.NotNil(t, segment.Metadata)
						require.Equal(t, int64(15), *segment.Metadata.TotalTokens)
					} else {
						require.Empty(t, segment.ResponseSpans)
						require.Nil(t, segment.Metadata)
					}
				})
			}
		}
	}
}
