package openai

import (
	"encoding/json"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
)

type CompletionRequest struct {
	Model            string             `json:"model"`
	Prompt           json.RawMessage    `json:"prompt"`
	Suffix           string             `json:"suffix,omitempty"`
	MaxTokens        *int64             `json:"max_tokens,omitempty"`
	Temperature      *float64           `json:"temperature,omitempty"`
	TopP             *float64           `json:"top_p,omitempty"`
	N                *int64             `json:"n,omitempty"`
	Stream           *bool              `json:"stream,omitempty"`
	StreamOptions    *llm.StreamOptions `json:"stream_options,omitempty"`
	Logprobs         *int64             `json:"logprobs,omitempty"`
	Echo             *bool              `json:"echo,omitempty"`
	Stop             *Stop              `json:"stop,omitempty"`
	PresencePenalty  *float64           `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64           `json:"frequency_penalty,omitempty"`
	BestOf           *int64             `json:"best_of,omitempty"`
	LogitBias        map[string]int64   `json:"logit_bias,omitempty"`
	Seed             *int64             `json:"seed,omitempty"`
	User             string             `json:"user,omitempty"`
}

type CompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   *CompletionUsage   `json:"usage,omitempty"`
}

type CompletionChoice struct {
	Text         string               `json:"text"`
	Index        int                  `json:"index"`
	Logprobs     *llm.LogprobsContent `json:"logprobs,omitempty"`
	FinishReason *string              `json:"finish_reason,omitempty"`
}

type CompletionUsage struct {
	PromptTokens     *int64   `json:"prompt_tokens"`
	CompletionTokens *int64   `json:"completion_tokens"`
	TotalTokens      *int64   `json:"total_tokens"`
	Cost             *float64 `json:"cost,omitempty"`
}

func completionUsageFromLLM(u *llm.Usage) *CompletionUsage {
	if u == nil {
		return nil
	}

	return &CompletionUsage{
		PromptTokens:     lo.ToPtr(u.PromptTokens),
		CompletionTokens: lo.ToPtr(u.CompletionTokens),
		TotalTokens:      lo.ToPtr(u.TotalTokens),
		Cost:             u.Cost,
	}
}

// Completion token billing needs both input and output counts. An absent or
// partial usage object is unknown, not an explicit zero-token measurement.
func completionUsageToLLM(u *CompletionUsage) *llm.Usage {
	if u == nil || u.PromptTokens == nil || u.CompletionTokens == nil || *u.PromptTokens < 0 || *u.CompletionTokens < 0 {
		return nil
	}
	total := *u.PromptTokens + *u.CompletionTokens
	if u.TotalTokens != nil {
		total = *u.TotalTokens
	}
	if total < 0 {
		return nil
	}
	return &llm.Usage{PromptTokens: *u.PromptTokens, CompletionTokens: *u.CompletionTokens, TotalTokens: total}
}
