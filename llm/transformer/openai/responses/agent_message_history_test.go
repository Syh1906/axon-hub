package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

func TestNormalizeAgentMessageHistory(t *testing.T) {
	text := "已经检查完成。Keep every byte, including\nnewlines and quotes: \"ok\"."
	for _, tc := range []struct {
		name, marker, namespace, tool, payload string
		before, want                           bool
	}{
		{"explicit_plaintext", "[]", "collaboration", "send_message", text, true, true},
		{"spawn", "[]", "collaboration", "spawn_agent", text, true, true},
		{"followup", "[]", "collaboration", "followup_task", text, true, true},
		{"missing_marker", "", "collaboration", "send_message", text, true, false},
		{"null_marker", "null", "collaboration", "send_message", text, true, false},
		{"encrypted_marker", `["message"]`, "collaboration", "send_message", text, true, false},
		{"other_namespace", "[]", "functions", "send_message", text, true, false},
		{"other_tool", "[]", "collaboration", "wait_agent", text, true, false},
		{"different_bytes", "[]", "collaboration", "send_message", text + " ", true, false},
		{"ciphertext", "[]", "collaboration", "send_message", "gAAAA_fixture", true, false},
		{"unknown_format", "[]", "collaboration", "send_message", "enc:opaque-v2", true, false},
		{"later_call_not_evidence", "[]", "collaboration", "send_message", text, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"message": text, "target": "worker", "task_name": "worker"})
			require.NoError(t, err)
			call := map[string]any{"type": "function_call", "call_id": "call_history", "namespace": tc.namespace, "name": tc.tool, "arguments": string(args)}
			if tc.marker != "" {
				call["encrypted_function_args"] = json.RawMessage(tc.marker)
			}
			agent := map[string]any{"id": "amsg_history", "type": "agent_message", "author": "/root", "recipient": "/root/worker",
				"custom_metadata": map[string]any{"preserve": true},
				"content": []any{
					map[string]any{"type": "input_text", "text": "Message header\n"},
					map[string]any{"type": "encrypted_content", "encrypted_content": tc.payload, "annotation": "keep"},
					map[string]any{"type": "encrypted_content", "encrypted_content": "opaque-content-unchanged"},
				}}
			items := []any{call, agent}
			index := "1"
			if !tc.before {
				items[0], items[1], index = agent, call, "0"
			}
			body, err := json.Marshal(map[string]any{"model": "test", "input": items})
			require.NoError(t, err)
			original := string(body)
			actual, err := NormalizeAgentMessageHistory(body)
			require.NoError(t, err)
			require.Equal(t, original, string(body))
			if !tc.want {
				require.Equal(t, body, actual)
				return
			}
			path := "input." + index
			require.Equal(t, "agent_message", gjson.GetBytes(actual, path+".type").String())
			require.Equal(t, "amsg_history", gjson.GetBytes(actual, path+".id").String())
			require.Equal(t, "/root", gjson.GetBytes(actual, path+".author").String())
			require.Equal(t, "/root/worker", gjson.GetBytes(actual, path+".recipient").String())
			require.True(t, gjson.GetBytes(actual, path+".custom_metadata.preserve").Bool())
			require.Equal(t, "input_text", gjson.GetBytes(actual, path+".content.1.type").String())
			require.Equal(t, text, gjson.GetBytes(actual, path+".content.1.text").String())
			require.False(t, gjson.GetBytes(actual, path+".content.1.encrypted_content").Exists())
			require.Equal(t, "keep", gjson.GetBytes(actual, path+".content.1.annotation").String())
			require.Equal(t, "opaque-content-unchanged", gjson.GetBytes(actual, path+".content.2.encrypted_content").String())
			again, err := NormalizeAgentMessageHistory(actual)
			require.NoError(t, err)
			require.Equal(t, actual, again)
		})
	}
}

func TestAgentMessageHistoryTransformRoundTrip(t *testing.T) {
	body := []byte(`{"model":"test","input":[
		{"type":"function_call","call_id":"call_history","namespace":"collaboration","name":"send_message","arguments":"{\"target\":\"worker\",\"message\":\"Historical task\"}","encrypted_function_args":[]},
		{"type":"function_call_output","call_id":"call_history","output":"ok"},
		{"type":"agent_message","id":"amsg_history","author":"/root","recipient":"/root/worker","content":[{"type":"encrypted_content","encrypted_content":"Historical task"}]}
	]}`)
	for _, tc := range []struct {
		name    string
		inbound transformer.Inbound
	}{{"responses", NewInboundTransformer()}, {"compact", NewCompactInboundTransformer()}} {
		t.Run(tc.name, func(t *testing.T) {
			unified, err := tc.inbound.TransformRequest(t.Context(), &httpclient.Request{Body: body})
			require.NoError(t, err)
			outbound, err := NewOutboundTransformer("https://example.invalid/v1", "unused")
			require.NoError(t, err)
			wire, err := outbound.TransformRequest(t.Context(), unified)
			require.NoError(t, err)
			require.Equal(t, "Historical task", gjson.GetBytes(wire.Body, "input.2.content.0.text").String())
			require.Equal(t, "[]", gjson.GetBytes(wire.Body, "input.0.encrypted_function_args").Raw)
			// Raw extension fragments belong to the original request and may be reused.
			require.Equal(t, "Historical task", gjson.GetBytes(unified.ProviderExtensions.OpenAIResponses.Request.RawInputItems[0].Raw, "content.0.encrypted_content").String())
		})
	}
}

func TestAgentMessageHistoryMatchesRecipient(t *testing.T) {
	for _, tc := range []struct {
		target, author, recipient string
		want                      bool
	}{
		{"/root/worker", "/root", "/root/worker", true},
		{"worker", "/root", "/root/worker", true},
		{"worker", "/root", "/root/another", false},
		{"opaque-agent-id", "/root", "/root/worker", false},
		{"", "/root", "/root/worker", false},
		{"worker", "", "/root/worker", false},
	} {
		t.Run(tc.target+"_to_"+tc.recipient, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"target": tc.target, "message": "identical text"})
			require.NoError(t, err)
			body, err := json.Marshal(map[string]any{"input": []any{
				map[string]any{"type": "function_call", "namespace": "collaboration", "name": "send_message", "arguments": string(args), "encrypted_function_args": []string{}},
				map[string]any{"type": "agent_message", "author": tc.author, "recipient": tc.recipient, "content": []any{
					map[string]any{"type": "encrypted_content", "encrypted_content": "identical text"},
				}},
			}})
			require.NoError(t, err)
			actual, err := NormalizeAgentMessageHistory(body)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(actual, "input.1.content.0.text").Exists())
		})
	}
}

func TestAgentMessageHistoryKeepsOrderAroundInstructions(t *testing.T) {
	body := []byte(`{"model":"test","input":[
		{"type":"message","role":"system","content":"First instruction"},
		{"type":"reasoning","id":"rs_unsigned","summary":[]},
		{"type":"function_call","call_id":"call_history","namespace":"collaboration","name":"send_message","arguments":"{\"target\":\"worker\",\"message\":\"Historical task\"}","encrypted_function_args":[]},
		{"type":"agent_message","id":"amsg_history","author":"/root","recipient":"/root/worker","content":[{"type":"encrypted_content","encrypted_content":"Historical task"}]},
		{"role":"system","content":"Second instruction"},
		{"type":"reasoning","id":"rs_unsigned_second","summary":[]},
		{"type":"agent_message","id":"amsg_ciphertext","author":"/root/other","recipient":"/root","content":[{"type":"encrypted_content","encrypted_content":"opaque-content-unchanged"}]},
		{"type":"function_call_output","call_id":"call_history","output":"ok"}
	]}`)
	for _, tc := range []struct {
		name    string
		inbound transformer.Inbound
	}{{"responses", NewInboundTransformer()}, {"compact", NewCompactInboundTransformer()}} {
		t.Run(tc.name, func(t *testing.T) {
			unified, err := tc.inbound.TransformRequest(t.Context(), &httpclient.Request{Body: body})
			require.NoError(t, err)
			outbound, err := NewOutboundTransformer("https://example.invalid/v1", "unused")
			require.NoError(t, err)
			wire, err := outbound.TransformRequest(t.Context(), unified)
			require.NoError(t, err)
			require.Len(t, gjson.GetBytes(wire.Body, "input").Array(), 4)
			require.Equal(t, "function_call", gjson.GetBytes(wire.Body, "input.0.type").String())
			require.Equal(t, "amsg_history", gjson.GetBytes(wire.Body, "input.1.id").String())
			require.Equal(t, "Historical task", gjson.GetBytes(wire.Body, "input.1.content.0.text").String())
			require.Equal(t, "amsg_ciphertext", gjson.GetBytes(wire.Body, "input.2.id").String())
			require.Equal(t, "opaque-content-unchanged", gjson.GetBytes(wire.Body, "input.2.content.0.encrypted_content").String())
			require.Equal(t, "function_call_output", gjson.GetBytes(wire.Body, "input.3.type").String())
			if tc.name == "responses" {
				require.Equal(t, "First instruction\nSecond instruction", gjson.GetBytes(wire.Body, "instructions").String())
			}
		})
	}
}
