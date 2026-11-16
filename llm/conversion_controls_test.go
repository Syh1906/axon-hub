package llm_test

import (
	"encoding/json"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestConversionRejectsCrossProtocolLogprobs(t *testing.T) {
	chat, err := openai.NewOutboundTransformer("https://unused.invalid", "fixture")
	require.NoError(t, err)
	resp, err := responses.NewOutboundTransformer("https://unused.invalid", "fixture")
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		body      string
		inbound   transformer.Inbound
		native    transformer.Outbound
		converted transformer.Outbound
	}{
		{"chat flag", `{"model":"m","messages":[{"role":"user","content":"hi"}],"logprobs":true}`, openai.NewInboundTransformer(), chat, resp},
		{"chat top probabilities", `{"model":"m","messages":[{"role":"user","content":"hi"}],"logprobs":true,"top_logprobs":5}`, openai.NewInboundTransformer(), chat, resp},
		{"responses include only", `{"model":"m","input":"hi","include":["message.output_text.logprobs"]}`, responses.NewInboundTransformer(), resp, chat},
		{"responses top probabilities", `{"model":"m","input":"hi","top_logprobs":5}`, responses.NewInboundTransformer(), resp, chat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := tc.inbound.TransformRequest(t.Context(), &httpclient.Request{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(tc.body)})
			require.NoError(t, err)
			require.True(t, llm.RequiresLogprobs(req))
			_, err = tc.native.TransformRequest(t.Context(), req)
			require.NoError(t, err)
			_, err = tc.converted.TransformRequest(t.Context(), req)
			require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
			data, err := json.Marshal(req)
			require.NoError(t, err)
			var restored llm.Request
			require.NoError(t, json.Unmarshal(data, &restored))
			require.True(t, llm.RequiresLogprobs(&restored))
		})
	}
}

func TestConversionRejectsLostResponseControls(t *testing.T) {
	chat, err := openai.NewOutboundTransformer("https://unused.invalid", "fixture")
	require.NoError(t, err)
	resp, err := responses.NewOutboundTransformer("https://unused.invalid", "fixture")
	require.NoError(t, err)
	claude, err := anthropic.NewOutboundTransformer("https://unused.invalid", "fixture")
	require.NoError(t, err)
	req := &llm.Request{Model: "m", RequestType: llm.RequestTypeChat, Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hello")}}}, Stop: &llm.Stop{MultipleStop: []string{"END"}}}
	require.True(t, llm.HasConversionRequirements(req))
	_, err = resp.TransformRequest(t.Context(), req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	_, err = chat.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	_, err = claude.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	req.Stop = nil
	req.Logprobs = lo.ToPtr(true)
	_, err = claude.TransformRequest(t.Context(), req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	_, err = chat.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	req.Logprobs = lo.ToPtr(false)
	require.False(t, llm.HasConversionRequirements(req))
}

func TestChatDoesNotSilentlyLoseMultipleChoices(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`,"n":2`, false}, {`,"n":0`, false}, {`,"n":1`, true}, {``, true}} {
		_, err := openai.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]` + tc.body + `}`)})
		if tc.valid {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, transformer.ErrInvalidRequest)
		}
	}
}
