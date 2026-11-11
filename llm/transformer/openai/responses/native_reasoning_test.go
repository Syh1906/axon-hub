package responses

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/deepseek"
)

func TestNativeReasoningRequestRoundTrip(t *testing.T) {
	// Synthetic equivalent of #85507: native plaintext, opaque provider data,
	// tool history and server-side search items must keep their relative order.
	const input = `[
		{"type":"reasoning","id":"r1","summary":[],"content":[{"type":"reasoning_text","text":"first"}],"encrypted_content":"opaque-native-1"},
		{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"result"},
		{"type":"reasoning","id":"r2","summary":[{"type":"summary_text","text":"brief"}],"content":[{"type":"reasoning_text","text":"second "},{"type":"reasoning_text","text":"part"}]},
		{"type":"web_search_call","id":"s1","status":"completed","action":{"type":"search","query":"example"}},
		{"type":"reasoning","id":"r3","summary":[],"content":[]},
		{"type":"function_call","call_id":"c2","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"c2","output":"result"}
	]`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"deepseek-v4-pro","input":` + input + `}`)})
	require.NoError(t, err)
	outbound, err := NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	wire, err := outbound.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	var payload struct {
		Input json.RawMessage `json:"input"`
	}
	require.NoError(t, json.Unmarshal(wire.Body, &payload))
	require.JSONEq(t, input, string(payload.Input))

	// Cross-protocol consumers receive full text, not summary+text or an empty
	// placeholder. They do not receive native Responses encrypted_content.
	chat, err := deepseek.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	chatWire, err := chat.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	var chatBody struct {
		Messages []struct {
			Role      string `json:"role"`
			Reasoning string `json:"reasoning_content"`
			Encrypted string `json:"encrypted_content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(chatWire.Body, &chatBody))
	var reasoning []string
	for _, msg := range chatBody.Messages {
		require.Empty(t, msg.Encrypted)
		if msg.Reasoning != "" {
			reasoning = append(reasoning, msg.Reasoning)
		}
	}
	require.Equal(t, []string{"first", "second part"}, reasoning)
}

func TestNativeReasoningDoesNotReclassifyForeignSignatures(t *testing.T) {
	for _, signature := range []string{"opaque-native", "anthropic-signature", "gemini-signature"} {
		// Generic/foreign reasoning has no native Responses provenance. Keep the
		// existing signature filter instead of allowing every opaque value.
		items := convertAssistantMessage(llm.Message{
			Role: "assistant", ReasoningContent: lo.ToPtr("foreign thought"),
			ReasoningSignature: lo.ToPtr(signature),
			ReasoningItems:     []llm.ReasoningItem{{Content: "foreign thought", Signature: signature}},
		})
		require.Empty(t, items)
	}
}

func TestNativeReasoningResponseRoundTrip(t *testing.T) {
	for _, item := range []string{
		`{"id":"r1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"full"}],"encrypted_content":"opaque-native"}`,
		`{"id":"r1","type":"reasoning","summary":[{"type":"summary_text","text":"brief"},{"type":"summary_text","text":"summary"}],"content":[{"type":"reasoning_text","text":"full"},{"type":"reasoning_text","text":" text"}],"status":"completed"}`,
		`{"id":"r1","type":"reasoning","summary":[],"content":[]}`,
		`{"id":"r1","type":"reasoning","summary":[],"encrypted_content":"opaque-native"}`,
	} {
		t.Run(item, func(t *testing.T) {
			outbound, err := NewOutboundTransformer("https://example.invalid", "synthetic")
			require.NoError(t, err)
			unified, err := outbound.TransformResponse(t.Context(), &httpclient.Response{Body: []byte(`{"id":"resp_native","object":"response","model":"deepseek-v4-pro","status":"completed","output":[` + item + `]}`)})
			require.NoError(t, err)
			// Continuation storage must retain the distinction after serialization.
			stored, err := json.Marshal(unified)
			require.NoError(t, err)
			var restored llm.Response
			require.NoError(t, json.Unmarshal(stored, &restored))
			wire, err := NewInboundTransformer().TransformResponse(t.Context(), &restored)
			require.NoError(t, err)
			var response struct {
				Output []json.RawMessage `json:"output"`
			}
			require.NoError(t, json.Unmarshal(wire.Body, &response))
			require.Len(t, response.Output, 1)
			require.JSONEq(t, item, string(response.Output[0]))
		})
	}
}
