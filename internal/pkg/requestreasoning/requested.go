// Package requestreasoning reads the budget from the actual provider request.
package requestreasoning

import (
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/gjson"
)

// Requested preserves numeric token budgets and provider effort names verbatim.
func Requested(body []byte, format llm.APIFormat) *string {
	var value gjson.Result
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact:
		if effort := responses.EffectiveReasoningEffort(body); effort != "" {
			return &effort
		}
	case llm.APIFormatOpenAIChatCompletion:
		value = gjson.GetBytes(body, "reasoning_effort")
	case llm.APIFormatGeminiContents:
		config := gjson.GetBytes(body, "generationConfig.thinkingConfig")
		if !config.Exists() {
			// Antigravity wraps the Gemini request in a transport envelope.
			config = gjson.GetBytes(body, "request.generationConfig.thinkingConfig")
		}
		value = config.Get("thinkingLevel")
		if !value.Exists() {
			if budget := config.Get("thinkingBudget"); budget.Type == gjson.Number {
				return new(budget.String())
			}
		}
	case llm.APIFormatAnthropicMessage:
		value = gjson.GetBytes(body, "output_config.effort")
		if !value.Exists() {
			if budget := gjson.GetBytes(body, "thinking.budget_tokens"); budget.Type == gjson.Number {
				return new(budget.String())
			}
			if gjson.GetBytes(body, "thinking.type").String() == "disabled" {
				return new("none")
			}
		}
	}
	if value.Type == gjson.String && value.Str != "" {
		return &value.Str
	}
	return nil
}
