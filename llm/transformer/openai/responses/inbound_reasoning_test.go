package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestConvertItemToMessage_Reasoning(t *testing.T) {
	// convertItemToMessage returns nil for reasoning items since they are
	// handled by convertReasoningWithFollowing in convertInputToMessages
	item := &Item{
		ID:   "reasoning_123",
		Type: "reasoning",
		Summary: []ReasoningSummary{
			{Type: "summary_text", Text: "First, I need to analyze the problem."},
		},
	}

	result, err := convertItemToMessage(item)
	require.NoError(t, err)
	require.Nil(t, result, "reasoning items should return nil from convertItemToMessage")
}

func TestConvertInputToMessages_GroupsConsecutiveToolCalls(t *testing.T) {
	input := &Input{Items: []Item{
		{Role: "user", Content: &Input{Text: lo.ToPtr("Run both tools.")}},
		{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
		{Type: "function_call", CallID: "call_b", Name: "second_tool", Arguments: `{"value":2}`},
		{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("first result")}},
		{Type: "function_call_output", CallID: "call_b", Output: &Input{Text: lo.ToPtr("second result")}},
		{Role: "user", Content: &Input{Text: lo.ToPtr("Continue.")}},
	}}

	messages, err := convertInputToMessages(input)
	require.NoError(t, err)
	require.Len(t, messages, 5)

	require.Equal(t, "user", messages[0].Role)
	require.Equal(t, "Run both tools.", lo.FromPtr(messages[0].Content.Content))

	require.Equal(t, "assistant", messages[1].Role)
	require.Len(t, messages[1].ToolCalls, 2)
	require.Equal(t, "call_a", messages[1].ToolCalls[0].ID)
	require.Equal(t, "first_tool", messages[1].ToolCalls[0].Function.Name)
	require.Equal(t, `{}`, messages[1].ToolCalls[0].Function.Arguments)
	require.Equal(t, "call_b", messages[1].ToolCalls[1].ID)
	require.Equal(t, "second_tool", messages[1].ToolCalls[1].Function.Name)
	require.Equal(t, `{"value":2}`, messages[1].ToolCalls[1].Function.Arguments)

	require.Equal(t, "tool", messages[2].Role)
	require.Equal(t, "call_a", lo.FromPtr(messages[2].ToolCallID))
	require.Equal(t, "first result", lo.FromPtr(messages[2].Content.Content))
	require.Equal(t, "tool", messages[3].Role)
	require.Equal(t, "call_b", lo.FromPtr(messages[3].ToolCallID))
	require.Equal(t, "second result", lo.FromPtr(messages[3].Content.Content))

	require.Equal(t, "user", messages[4].Role)
	require.Equal(t, "Continue.", lo.FromPtr(messages[4].Content.Content))
}

func TestConvertInputToMessages_GroupsMixedToolCallTypes(t *testing.T) {
	input := &Input{Items: []Item{
		{
			Type:      "function_call",
			CallID:    "call_function",
			Name:      "function_tool",
			Namespace: "namespace",
			Arguments: `{"query":"value"}`,
		},
		{
			Type:   "custom_tool_call",
			CallID: "call_custom",
			Name:   "custom_tool",
			Input:  lo.ToPtr("freeform input"),
		},
	}}

	messages, err := convertInputToMessages(input)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "assistant", messages[0].Role)
	require.Len(t, messages[0].ToolCalls, 2)

	functionCall := messages[0].ToolCalls[0]
	require.Equal(t, "call_function", functionCall.ID)
	require.Equal(t, "function", functionCall.Type)
	require.Equal(t, "function_tool", functionCall.Function.Name)
	require.Equal(t, "namespace", functionCall.Function.Namespace)
	require.Equal(t, `{"query":"value"}`, functionCall.Function.Arguments)

	customCall := messages[0].ToolCalls[1]
	require.Equal(t, "call_custom", customCall.ID)
	require.Equal(t, llm.ToolTypeResponsesCustomTool, customCall.Type)
	require.NotNil(t, customCall.ResponseCustomToolCall)
	require.Equal(t, "call_custom", customCall.ResponseCustomToolCall.CallID)
	require.Equal(t, "custom_tool", customCall.ResponseCustomToolCall.Name)
	require.Equal(t, "freeform input", customCall.ResponseCustomToolCall.Input)
}

func TestConvertInputToMessages_DoesNotGroupToolCallsAcrossBoundaries(t *testing.T) {
	t.Run("tool output", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("result")}},
			{Type: "function_call", CallID: "call_b", Name: "second_tool", Arguments: `{}`},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 3)
		require.Equal(t, []string{"assistant", "tool", "assistant"}, []string{
			messages[0].Role,
			messages[1].Role,
			messages[2].Role,
		})
		require.Equal(t, "call_a", messages[0].ToolCalls[0].ID)
		require.Equal(t, "call_b", messages[2].ToolCalls[0].ID)
	})

	t.Run("user message", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Role: "user", Content: &Input{Text: lo.ToPtr("New turn.")}},
			{Type: "function_call", CallID: "call_b", Name: "second_tool", Arguments: `{}`},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 3)
		require.Equal(t, []string{"assistant", "user", "assistant"}, []string{
			messages[0].Role,
			messages[1].Role,
			messages[2].Role,
		})
		require.Equal(t, "call_a", messages[0].ToolCalls[0].ID)
		require.Equal(t, "call_b", messages[2].ToolCalls[0].ID)
	})
}

// A Responses client may place a message item between a tool call and its output.
// Chat-compatible upstreams reject the resulting assistant(tool_calls) -> message ->
// tool sequence with "tool_calls must be followed by tool messages", so the output
// has to sit next to the assistant message that requested it.
func TestConvertInputToMessages_KeepsToolOutputAdjacentAcrossAnInterveningMessage(t *testing.T) {
	roles := func(messages []llm.Message) []string {
		out := make([]string, 0, len(messages))
		for _, msg := range messages {
			out = append(out, msg.Role)
		}
		return out
	}

	t.Run("system message between call and output", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "run_command", Arguments: `{"cmd":"ls"}`},
			{Role: "system", Content: &Input{Text: lo.ToPtr("Approved command prefix saved")}},
			{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("first result")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "tool", "system"}, roles(messages))
		require.Equal(t, "call_a", messages[0].ToolCalls[0].ID)
		require.Equal(t, "call_a", lo.FromPtr(messages[1].ToolCallID))
		require.Equal(t, "first result", lo.FromPtr(messages[1].Content.Content))
		require.Equal(t, "Approved command prefix saved", lo.FromPtr(messages[2].Content.Content))
	})

	t.Run("user message between call and output", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Role: "user", Content: &Input{Text: lo.ToPtr("New turn.")}},
			{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("first result")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "tool", "user"}, roles(messages))
		require.Equal(t, "call_a", lo.FromPtr(messages[1].ToolCallID))
		require.Equal(t, "New turn.", lo.FromPtr(messages[2].Content.Content))
	})

	t.Run("custom tool call output", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "custom_tool_call", CallID: "call_custom", Name: "custom_tool", Input: lo.ToPtr("freeform")},
			{Role: "user", Content: &Input{Text: lo.ToPtr("Interjected.")}},
			{Type: "custom_tool_call_output", CallID: "call_custom", Output: &Input{Text: lo.ToPtr("custom result")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "tool", "user"}, roles(messages))
		require.Equal(t, "call_custom", lo.FromPtr(messages[1].ToolCallID))
		require.Equal(t, "custom result", lo.FromPtr(messages[1].Content.Content))
	})

	t.Run("reasoning merged call keeps its output adjacent", func(t *testing.T) {
		input := &Input{Items: []Item{
			{
				ID:      "reasoning_1",
				Type:    "reasoning",
				Summary: []ReasoningSummary{{Text: "thinking"}},
			},
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Role: "system", Content: &Input{Text: lo.ToPtr("Note.")}},
			{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("first result")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "tool", "system"}, roles(messages))
		require.Len(t, messages[0].ToolCalls, 1)
		require.Equal(t, "call_a", messages[0].ToolCalls[0].ID)
		require.Equal(t, "call_a", lo.FromPtr(messages[1].ToolCallID))
	})

	t.Run("grouped calls collect their outputs in item order", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Type: "function_call", CallID: "call_b", Name: "second_tool", Arguments: `{}`},
			{Role: "user", Content: &Input{Text: lo.ToPtr("Interjected.")}},
			{Type: "function_call_output", CallID: "call_b", Output: &Input{Text: lo.ToPtr("second result")}},
			{Type: "function_call_output", CallID: "call_a", Output: &Input{Text: lo.ToPtr("first result")}},
			{Role: "user", Content: &Input{Text: lo.ToPtr("Continue.")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "tool", "tool", "user", "user"}, roles(messages))
		require.Len(t, messages[0].ToolCalls, 2)
		require.Equal(t, "call_b", lo.FromPtr(messages[1].ToolCallID))
		require.Equal(t, "second result", lo.FromPtr(messages[1].Content.Content))
		require.Equal(t, "call_a", lo.FromPtr(messages[2].ToolCallID))
		require.Equal(t, "Interjected.", lo.FromPtr(messages[3].Content.Content))
		require.Equal(t, "Continue.", lo.FromPtr(messages[4].Content.Content))
	})

	t.Run("an output belonging to a later call is not hoisted", func(t *testing.T) {
		input := &Input{Items: []Item{
			{Type: "function_call", CallID: "call_a", Name: "first_tool", Arguments: `{}`},
			{Role: "user", Content: &Input{Text: lo.ToPtr("Interjected.")}},
			{Type: "function_call", CallID: "call_b", Name: "second_tool", Arguments: `{}`},
			{Type: "function_call_output", CallID: "call_b", Output: &Input{Text: lo.ToPtr("second result")}},
		}}

		messages, err := convertInputToMessages(input)
		require.NoError(t, err)
		require.Equal(t, []string{"assistant", "user", "assistant", "tool"}, roles(messages))
		require.Equal(t, "call_a", messages[0].ToolCalls[0].ID)
		require.Equal(t, "call_b", messages[2].ToolCalls[0].ID)
		require.Equal(t, "call_b", lo.FromPtr(messages[3].ToolCallID))
	})
}

func TestConvertReasoningWithFollowing(t *testing.T) {
	tests := []struct {
		name     string
		items    []Item
		startIdx int
		validate func(t *testing.T, result *llm.Message, consumed int, err error)
	}{
		{
			name: "reasoning item with summary only",
			items: []Item{
				{
					ID:   "reasoning_123",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "First, I need to analyze the problem."},
						{Type: "summary_text", Text: " Then, I will solve it step by step."},
					},
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
				require.Equal(t, "First, I need to analyze the problem. Then, I will solve it step by step.", *result.ReasoningContent)
			},
		},
		{
			name: "reasoning item with encrypted content",
			items: []Item{
				{
					ID:   "reasoning_456",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "Reasoning summary"},
					},
					EncryptedContent: lo.ToPtr("encrypted_data_here"),
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
				require.Equal(t, "Reasoning summary", *result.ReasoningContent)
				require.NotNil(t, result.ReasoningSignature)
				require.Equal(t, "encrypted_data_here", *result.ReasoningSignature)
			},
		},
		{
			name: "reasoning item with empty summary",
			items: []Item{
				{
					ID:      "reasoning_789",
					Type:    "reasoning",
					Summary: []ReasoningSummary{},
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, consumed)
				require.Equal(t, "assistant", result.Role)
				require.Nil(t, result.ReasoningContent)
			},
		},
		{
			name: "reasoning merged with function_call",
			items: []Item{
				{
					ID:   "reasoning_001",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "I need to call the function."},
					},
				},
				{
					Type:      "function_call",
					CallID:    "call_123",
					Name:      "get_weather",
					Arguments: `{"location": "Tokyo"}`,
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
				require.Equal(t, "I need to call the function.", *result.ReasoningContent)
				require.Len(t, result.ToolCalls, 1)
				require.Equal(t, "call_123", result.ToolCalls[0].ID)
				require.Equal(t, "get_weather", result.ToolCalls[0].Function.Name)
			},
		},
		{
			name: "consecutive reasoning items merged with function_call",
			items: []Item{
				{
					ID:               "rs_first",
					Type:             "reasoning",
					Summary:          []ReasoningSummary{{Type: "summary_text", Text: "first"}},
					EncryptedContent: lo.ToPtr("gAAAA_FIRST_BLOB"),
				},
				{
					ID:               "rs_second",
					Type:             "reasoning",
					Summary:          []ReasoningSummary{{Type: "summary_text", Text: "second"}},
					EncryptedContent: lo.ToPtr("gAAAA_SECOND_BLOB"),
				},
				{
					Type:      "function_call",
					CallID:    "call_123",
					Name:      "get_weather",
					Arguments: `{}`,
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 3, consumed)
				require.Equal(t, []llm.ReasoningItem{
					{ID: "rs_first", Content: "first", Signature: "gAAAA_FIRST_BLOB"},
					{ID: "rs_second", Content: "second", Signature: "gAAAA_SECOND_BLOB"},
				}, result.ReasoningItems)
				require.Equal(t, "firstsecond", lo.FromPtr(result.ReasoningContent))
				require.Equal(t, "gAAAA_SECOND_BLOB", lo.FromPtr(result.ReasoningSignature))
				require.Len(t, result.ToolCalls, 1)
				require.Equal(t, "call_123", result.ToolCalls[0].ID)
			},
		},
		{
			name: "reasoning merged with assistant text message",
			items: []Item{
				{
					ID:   "reasoning_002",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "Thinking about the answer."},
					},
				},
				{
					Type: "message",
					Role: "assistant",
					Text: lo.ToPtr("The answer is 42."),
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
				require.Equal(t, "Thinking about the answer.", *result.ReasoningContent)
				require.NotNil(t, result.Content.Content)
				require.Equal(t, "The answer is 42.", *result.Content.Content)
			},
		},
		{
			name: "reasoning stops at user message",
			items: []Item{
				{
					ID:   "reasoning_003",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "Thinking..."},
					},
				},
				{
					Type: "message",
					Role: "user",
					Text: lo.ToPtr("Next question"),
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
				require.Equal(t, "Thinking...", *result.ReasoningContent)
				require.Empty(t, result.ToolCalls)
			},
		},
		{
			name: "reasoning stops at function_call_output",
			items: []Item{
				{
					ID:   "reasoning_004",
					Type: "reasoning",
					Summary: []ReasoningSummary{
						{Type: "summary_text", Text: "Thinking..."},
					},
				},
				{
					Type:   "function_call_output",
					CallID: "call_456",
					Output: &Input{Text: lo.ToPtr("result")},
				},
			},
			startIdx: 0,
			validate: func(t *testing.T, result *llm.Message, consumed int, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, consumed)
				require.Equal(t, "assistant", result.Role)
				require.NotNil(t, result.ReasoningContent)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, consumed, err := convertReasoningWithFollowing(tt.items, tt.startIdx)
			tt.validate(t, result, consumed, err)
		})
	}
}

func TestBuildReasoningItems_OmitsEmptyEncryptedContent(t *testing.T) {
	items := buildReasoningItems(llm.Message{
		ReasoningItems: []llm.ReasoningItem{{ID: "rs_summary", Content: "summary only"}},
	})

	require.Len(t, items, 1)
	require.Equal(t, "rs_summary", items[0].ID)
	require.Nil(t, items[0].EncryptedContent)
}

func TestInboundTransformer_TransformRequest_WithReasoningInput(t *testing.T) {
	trans := NewInboundTransformer()

	tests := []struct {
		name        string
		httpReq     *httpclient.Request
		expectError bool
		validate    func(t *testing.T, result *llm.Request)
	}{
		{
			name: "request with reasoning input item merged with assistant",
			httpReq: &httpclient.Request{
				Body: []byte(`{
					"model": "o3",
					"input": [
						{
							"type": "message",
							"role": "user",
							"content": "What is 2+2?"
						},
						{
							"type": "reasoning",
							"id": "reasoning_abc",
							"summary": [
								{"type": "summary_text", "text": "Let me think about this math problem."}
							]
						},
						{
							"type": "message",
							"role": "assistant",
							"content": "The answer is 4."
						}
					]
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Request) {
				require.Equal(t, "o3", result.Model)
				// Reasoning + assistant message are merged into one message
				require.Len(t, result.Messages, 2)

				// First message: user
				require.Equal(t, "user", result.Messages[0].Role)
				require.Equal(t, "What is 2+2?", *result.Messages[0].Content.Content)

				// Second message: assistant with merged reasoning and text content
				require.Equal(t, "assistant", result.Messages[1].Role)
				require.NotNil(t, result.Messages[1].ReasoningContent)
				require.Equal(t, "Let me think about this math problem.", *result.Messages[1].ReasoningContent)
				require.NotNil(t, result.Messages[1].Content.Content)
				require.Equal(t, "The answer is 4.", *result.Messages[1].Content.Content)
			},
		},
		{
			name: "request with reasoning config",
			httpReq: &httpclient.Request{
				Body: []byte(`{
					"model": "o3",
					"input": "Solve this complex problem",
					"reasoning": {
						"effort": "high",
						"summary": "detailed",
						"max_tokens": 10000
					}
				}`),
			},
			expectError: false,
			validate: func(t *testing.T, result *llm.Request) {
				require.Equal(t, "o3", result.Model)
				require.Equal(t, "high", result.ReasoningEffort)
				require.NotNil(t, result.ReasoningBudget)
				require.Equal(t, int64(10000), *result.ReasoningBudget)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.TransformRequest(context.Background(), tt.httpReq)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestConvertToResponsesAPIResponse_AttachesAnnotationsToFirstTextItem(t *testing.T) {
	resp := convertToResponsesAPIResponse(&llm.Response{
		ID:      "resp_annotations",
		Created: 1677652288,
		Model:   "gpt-4o",
		Choices: []llm.Choice{{
			Message: &llm.Message{
				ID:   "msg_annotations",
				Role: "assistant",
				Annotations: []llm.Annotation{
					{
						Type:       "url_citation",
						StartIndex: lo.ToPtr(int64(0)),
						EndIndex:   lo.ToPtr(int64(5)),
						URLCitation: &llm.URLCitation{
							URL:   "https://example.com",
							Title: "Example",
						},
					},
				},
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{Type: "text", Text: lo.ToPtr("Hello")},
						{Type: "text", Text: lo.ToPtr(" world")},
					},
				},
			},
			FinishReason: lo.ToPtr("stop"),
		}},
	})

	require.Len(t, resp.Output, 1)
	require.NotNil(t, resp.Output[0].Content)
	require.Len(t, resp.Output[0].Content.Items, 2)
	require.Len(t, resp.Output[0].Content.Items[0].Annotations, 1)
	require.Empty(t, resp.Output[0].Content.Items[1].Annotations)
	require.Equal(t, "url_citation", resp.Output[0].Content.Items[0].Annotations[0].Type)
	require.NotNil(t, resp.Output[0].Content.Items[0].Annotations[0].URLCitation)
	require.Equal(t, "https://example.com", resp.Output[0].Content.Items[0].Annotations[0].URLCitation.URL)
}

func TestConvertToResponsesAPIResponse_PreservesMultipleReasoningItems(t *testing.T) {
	resp := convertToResponsesAPIResponse(&llm.Response{
		ID:      "resp_reasoning_items",
		Model:   "gpt-5",
		Created: 1,
		Choices: []llm.Choice{{
			Message: &llm.Message{
				Role: "assistant",
				ReasoningItems: []llm.ReasoningItem{
					{ID: "rs_first", Content: "first", Signature: "gAAAA_FIRST_BLOB"},
					{ID: "rs_second", Content: "second", Signature: "gAAAA_SECOND_BLOB"},
				},
				ToolCalls: []llm.ToolCall{{
					ID:   "call_tool",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "lookup",
						Arguments: "{}",
					},
				}},
			},
		}},
	})

	require.Len(t, resp.Output, 3)
	require.Equal(t, "reasoning", resp.Output[0].Type)
	require.Equal(t, "rs_first", resp.Output[0].ID)
	require.Len(t, resp.Output[0].Summary, 1)
	require.Equal(t, "first", resp.Output[0].Summary[0].Text)
	require.NotNil(t, resp.Output[0].EncryptedContent)
	require.Equal(t, "gAAAA_FIRST_BLOB", *resp.Output[0].EncryptedContent)

	require.Equal(t, "reasoning", resp.Output[1].Type)
	require.Equal(t, "rs_second", resp.Output[1].ID)
	require.Len(t, resp.Output[1].Summary, 1)
	require.Equal(t, "second", resp.Output[1].Summary[0].Text)
	require.NotNil(t, resp.Output[1].EncryptedContent)
	require.Equal(t, "gAAAA_SECOND_BLOB", *resp.Output[1].EncryptedContent)
	require.Equal(t, "function_call", resp.Output[2].Type)
}

func TestInboundTransformer_TransformResponse_WithReasoningContent(t *testing.T) {
	trans := NewInboundTransformer()

	tests := []struct {
		name        string
		chatResp    *llm.Response
		expectError bool
		validate    func(t *testing.T, result *httpclient.Response)
	}{
		{
			name: "response with reasoning content",
			chatResp: &llm.Response{
				ID:      "chatcmpl-reasoning",
				Object:  "chat.completion",
				Created: 1677652288,
				Model:   "o3",
				Choices: []llm.Choice{
					{
						Index: 0,
						Message: &llm.Message{
							Role:               "assistant",
							ReasoningContent:   lo.ToPtr("I analyzed the problem step by step."),
							ReasoningSignature: lo.ToPtr("encrypted_data_here"),
							Content: llm.MessageContent{
								Content: lo.ToPtr("The answer is 42."),
							},
						},
						FinishReason: lo.ToPtr("stop"),
					},
				},
				Usage: &llm.Usage{
					PromptTokens:     50,
					CompletionTokens: 100,
					TotalTokens:      150,
					CompletionTokensDetails: &llm.CompletionTokensDetails{
						ReasoningTokens: 80,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *httpclient.Response) {
				require.Equal(t, http.StatusOK, result.StatusCode)

				var resp Response

				err := json.Unmarshal(result.Body, &resp)
				require.NoError(t, err)
				require.Equal(t, "response", resp.Object)
				require.Equal(t, "o3", resp.Model)

				// Should have reasoning output item and message output item
				require.Len(t, resp.Output, 2)

				// First output should be reasoning
				reasoningOutput := resp.Output[0]
				require.Equal(t, "reasoning", reasoningOutput.Type)
				require.Len(t, reasoningOutput.Summary, 1)
				require.Equal(t, "summary_text", reasoningOutput.Summary[0].Type)
				require.Equal(t, "I analyzed the problem step by step.", reasoningOutput.Summary[0].Text)
				require.NotNil(t, reasoningOutput.EncryptedContent)
				require.Equal(t, "encrypted_data_here", *reasoningOutput.EncryptedContent)

				// Second output should be message
				messageOutput := resp.Output[1]
				require.Equal(t, "message", messageOutput.Type)
				require.Equal(t, "assistant", messageOutput.Role)

				// Check usage includes reasoning tokens
				require.NotNil(t, resp.Usage)
				require.Equal(t, int64(80), resp.Usage.OutputTokenDetails.ReasoningTokens)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.TransformResponse(context.Background(), tt.chatResp)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)

				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestInboundTransformer_TransformResponse_FinishReasonStatus(t *testing.T) {
	trans := NewInboundTransformer()

	tests := []struct {
		name          string
		finishReason  string
		wantStatus    string
		wantIncReason string
	}{
		{"content_filter", "content_filter", "incomplete", "content_filter"},
		{"length", "length", "incomplete", "max_output_tokens"},
		{"cancelled", "cancelled", "cancelled", ""},
		{"stop", "stop", "completed", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.TransformResponse(context.Background(), &llm.Response{
				ID:     "chatcmpl-1",
				Object: "chat.completion",
				Model:  "gpt-4",
				Choices: []llm.Choice{{
					Index: 0,
					Message: &llm.Message{
						Role:    "assistant",
						Content: llm.MessageContent{Content: lo.ToPtr("partial")},
					},
					FinishReason: lo.ToPtr(tt.finishReason),
				}},
			})
			require.NoError(t, err)

			var resp Response
			require.NoError(t, json.Unmarshal(result.Body, &resp))
			require.NotNil(t, resp.Status)
			require.Equal(t, tt.wantStatus, *resp.Status)

			if tt.wantIncReason == "" {
				require.Nil(t, resp.IncompleteDetails)
			} else {
				require.NotNil(t, resp.IncompleteDetails)
				require.Equal(t, tt.wantIncReason, resp.IncompleteDetails.Reason)
			}
		})
	}
}
