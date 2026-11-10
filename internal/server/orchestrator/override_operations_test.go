package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestOverrideOperationsNewFormat(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model:           "claude-3.5-sonnet",
		ReasoningEffort: "high",
		Metadata: map[string]string{
			"user_id": "u-42",
		},
	}

	t.Run("set and delete operations", func(t *testing.T) {
		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "ops-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "set", "path": "temperature", "value": "0.7"},
						{"op": "set", "path": "custom", "value": "model-{{.Model}}"},
						{"op": "delete", "path": "frequency_penalty"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"frequency_penalty": 0.5, "existing": "keep"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.Equal(t, 0.7, gjson.Get(bodyStr, "temperature").Float())
		require.Equal(t, "model-claude-3.5-sonnet", gjson.Get(bodyStr, "custom").String())
		require.False(t, gjson.Get(bodyStr, "frequency_penalty").Exists())
		require.Equal(t, "keep", gjson.Get(bodyStr, "existing").String())
	})

	t.Run("rename operation", func(t *testing.T) {
		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "rename-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "rename", "from": "max_tokens", "to": "max_completion_tokens"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"max_tokens": 4096, "model": "test"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.False(t, gjson.Get(bodyStr, "max_tokens").Exists())
		require.Equal(t, int64(4096), gjson.Get(bodyStr, "max_completion_tokens").Int())
		require.Equal(t, "test", gjson.Get(bodyStr, "model").String())
	})

	t.Run("copy operation", func(t *testing.T) {
		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "copy-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "copy", "from": "model", "to": "metadata.original_model"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"model": "gpt-4"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.Equal(t, "gpt-4", gjson.Get(bodyStr, "model").String())
		require.Equal(t, "gpt-4", gjson.Get(bodyStr, "metadata.original_model").String())
	})

	t.Run("rename non-existent field is no-op", func(t *testing.T) {
		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "rename-noop-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "rename", "from": "nonexistent", "to": "target"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"model": "gpt-4"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.Equal(t, "gpt-4", gjson.Get(bodyStr, "model").String())
		require.False(t, gjson.Get(bodyStr, "target").Exists())
	})
}

func TestOverrideOperationsWithCondition(t *testing.T) {
	ctx := context.Background()

	t.Run("condition true executes operation", func(t *testing.T) {
		llmRequest := &llm.Request{
			Model:           "claude-3.5-sonnet",
			ReasoningEffort: "high",
		}

		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "cond-true-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "delete", "path": "reasoning_effort", "condition": "{{if eq .ReasoningEffort \"high\"}}true{{end}}"},
						{"op": "set", "path": "thinking.type", "value": "enabled", "condition": "{{if .ReasoningEffort}}true{{end}}"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"reasoning_effort": "high", "model": "test"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.False(t, gjson.Get(bodyStr, "reasoning_effort").Exists())
		require.Equal(t, "enabled", gjson.Get(bodyStr, "thinking.type").String())
	})

	t.Run("condition false skips operation", func(t *testing.T) {
		llmRequest := &llm.Request{
			Model:           "gpt-4",
			ReasoningEffort: "low",
		}

		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "cond-false-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "delete", "path": "reasoning_effort", "condition": "{{if eq .ReasoningEffort \"high\"}}true{{end}}"},
						{"op": "rename", "from": "max_tokens", "to": "max_output_tokens", "condition": "{{if eq .Model \"claude-3.5-sonnet\"}}true{{end}}"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "gpt-4",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"reasoning_effort": "low", "max_tokens": 1000}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.Equal(t, "low", gjson.Get(bodyStr, "reasoning_effort").String())
		require.Equal(t, int64(1000), gjson.Get(bodyStr, "max_tokens").Int())
		require.False(t, gjson.Get(bodyStr, "max_output_tokens").Exists())
	})

	t.Run("conditional rename with reasoning effort mapping", func(t *testing.T) {
		llmRequest := &llm.Request{
			Model:           "claude-3.5-sonnet",
			ReasoningEffort: "high",
		}

		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "thinking-map-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "rename", "from": "reasoning_effort", "to": "thinking.budget_tokens", "condition": "{{if .ReasoningEffort}}true{{end}}"},
						{"op": "set", "path": "thinking.type", "value": "enabled", "condition": "{{if .ReasoningEffort}}true{{end}}"},
						{"op": "set", "path": "thinking.budget_tokens", "value": "{{if eq .ReasoningEffort \"high\"}}16384{{else if eq .ReasoningEffort \"medium\"}}8192{{else}}4096{{end}}", "condition": "{{if .ReasoningEffort}}true{{end}}"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "claude-3.5-sonnet",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"reasoning_effort": "high", "model": "test"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.False(t, gjson.Get(bodyStr, "reasoning_effort").Exists())
		require.Equal(t, "enabled", gjson.Get(bodyStr, "thinking.type").String())
		require.Equal(t, int64(16384), gjson.Get(bodyStr, "thinking.budget_tokens").Int())
	})

	t.Run("no condition means always execute", func(t *testing.T) {
		llmRequest := &llm.Request{
			Model: "gpt-4",
		}

		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "no-cond-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: `[
						{"op": "set", "path": "temperature", "value": "0.5"},
						{"op": "delete", "path": "top_p"}
					]`,
				},
			},
			Outbound: &mockTransformer{},
		}

		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    "gpt-4",
			},
		}

		middleware := applyOverrideRequestBody(outbound)
		rawRequest := &httpclient.Request{
			Body: []byte(`{"top_p": 0.9, "model": "test"}`),
		}

		result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
		require.NoError(t, err)

		bodyStr := string(result.Body)
		require.Equal(t, 0.5, gjson.Get(bodyStr, "temperature").Float())
		require.False(t, gjson.Get(bodyStr, "top_p").Exists())
	})
}

func TestOverrideLegacyFormatCompatibility(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model:           "gpt-4",
		ReasoningEffort: "high",
	}

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "legacy-test",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{"temperature": 0.7, "max_tokens": 2000, "remove_me": "__AXONHUB_CLEAR__"}`,
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
			OriginalModel:    "gpt-4",
		},
	}

	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{
		Body: []byte(`{"remove_me": "old_value", "keep": "yes"}`),
	}

	result, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(result.Body)
	require.Equal(t, 0.7, gjson.Get(bodyStr, "temperature").Float())
	require.Equal(t, int64(2000), gjson.Get(bodyStr, "max_tokens").Int())
	require.False(t, gjson.Get(bodyStr, "remove_me").Exists())
	require.Equal(t, "yes", gjson.Get(bodyStr, "keep").String())
}

func TestOverrideBodySetIfAbsent(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		body     string
		op       objects.OverrideOperation
		metadata map[string]string
		expected string
	}{
		{
			name:     "sets top-level default when path is absent",
			body:     `{"model":"gpt-5.5"}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000"},
			expected: `{"model":"gpt-5.5","max_output_tokens":32000}`,
		},
		{
			name:     "preserves existing number",
			body:     `{"max_output_tokens":8000}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000"},
			expected: `{"max_output_tokens":8000}`,
		},
		{
			name:     "preserves zero",
			body:     `{"max_output_tokens":0}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000"},
			expected: `{"max_output_tokens":0}`,
		},
		{
			name:     "preserves false",
			body:     `{"feature":{"enabled":false}}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "feature.enabled", Value: "true"},
			expected: `{"feature":{"enabled":false}}`,
		},
		{
			name:     "preserves empty string",
			body:     `{"label":""}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "label", Value: "fallback"},
			expected: `{"label":""}`,
		},
		{
			name:     "preserves explicit null",
			body:     `{"max_output_tokens":null}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000"},
			expected: `{"max_output_tokens":null}`,
		},
		{
			name:     "sets nested default when path is absent",
			body:     `{"generation":{}}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "generation.max_output_tokens", Value: "32000"},
			expected: `{"generation":{"max_output_tokens":32000}}`,
		},
		{
			name:     "preserves existing nested value",
			body:     `{"generation":{"max_output_tokens":16000}}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "generation.max_output_tokens", Value: "32000"},
			expected: `{"generation":{"max_output_tokens":16000}}`,
		},
		{
			name:     "renders template when path is absent",
			body:     `{}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: `{{index .Metadata "default_max"}}`},
			metadata: map[string]string{"default_max": "64000"},
			expected: `{"max_output_tokens":64000}`,
		},
		{
			name:     "respects false condition",
			body:     `{}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000", Condition: `{{eq .Model "other-model"}}`},
			expected: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmRequest := &llm.Request{Model: "gpt-5.5", Metadata: tt.metadata}
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "set-if-absent-test",
					Settings: &objects.ChannelSettings{
						BodyOverrideOperations: []objects.OverrideOperation{tt.op},
					},
				},
				Outbound: &mockTransformer{},
			}
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       llmRequest,
					OriginalModel:    llmRequest.Model,
				},
			}

			middleware := applyOverrideRequestBody(outbound)
			result, err := middleware.OnOutboundRawRequest(ctx, &httpclient.Request{Body: []byte(tt.body)})
			require.NoError(t, err)
			require.JSONEq(t, tt.expected, string(result.Body))
		})
	}
}

// TestOverrideBodySetIfAbsentOriginalRequest covers https://github.com/looplj/axonhub/issues/2133:
// the outbound body is produced by format transformation, so a field the client sent may not
// survive it (e.g. Anthropic `thinking` mapped to `reasoning_effort`). set_if_absent must treat
// a field present in the original inbound request body as present.
func TestOverrideBodySetIfAbsentOriginalRequest(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		originalBody string
		body         string
		op           objects.OverrideOperation
		expected     string
	}{
		{
			name:         "preserves field sent by client but dropped by transformation",
			originalBody: `{"model":"deepseek-v4-flash","thinking":{"type":"enabled","budget_tokens":13312}}`,
			body:         `{"model":"deepseek-v4-flash","reasoning_effort":"high"}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "thinking", Value: `{"type":"disabled"}`},
			expected:     `{"model":"deepseek-v4-flash","reasoning_effort":"high"}`,
		},
		{
			name:         "preserves explicit null sent by client",
			originalBody: `{"thinking":null}`,
			body:         `{}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "thinking", Value: `{"type":"disabled"}`},
			expected:     `{}`,
		},
		{
			name:         "preserves nested field sent by client",
			originalBody: `{"generation":{"max_output_tokens":16000}}`,
			body:         `{}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "generation.max_output_tokens", Value: "32000"},
			expected:     `{}`,
		},
		{
			name:         "sets default when absent from both bodies",
			originalBody: `{"model":"deepseek-v4-flash"}`,
			body:         `{"model":"deepseek-v4-flash","reasoning_effort":"high"}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "thinking", Value: `{"type":"disabled"}`},
			expected:     `{"model":"deepseek-v4-flash","reasoning_effort":"high","thinking":{"type":"disabled"}}`,
		},
		{
			name:         "preserves field present only in transformed body",
			originalBody: `{}`,
			body:         `{"max_output_tokens":8000}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "max_output_tokens", Value: "32000"},
			expected:     `{"max_output_tokens":8000}`,
		},
		{
			name:         "ignores non-JSON original body",
			originalBody: `not-json`,
			body:         `{}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "thinking", Value: `{"type":"disabled"}`},
			expected:     `{"thinking":{"type":"disabled"}}`,
		},
		{
			name:         "sets default when no original request is recorded",
			originalBody: "",
			body:         `{}`,
			op:           objects.OverrideOperation{Op: objects.OverrideOpSetIfAbsent, Path: "thinking", Value: `{"type":"disabled"}`},
			expected:     `{"thinking":{"type":"disabled"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmRequest := &llm.Request{Model: "deepseek-v4-flash"}
			if tt.originalBody != "" {
				llmRequest.RawRequest = &httpclient.Request{Body: []byte(tt.originalBody)}
			}

			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "set-if-absent-original-test",
					Settings: &objects.ChannelSettings{
						BodyOverrideOperations: []objects.OverrideOperation{tt.op},
					},
				},
				Outbound: &mockTransformer{},
			}
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       llmRequest,
					OriginalModel:    llmRequest.Model,
				},
			}

			middleware := applyOverrideRequestBody(outbound)
			result, err := middleware.OnOutboundRawRequest(ctx, &httpclient.Request{Body: []byte(tt.body)})
			require.NoError(t, err)
			require.JSONEq(t, tt.expected, string(result.Body))
		})
	}
}

func TestParseOverrideOperations(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		ops, err := objects.ParseOverrideOperations("")
		require.NoError(t, err)
		require.Nil(t, ops)
	})

	t.Run("empty object", func(t *testing.T) {
		ops, err := objects.ParseOverrideOperations("{}")
		require.NoError(t, err)
		require.Nil(t, ops)
	})

	t.Run("empty array", func(t *testing.T) {
		ops, err := objects.ParseOverrideOperations("[]")
		require.NoError(t, err)
		require.Nil(t, ops)
	})

	t.Run("new format array", func(t *testing.T) {
		ops, err := objects.ParseOverrideOperations(`[
			{"op": "set", "path": "temp", "value": "0.7"},
			{"op": "delete", "path": "top_p"},
			{"op": "rename", "from": "a", "to": "b", "condition": "{{if .Model}}true{{end}}"}
		]`)
		require.NoError(t, err)
		require.Len(t, ops, 3)
		require.Equal(t, objects.OverrideOpSet, ops[0].Op)
		require.Equal(t, "temp", ops[0].Path)
		require.Equal(t, objects.OverrideOpDelete, ops[1].Op)
		require.Equal(t, objects.OverrideOpRename, ops[2].Op)
		require.Equal(t, "a", ops[2].From)
		require.Equal(t, "b", ops[2].To)
		require.NotEmpty(t, ops[2].Condition)
	})

	t.Run("legacy map format", func(t *testing.T) {
		ops, err := objects.ParseOverrideOperations(`{"temperature": 0.7, "remove": "__AXONHUB_CLEAR__"}`)
		require.NoError(t, err)
		require.Len(t, ops, 2)

		setFound := false
		deleteFound := false

		for _, op := range ops {
			if op.Op == objects.OverrideOpSet && op.Path == "temperature" {
				setFound = true
			}

			if op.Op == objects.OverrideOpDelete && op.Path == "remove" {
				deleteFound = true
			}
		}

		require.True(t, setFound)
		require.True(t, deleteFound)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := objects.ParseOverrideOperations(`{invalid}`)
		require.Error(t, err)
	})
}

func TestIssue632Override(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		reasoningEffort string
		expectedEnabled bool
	}{
		{
			name:            "low effort should disable thinking",
			reasoningEffort: "low",
			expectedEnabled: false,
		},
		{
			name:            "high effort should enable thinking",
			reasoningEffort: "high",
			expectedEnabled: true,
		},
		{
			name:            "medium effort should enable thinking",
			reasoningEffort: "medium",
			expectedEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test request
			llmRequest := &llm.Request{
				Model:           "sglang-model",
				ReasoningEffort: tt.reasoningEffort,
			}

			// Override parameters to map ReasoningEffort to chat_template_kwargs
			// This matches the requirement from Issue #632
			overrideParams := map[string]string{
				"chat_template_kwargs": `{"enable_thinking": {{if eq .ReasoningEffort "low"}}false{{else}}true{{end}}}`,
			}
			overrideParamsJSON, _ := json.Marshal(overrideParams)

			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "sglang-channel",
					Settings: &objects.ChannelSettings{
						OverrideParameters: string(overrideParamsJSON),
					},
				},
				Outbound: &mockTransformer{},
			}

			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       llmRequest,
				},
			}

			middleware := applyOverrideRequestBody(outbound)
			rawRequest := &httpclient.Request{
				Body: []byte("{}"),
			}

			processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
			require.NoError(t, err)

			bodyStr := string(processedRequest.Body)
			// Verify chat_template_kwargs is set correctly as an object
			kwargs := gjson.Get(bodyStr, "chat_template_kwargs")
			require.True(t, kwargs.IsObject(), "chat_template_kwargs should be an object, got: %s", bodyStr)
			require.Equal(t, tt.expectedEnabled, kwargs.Get("enable_thinking").Bool(), "enable_thinking mismatch for effort %s", tt.reasoningEffort)
		})
	}
}

func TestOverrideOperationsArrayOps(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model: "claude-sonnet-4-6",
		Metadata: map[string]string{
			"session_id": "sess-9",
		},
	}

	runMiddleware := func(t *testing.T, ops string, body string) string {
		t.Helper()

		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:   1,
				Name: "array-test",
				Settings: &objects.ChannelSettings{
					OverrideParameters: ops,
				},
			},
			Outbound: &mockTransformer{},
		}
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest:       llmRequest,
				OriginalModel:    llmRequest.Model,
			},
		}

		middleware := applyOverrideRequestBody(outbound)

		result, err := middleware.OnOutboundRawRequest(ctx, &httpclient.Request{Body: []byte(body)})
		require.NoError(t, err)

		return string(result.Body)
	}

	t.Run("delete removes a field from every array item", func(t *testing.T) {
		ops := `[{"op":"delete","path":"input[*].internal_chat_message_metadata_passthrough"}]`
		body := `{"input":[{"role":"user","content":"hello","internal_chat_message_metadata_passthrough":{"id":1}},{"role":"assistant","content":"hi","internal_chat_message_metadata_passthrough":null},{"role":"user","content":"next"}],"model":"test"}`

		got := runMiddleware(t, ops, body)

		require.JSONEq(t, `{"input":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}],"model":"test"}`, got)
	})

	t.Run("delete traverses nested arrays and preserves other values", func(t *testing.T) {
		ops := `[{"op":"delete","path":"messages[*].content[*].cache_control"}]`
		body := `{"messages":[{"content":[{"text":"hello","cache_control":{"type":"ephemeral"}},{"text":"next","id":9007199254740993}]},{"content":"plain text"},{"content":[null,{"text":"last","cache_control":false}]}]}`

		got := runMiddleware(t, ops, body)

		require.JSONEq(t, `{"messages":[{"content":[{"text":"hello"},{"text":"next","id":9007199254740993}]},{"content":"plain text"},{"content":[null,{"text":"last"}]}]}`, got)
		require.Equal(t, "9007199254740993", gjson.Get(got, "messages.0.content.1.id").Raw)
	})

	t.Run("delete wildcard leaves missing and non-array paths unchanged", func(t *testing.T) {
		ops := `[{"op":"delete","path":"input[*].metadata"}]`
		for _, body := range []string{`{}`, `{"input":[]}`, `{"input":null}`, `{"input":{"metadata":"keep"}}`, `{"input":[null,7,"text",{}]}`} {
			require.JSONEq(t, body, runMiddleware(t, ops, body))
		}
	})

	t.Run("delete wildcard respects escaped path characters", func(t *testing.T) {
		ops := `[{"op":"delete","path":"message\\.list[*].meta\\.data"}]`
		body := `{"message.list":[{"meta.data":"remove","keep":true}],"message":{"list":[{"meta.data":"keep"}]}}`

		got := runMiddleware(t, ops, body)

		require.JSONEq(t, `{"message.list":[{"keep":true}],"message":{"list":[{"meta.data":"keep"}]}}`, got)
	})

	t.Run("delete preserves exact indexes and escaped literal wildcards", func(t *testing.T) {
		ops := `[{"op":"delete","path":"input.1.metadata"},{"op":"delete","path":"literal\\[\\*\\].metadata"}]`
		body := `{"input":[{"metadata":"keep"},{"metadata":"remove","keep":true}],"literal[*]":{"metadata":"remove","keep":false}}`

		got := runMiddleware(t, ops, body)

		require.JSONEq(t, `{"input":[{"metadata":"keep"},{"keep":true}],"literal[*]":{"keep":false}}`, got)
	})

	t.Run("delete wildcard removes all selected array elements", func(t *testing.T) {
		ops := `[{"op":"delete","path":"input[*]"}]`
		got := runMiddleware(t, ops, `{"input":[1,2,3],"keep":true}`)

		require.JSONEq(t, `{"input":[],"keep":true}`, got)
	})

	t.Run("delete wildcard preserves relative paths while rebuilding arrays", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			path string
			body string
			want string
		}{
			{
				name: "root array",
				path: "[*].metadata",
				body: `[{"metadata":true,"keep":9007199254740993},null,7]`,
				want: `[{"keep":9007199254740993},null,7]`,
			},
			{
				name: "adjacent wildcards",
				path: "input[*][*].metadata",
				body: `{"input":[[{"metadata":true,"keep":1},7],[{"metadata":null}],null]}`,
				want: `{"input":[[{"keep":1},7],[{}],null]}`,
			},
			{
				name: "remove nested items",
				path: "input[*][*]",
				body: `{"input":[[1,2],[3],null]}`,
				want: `{"input":[[],[],null]}`,
			},
			{
				name: "wildcard in an empty object key",
				path: "input[*].[*]",
				body: `{"input":[{"":[1,2],"keep":true}]}`,
				want: `{"input":[{"":[],"keep":true}]}`,
			},
			{
				name: "field wildcard in an empty object key",
				path: "input[*].[*].metadata",
				body: `{"input":[{"":[{"metadata":true,"keep":1}]}]}`,
				want: `{"input":[{"":[{"keep":1}]}]}`,
			},
			{
				name: "empty key wildcard does not select the array item itself",
				path: "input[*].[*]",
				body: `{"input":[[1,2],null]}`,
				want: `{"input":[[1,2],null]}`,
			},
			{
				name: "empty object key",
				path: "input[*].",
				body: `{"input":[{"":true,"keep":1},null,{"":2}]}`,
				want: `{"input":[{"keep":1},null,{}]}`,
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				ops, err := json.Marshal([]objects.OverrideOperation{{Op: objects.OverrideOpDelete, Path: tt.path}})
				require.NoError(t, err)
				got := runMiddleware(t, string(ops), tt.body)
				require.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("delete wildcard without matches preserves the original bytes", func(t *testing.T) {
		ops := `[{"op":"delete","path":"input[*].metadata"}]`
		body := `{ "input": [ { "keep": 1e+09 }, null, 7 ], "id": 9007199254740993 }`
		require.Equal(t, body, runMiddleware(t, ops, body))
	})

	t.Run("array_append inserts a single object at the end", func(t *testing.T) {
		ops := `[{"op":"array_append","path":"system","value":"{\"type\":\"text\",\"text\":\"injected\"}"}]`
		got := runMiddleware(t, ops, `{"system":[{"type":"text","text":"original"}]}`)

		require.Equal(t, "original", gjson.Get(got, "system.0.text").String())
		require.Equal(t, "injected", gjson.Get(got, "system.1.text").String())
		require.Equal(t, int64(2), gjson.Get(got, "system.#").Int())
	})

	t.Run("array_prepend inserts a single object at the start", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"system","value":"{\"type\":\"text\",\"text\":\"injected\"}"}]`
		got := runMiddleware(t, ops, `{"system":[{"type":"text","text":"original"}]}`)

		require.Equal(t, "injected", gjson.Get(got, "system.0.text").String())
		require.Equal(t, "original", gjson.Get(got, "system.1.text").String())
	})

	t.Run("array_prepend with array value spreads elements (splat default true)", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"system","value":"[{\"text\":\"a\"},{\"text\":\"b\"}]"}]`
		got := runMiddleware(t, ops, `{"system":[{"text":"original"}]}`)

		require.Equal(t, "a", gjson.Get(got, "system.0.text").String())
		require.Equal(t, "b", gjson.Get(got, "system.1.text").String())
		require.Equal(t, "original", gjson.Get(got, "system.2.text").String())
	})

	t.Run("array_prepend with splat=false inserts the array as a single element", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"tags","value":"[\"a\",\"b\"]","splat":false}]`
		got := runMiddleware(t, ops, `{"tags":["x"]}`)

		require.True(t, gjson.Get(got, "tags.0").IsArray())
		require.Equal(t, "a", gjson.Get(got, "tags.0.0").String())
		require.Equal(t, "b", gjson.Get(got, "tags.0.1").String())
		require.Equal(t, "x", gjson.Get(got, "tags.1").String())
	})

	t.Run("array_insert at positive index", func(t *testing.T) {
		ops := `[{"op":"array_insert","path":"items","index":1,"value":"X"}]`
		got := runMiddleware(t, ops, `{"items":["a","b","c"]}`)

		require.Equal(t, `["a","X","b","c"]`, gjson.Get(got, "items").Raw)
	})

	t.Run("array_insert with negative index counts from end", func(t *testing.T) {
		ops := `[{"op":"array_insert","path":"items","index":-1,"value":"X"}]`
		got := runMiddleware(t, ops, `{"items":["a","b","c"]}`)

		// -1 means "before last": result -> [a, b, X, c]
		require.Equal(t, `["a","b","X","c"]`, gjson.Get(got, "items").Raw)
	})

	t.Run("array_insert clamps out-of-range index", func(t *testing.T) {
		ops := `[{"op":"array_insert","path":"items","index":99,"value":"X"}]`
		got := runMiddleware(t, ops, `{"items":["a","b"]}`)

		require.Equal(t, `["a","b","X"]`, gjson.Get(got, "items").Raw)
	})

	t.Run("array_append on missing path creates new array", func(t *testing.T) {
		ops := `[{"op":"array_append","path":"system","value":"{\"type\":\"text\",\"text\":\"only\"}"}]`
		got := runMiddleware(t, ops, `{}`)

		require.Equal(t, "only", gjson.Get(got, "system.0.text").String())
		require.Equal(t, int64(1), gjson.Get(got, "system.#").Int())
	})

	t.Run("array_prepend on non-array path is a no-op with warning", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"system","value":"X"}]`
		got := runMiddleware(t, ops, `{"system":"not-an-array"}`)

		// On error the middleware logs a warning but continues with the unchanged body.
		require.Equal(t, "not-an-array", gjson.Get(got, "system").String())
	})

	t.Run("multiple array_prepend ops apply in declared order at index 0", func(t *testing.T) {
		// Two prepends: each runs against current state and inserts at index 0.
		// First op inserts B -> [B, original]; second op inserts A -> [A, B, original].
		ops := `[
			{"op":"array_prepend","path":"system","value":"{\"text\":\"B\"}"},
			{"op":"array_prepend","path":"system","value":"{\"text\":\"A\"}"}
		]`
		got := runMiddleware(t, ops, `{"system":[{"text":"original"}]}`)

		require.Equal(t, "A", gjson.Get(got, "system.0.text").String())
		require.Equal(t, "B", gjson.Get(got, "system.1.text").String())
		require.Equal(t, "original", gjson.Get(got, "system.2.text").String())
	})

	t.Run("array_prepend supports nested cache_control objects", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"system","value":"{\"type\":\"text\",\"text\":\"prefix\",\"cache_control\":{\"type\":\"ephemeral\"}}"}]`
		got := runMiddleware(t, ops, `{"system":[{"type":"text","text":"user"}]}`)

		require.Equal(t, "ephemeral", gjson.Get(got, "system.0.cache_control.type").String())
		require.Equal(t, "prefix", gjson.Get(got, "system.0.text").String())
		require.Equal(t, "user", gjson.Get(got, "system.1.text").String())
	})

	t.Run("array op respects condition", func(t *testing.T) {
		// Condition false: no change.
		ops := `[{"op":"array_append","path":"system","value":"X","condition":"{{eq .Model \"never\"}}"}]`
		got := runMiddleware(t, ops, `{"system":["a"]}`)
		require.Equal(t, `["a"]`, gjson.Get(got, "system").Raw)

		// Condition true: append happens.
		ops = `[{"op":"array_append","path":"system","value":"X","condition":"{{eq .Model \"claude-sonnet-4-6\"}}"}]`
		got = runMiddleware(t, ops, `{"system":["a"]}`)
		require.Equal(t, `["a","X"]`, gjson.Get(got, "system").Raw)
	})

	t.Run("array_prepend supports templated value", func(t *testing.T) {
		ops := `[{"op":"array_prepend","path":"system","value":"{\"type\":\"text\",\"text\":\"session-{{index .Metadata \"session_id\"}}\"}"}]`
		got := runMiddleware(t, ops, `{"system":[{"text":"u"}]}`)

		require.Equal(t, "session-sess-9", gjson.Get(got, "system.0.text").String())
	})

	t.Run("array_remove removes matching tool by nested name", func(t *testing.T) {
		ops := `[{
			"op":"array_remove",
			"path":"tools",
			"match":{"path":"function.name","eq":"web_search"}
		}]`
		got := runMiddleware(t, ops, `{
			"tools":[
				{"type":"function","function":{"name":"get_weather"}},
				{"type":"function","function":{"name":"web_search"}},
				{"type":"function","function":{"name":"calculate"}}
			]
		}`)

		require.Equal(t, int64(2), gjson.Get(got, "tools.#").Int())
		require.Equal(t, "get_weather", gjson.Get(got, "tools.0.function.name").String())
		require.Equal(t, "calculate", gjson.Get(got, "tools.1.function.name").String())
	})

	t.Run("array_remove keeps non-matching items", func(t *testing.T) {
		ops := `[{"op":"array_remove","path":"tools","match":{"path":"function.name","eq":"missing"}}]`
		got := runMiddleware(t, ops, `{"tools":[{"function":{"name":"web_search"}}]}`)

		require.Equal(t, int64(1), gjson.Get(got, "tools.#").Int())
		require.Equal(t, "web_search", gjson.Get(got, "tools.0.function.name").String())
	})

	t.Run("array_remove on non-array path is a no-op with warning", func(t *testing.T) {
		ops := `[{"op":"array_remove","path":"tools","match":{"path":"function.name","eq":"web_search"}}]`
		got := runMiddleware(t, ops, `{"tools":"not-an-array"}`)

		// On error the middleware logs a warning but continues with the unchanged body.
		require.Equal(t, "not-an-array", gjson.Get(got, "tools").String())
	})

	t.Run("array_remove on missing path is a no-op", func(t *testing.T) {
		ops := `[{"op":"array_remove","path":"tools","match":{"path":"function.name","eq":"web_search"}}]`
		got := runMiddleware(t, ops, `{"messages":[{"role":"user","content":"hello"}]}`)

		require.False(t, gjson.Get(got, "tools").Exists())
		require.Equal(t, "hello", gjson.Get(got, "messages.0.content").String())
	})

	t.Run("array_remove with empty match eq is a no-op with warning", func(t *testing.T) {
		ops := `[{"op":"array_remove","path":"tools","match":{"path":"function.name","eq":""}}]`
		got := runMiddleware(t, ops, `{"tools":[{"function":{"name":"web_search"}},{"function":{"name":""}}]}`)

		// Empty match.eq is rejected defensively at runtime so a malformed stored op cannot remove items accidentally.
		require.Equal(t, int64(2), gjson.Get(got, "tools.#").Int())
	})

	t.Run("array_remove trims match eq before comparing", func(t *testing.T) {
		ops := `[{"op":"array_remove","path":"tools","match":{"path":"function.name","eq":" web_search "}}]`
		got := runMiddleware(t, ops, `{"tools":[{"function":{"name":"web_search"}},{"function":{"name":"calculate"}}]}`)

		require.Equal(t, int64(1), gjson.Get(got, "tools.#").Int())
		require.Equal(t, "calculate", gjson.Get(got, "tools.0.function.name").String())
	})
}

// TestOverrideBodySkipsNonJSONBody guards the image edit regression: sjson rebuilds a
// non-JSON document from scratch, so applying body overrides to a multipart payload used
// to replace the whole form with a tiny JSON object while Content-Type still advertised
// the multipart boundary, producing a truncated upstream request.
func TestOverrideBodySkipsNonJSONBody(t *testing.T) {
	ctx := context.Background()

	multipartBody := []byte("--boundary123\r\n" +
		"Content-Disposition: form-data; name=\"image\"; filename=\"image_1.png\"\r\n" +
		"Content-Type: image/png\r\n\r\n" +
		"\x89PNG\r\n\x1a\nbinary-image-bytes\r\n" +
		"--boundary123\r\n" +
		"Content-Disposition: form-data; name=\"prompt\"\r\n\r\n" +
		"make it blue\r\n" +
		"--boundary123--\r\n")

	newOutbound := func() *PersistentOutboundTransformer {
		channel := &biz.Channel{
			Channel: &ent.Channel{
				ID:              1,
				Name:            "test-channel",
				SupportedModels: []string{"gpt-image-1"},
				Settings: &objects.ChannelSettings{
					BodyOverrideOperations: []objects.OverrideOperation{
						{Op: objects.OverrideOpSet, Path: "response_format", Value: "b64_json"},
					},
				},
			},
			Outbound: &mockTransformer{},
		}

		return &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
				CurrentCandidateIndex: 0,
				CurrentModelIndex:     0,
				LlmRequest:            &llm.Request{Model: "gpt-image-1"},
			},
		}
	}

	t.Run("multipart body is left untouched", func(t *testing.T) {
		headers := make(http.Header)
		headers.Set("Content-Type", "multipart/form-data; boundary=boundary123")

		request := &httpclient.Request{
			Method:      "POST",
			URL:         "https://api.example.com/v1/images/edits",
			Headers:     headers,
			ContentType: "multipart/form-data; boundary=boundary123",
			Body:        multipartBody,
		}

		modified, err := applyOverrideRequestBody(newOutbound()).OnOutboundRawRequest(ctx, request)
		require.NoError(t, err)
		require.Equal(t, multipartBody, modified.Body)
	})

	t.Run("multipart body without explicit content type is left untouched", func(t *testing.T) {
		request := &httpclient.Request{
			Method: "POST",
			URL:    "https://api.example.com/v1/images/edits",
			Body:   multipartBody,
		}

		modified, err := applyOverrideRequestBody(newOutbound()).OnOutboundRawRequest(ctx, request)
		require.NoError(t, err)
		require.Equal(t, multipartBody, modified.Body)
	})

	t.Run("json body still gets overridden", func(t *testing.T) {
		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")

		request := &httpclient.Request{
			Method:      "POST",
			URL:         "https://api.example.com/v1/images/generations",
			Headers:     headers,
			ContentType: "application/json",
			Body:        []byte(`{"model":"gpt-image-1","prompt":"a cat"}`),
		}

		modified, err := applyOverrideRequestBody(newOutbound()).OnOutboundRawRequest(ctx, request)
		require.NoError(t, err)
		require.Equal(t, "b64_json", gjson.GetBytes(modified.Body, "response_format").String())
		require.Equal(t, "a cat", gjson.GetBytes(modified.Body, "prompt").String())
	})
}

func TestOverrideDeleteWildcardAllocationBudget(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			Settings: &objects.ChannelSettings{
				OverrideParameters: `[{"op":"delete","path":"input[*].metadata"}]`,
			},
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       &llm.Request{Model: "test"},
		},
	}
	middleware := applyOverrideRequestBody(outbound)
	items := strings.TrimSuffix(strings.Repeat(`{"metadata":true,"keep":1},`, 128), ",")
	body := []byte(`{"input":[` + items + `],"padding":"` + strings.Repeat("x", 256*1024) + `"}`)
	var got []byte

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			request, err := middleware.OnOutboundRawRequest(ctx, &httpclient.Request{Body: body})
			if err != nil {
				b.Fatal(err)
			}
			got = request.Body
		}
	})

	require.Equal(t, int64(128), gjson.GetBytes(got, "input.#").Int())
	require.False(t, gjson.GetBytes(got, "input.0.metadata").Exists())
	require.Equal(t, int64(1), gjson.GetBytes(got, "input.127.keep").Int())
	require.Len(t, gjson.GetBytes(got, "padding").String(), 256*1024)
	// Allow ample middleware overhead, but not a full-body copy for every item.
	require.Less(t, result.AllocedBytesPerOp(), int64(8*len(body)))
	t.Logf("request bytes: %d; allocated bytes/op: %d", len(body), result.AllocedBytesPerOp())
}

func TestOverrideConditionTemplateFunctions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		model     string
		condition string
		want      bool
	}{
		{name: "contains matches substring", model: "anthropic/claude-sonnet-4.5", condition: `{{contains "claude" .Model}}`, want: true},
		{name: "contains misses substring", model: "openai/gpt-4", condition: `{{contains "claude" .Model}}`, want: false},
		{name: "contains works as a pipeline", model: "anthropic/claude-sonnet-4.5", condition: `{{.Model | contains "claude"}}`, want: true},
		{name: "regexMatch matches", model: "anthropic/claude-sonnet-4.5", condition: `{{regexMatch "^anthropic/claude-" .Model}}`, want: true},
		{name: "regexMatch misses", model: "openai/gpt-4", condition: `{{regexMatch "^anthropic/claude-" .Model}}`, want: false},
		{name: "regexMatch is unanchored", model: "proxy/claude-sonnet", condition: `{{regexMatch "claude" .Model}}`, want: true},
		{name: "regexMatch ignores an invalid pattern", model: "anthropic/claude-sonnet-4.5", condition: `{{regexMatch "[" .Model}}`, want: false},
		{name: "globMatch matches", model: "anthropic/claude-sonnet-4.5", condition: `{{globMatch "**/claude-*" .Model}}`, want: true},
		{name: "globMatch keeps star within a segment", model: "anthropic/openai/claude", condition: `{{globMatch "anthropic/*" .Model}}`, want: false},
		{name: "globMatch ignores an invalid pattern", model: "model-a", condition: `{{globMatch "model-[" .Model}}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "condition-func-test",
					Settings: &objects.ChannelSettings{
						BodyOverrideOperations: []objects.OverrideOperation{
							{Op: objects.OverrideOpSet, Path: "matched", Value: "yes", Condition: tt.condition},
						},
					},
				},
				Outbound: &mockTransformer{},
			}
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       &llm.Request{Model: tt.model},
					OriginalModel:    tt.model,
				},
			}

			middleware := applyOverrideRequestBody(outbound)
			result, err := middleware.OnOutboundRawRequest(ctx, &httpclient.Request{Body: []byte(`{"model":"test"}`)})
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(result.Body, "matched").Exists())
		})
	}
}
