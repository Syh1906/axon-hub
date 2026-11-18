package openai

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompletionNativePromptForms(t *testing.T) {
	inbound := NewCompletionInboundTransformer()
	outbound, err := NewCompletionOutboundTransformer(&Config{BaseURL: "https://provider.example/v1", APIKeyProvider: auth.NewStaticKeyProvider("synthetic")})
	require.NoError(t, err)
	for _, prompt := range []string{`"hello"`, `["hello","world"]`, `[101,202]`, `[[101,202],[303]]`} {
		request, err := inbound.TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"alias","prompt":` + prompt + `,"stream":true,"n":2}`)})
		require.NoError(t, err)
		request.Model = "native-model"
		raw, err := outbound.TransformRequest(t.Context(), request)
		require.NoError(t, err)
		require.JSONEq(t, prompt, gjson.GetBytes(raw.Body, "prompt").Raw)
		require.Equal(t, "native-model", gjson.GetBytes(raw.Body, "model").String())
		require.Equal(t, int64(2), gjson.GetBytes(raw.Body, "n").Int())
	}
}

func TestCompletionPreservesMissingAndExplicitZeroUsage(t *testing.T) {
	inbound := NewCompletionInboundTransformer()
	for _, usage := range []string{"", `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`} {
		wire := []byte(`{"id":"fixture","object":"text_completion","model":"m","choices":[{"text":"hello","index":0}]` + usage + `}`)
		var provider CompletionResponse
		require.NoError(t, json.Unmarshal(wire, &provider))
		normalized := completionResponseToLLM(&provider)
		if usage == "" {
			require.Nil(t, normalized.Usage)
		} else {
			require.NotNil(t, normalized.Usage)
		}
		raw, err := inbound.TransformResponse(t.Context(), normalized)
		require.NoError(t, err)
		require.Equal(t, usage != "", gjson.GetBytes(raw.Body, "usage").Exists(), string(raw.Body))
		chunk, err := inbound.transformStreamChunk(normalized)
		require.NoError(t, err)
		require.Equal(t, usage != "", gjson.GetBytes(chunk.Data, "usage").Exists(), string(chunk.Data))
		_, meta, err := AggregateCompletionStreamChunks(t.Context(), []*httpclient.StreamEvent{{Data: wire}})
		require.NoError(t, err)
		if usage == "" {
			require.Nil(t, meta.Usage)
		} else {
			require.NotNil(t, meta.Usage)
		}
	}
}

func TestCompletionAggregateRetainsChoiceIndexes(t *testing.T) {
	chunks := []*httpclient.StreamEvent{
		{Data: []byte(`{"id":"batch","model":"m","choices":[{"text":"B1","index":1},{"text":"A1","index":0}]}`)},
		{Data: []byte(`{"id":"batch","model":"m","choices":[{"text":"A2","index":0,"finish_reason":"stop"},{"text":"B2","index":1,"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":4,"total_tokens":8}}`)},
	}
	body, meta, err := AggregateCompletionStreamChunks(t.Context(), chunks)
	require.NoError(t, err)
	var result llm.Response
	require.NoError(t, json.Unmarshal(body, &result))
	require.Len(t, result.Completion.Choices, 2)
	require.Equal(t, "A1A2", result.Completion.Choices[0].Text)
	require.Equal(t, "B1B2", result.Completion.Choices[1].Text)
	require.True(t, meta.Completed)
}

func TestCompletionRejectsInvalidPromptForms(t *testing.T) {
	inbound := NewCompletionInboundTransformer()
	for _, prompt := range []string{`null`, `{}`, `[]`, `["hello",1]`, `[1.5]`, `[-1]`, `[[1],null]`} {
		_, err := inbound.TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"m","prompt":` + prompt + `}`)})
		require.Error(t, err, prompt)
	}
}

func TestCompletionPartialUsageRemainsUnknown(t *testing.T) {
	for _, usage := range []string{`{}`, `{"prompt_tokens":3}`, `{"completion_tokens":2}`, `{"total_tokens":5}`, `{"prompt_tokens":null,"completion_tokens":2}`} {
		var provider CompletionResponse
		require.NoError(t, json.Unmarshal([]byte(`{"choices":[{"text":"hello","index":0}],"usage":`+usage+`}`), &provider))
		normalized := completionResponseToLLM(&provider)
		require.Nil(t, normalized.Usage, usage)
		raw, err := NewCompletionInboundTransformer().TransformResponse(t.Context(), normalized)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(raw.Body, "usage").Exists(), string(raw.Body))
	}
}
