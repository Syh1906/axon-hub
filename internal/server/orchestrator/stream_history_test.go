package orchestrator

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func TestChatStreamHistoryWithAndWithoutChunkStorage(t *testing.T) {
	events := []string{
		`{"id":"r","choices":[{"index":0,"delta":{"content":"first "}}]}`,
		`{"id":"r","choices":[{"index":0,"delta":{"content":"second"},"finish_reason":"stop"}]}`,
		`{"id":"r","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`,
		`[DONE]`,
	}
	for _, store := range []bool{false, true} {
		state := &PersistenceState{
			routeDecision:      &firstContentDecision{},
			Settings:           &RequestSettings{StoragePolicy: biz.StoragePolicy{StoreChunks: store}},
			RawProviderRequest: &httpclient.Request{APIFormat: string(llm.APIFormatOpenAIChatCompletion)},
		}
		out := &openai.OutboundTransformer{}
		s := NewOutboundPersistentStream(t.Context(), nil, nil, nil, nil, nil, out, nil, state)
		for i, data := range events {
			event := &httpclient.StreamEvent{Data: []byte(data)}
			if i >= 2 {
				// Trailing usage or markers can arrive in the accounting tail.
				require.NoError(t, s.consumeTailEvent(event))
			} else {
				s.recordEvent(event)
			}
		}
		body, meta, err := s.aggregate(t.Context())
		require.NoError(t, err)
		require.Equal(t, "first second", gjson.GetBytes(body, "choices.0.message.content").String())
		require.Equal(t, "r", meta.ID)
		require.True(t, meta.Completed)
		require.Equal(t, llm.AccountingEvidenceExplicitFinal, meta.AccountingEvidence)
		require.EqualValues(t, 15, meta.Usage.TotalTokens)
		require.Equal(t, len(events), s.count)
		if store {
			require.Len(t, s.responseChunks, len(events))
			for i, event := range s.responseChunks {
				require.Equal(t, events[i], string(event.Data))
			}
		} else {
			require.Empty(t, s.responseChunks)
			require.True(t, s.cacheReported)
			require.EqualValues(t, 4, s.cachedTokens)
		}
	}
}

func TestPassThroughReusesUnchangedWireBytes(t *testing.T) {
	_, req := nativePassThroughFixture(t, 128)
	raw := req.RawRequest.Body
	body, err := mergePassThroughRequestBody(raw, req.APIFormat, req.Model)
	require.NoError(t, err)
	require.Same(t, &raw[0], &body[0])
	for _, wire := range []string{
		`{"type":"response.output_text.delta","delta":"text"}`,
		`{"type":"response.created","response":{"model":"alias"}}`,
	} {
		event := &httpclient.StreamEvent{Data: []byte(wire)}
		require.Same(t, event, preserveResponsesStreamEventModel(event, "alias"))
	}
	event := &httpclient.StreamEvent{Data: []byte(`{"model":"real","response":{"model":"real"}}`)}
	before := bytes.Clone(event.Data)
	mapped := preserveResponsesStreamEventModel(event, "alias")
	require.JSONEq(t, `{"model":"alias","response":{"model":"alias"}}`, string(mapped.Data))
	require.Equal(t, before, event.Data)
}
