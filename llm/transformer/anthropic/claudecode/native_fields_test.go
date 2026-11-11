package claudecode

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

func TestNativeThinkingBindingBetaSurvivesClaudeCodeHeaders(t *testing.T) {
	const body = `{"model":"claude-sonnet-5-5","max_tokens":2048,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}},"tools":[{"type":"web_search_20250305","name":"web_search"}]}`
	req, err := anthropic.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	out, err := NewOutboundTransformer(Params{TokenProvider: newMockTokenProvider("synthetic-token")})
	require.NoError(t, err)
	wire, err := out.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "drop_block", gjson.GetBytes(wire.Body, "thinking.block_binding.prefix_mismatch_behavior").String())
	betas := strings.Split(strings.Join(wire.Headers.Values("Anthropic-Beta"), ","), ",")
	require.Contains(t, betas, "thinking-binding-controls-2026-08-01")
	for _, beta := range strings.Split(ClaudeCodeBetaHeader, ",") {
		require.Contains(t, betas, beta)
	}
}

func TestNativeToolChoiceKeepsDeclaredName(t *testing.T) {
	for _, toolName := range []string{"code_execution", "lookup"} {
		t.Run(toolName, func(t *testing.T) {
			body := `{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"code_execution_20250825","name":"code_execution"},{"name":"lookup","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"` + toolName + `"}}`
			req, err := anthropic.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
			require.NoError(t, err)
			out, err := NewOutboundTransformer(Params{TokenProvider: newMockTokenProvider("synthetic-token"), IsOfficial: true})
			require.NoError(t, err)
			wire, err := out.TransformRequest(t.Context(), req)
			require.NoError(t, err)
			want := toolName
			if toolName == "lookup" {
				want = toolPrefix + toolName
			}
			require.Equal(t, want, gjson.GetBytes(wire.Body, "tool_choice.name").String())
			require.Equal(t, "code_execution", gjson.GetBytes(wire.Body, "tools.0.name").String())
			require.Equal(t, toolPrefix+"lookup", gjson.GetBytes(wire.Body, "tools.1.name").String())
			require.Equal(t, toolName, req.ToolChoice.NamedToolChoice.Function.Name)
		})
	}
}
