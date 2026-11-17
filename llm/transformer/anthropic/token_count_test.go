package anthropic

import (
	"context"
	"net/http"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTokenCountNativeRoundTrip(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"model":"alias","messages":[{"role":"user","content":[{"type":"text","text":"Hello"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}],"system":"Be concise","tools":[{"name":"search","input_schema":{"type":"object"}}],"thinking":{"type":"adaptive"}}`)
	inbound := NewTokenCountInboundTransformer()
	request, err := inbound.TransformRequest(ctx, &httpclient.Request{Body: body, Headers: http.Header{"Content-Type": {"application/json"}, "Anthropic-Beta": {"count-test"}}})
	require.NoError(t, err)
	require.Equal(t, llm.RequestTypeTokenCount, request.RequestType)
	require.Nil(t, request.MaxTokens)
	request.Model = "claude-native"
	outbound, err := NewOutboundTransformer("https://provider.example/v1", "synthetic-test-key")
	require.NoError(t, err)
	raw, err := outbound.TransformRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "https://provider.example/v1/messages/count_tokens", raw.URL)
	require.Equal(t, "claude-native", gjson.GetBytes(raw.Body, "model").String())
	for _, field := range []string{"messages", "system", "tools", "thinking"} {
		require.JSONEq(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(raw.Body, field).Raw)
	}
	require.Equal(t, "alias", gjson.GetBytes(body, "model").String())
	require.Equal(t, "count-test", raw.Headers.Get("Anthropic-Beta"))
	require.Equal(t, "synthetic-test-key", raw.Auth.APIKey)
	response, err := outbound.TransformResponse(ctx, &httpclient.Response{StatusCode: 200, Request: raw, Body: []byte(`{"input_tokens":137}`)})
	require.NoError(t, err)
	require.Nil(t, response.Usage, "a count is not billable generation usage")
	result, err := inbound.TransformResponse(ctx, response)
	require.NoError(t, err)
	require.JSONEq(t, `{"input_tokens":137}`, string(result.Body))
}

func TestTokenCountInvalidInputAndResponse(t *testing.T) {
	inbound := NewTokenCountInboundTransformer()
	for _, body := range []string{`null`, `{}`, `{"model":"m","messages":[]}`, `{"model":"m","messages":[{"role":"user","content":"x"}],"stream":true}`} {
		_, err := inbound.TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body)})
		require.ErrorIs(t, err, transformer.ErrInvalidRequest)
	}
	for _, body := range []string{`{}`, `{"input_tokens":-1}`, `{"input_tokens":1.5}`, `{"input_tokens":"2"}`, `{"input_tokens":null}`} {
		_, err := transformTokenCountResponse(&httpclient.Response{Body: []byte(body)})
		require.Error(t, err)
	}
	response, err := transformTokenCountResponse(&httpclient.Response{Body: []byte(`{"input_tokens":0}`)})
	require.NoError(t, err)
	require.Equal(t, int64(0), response.TokenCount.InputTokens)
}

func TestTokenCountCapability(t *testing.T) {
	require.Equal(t, map[string]struct{}{llm.APIFormatAnthropicMessage.String(): {}}, llm.CapableAPIFormats(llm.RequestTypeTokenCount))
	outbound, err := NewOutboundTransformerWithConfig(&Config{Type: PlatformBedrock, BaseURL: "https://bedrock.example"})
	require.NoError(t, err)
	_, err = outbound.TransformRequest(t.Context(), &llm.Request{RequestType: llm.RequestTypeTokenCount, Model: "m", TokenCount: &llm.TokenCountRequest{Body: []byte(`{"model":"m"}`)}})
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
}
