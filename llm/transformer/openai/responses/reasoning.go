package responses

import (
	"strings"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
)

// reasoningFromItem keeps native Responses data separate from the scalar text
// consumed by Chat/Anthropic converters. Full text takes precedence over a
// summary there; concatenating both would duplicate the same reasoning.
func reasoningFromItem(item Item) llm.ReasoningItem {
	if item.Content == nil && len(item.Summary) == 0 && lo.FromPtr(item.EncryptedContent) == "" {
		return llm.ReasoningItem{ID: item.ID}
	}
	native := &llm.ResponsesReasoning{Status: item.Status}
	for _, summary := range item.Summary {
		native.Summary = append(native.Summary, summary.Text)
	}
	if item.Content != nil {
		native.Content = make([]string, 0, len(item.Content.Items))
		for _, part := range item.Content.Items {
			if part.Type == "reasoning_text" {
				native.Content = append(native.Content, lo.FromPtr(part.Text))
			}
		}
	}
	parts := native.Content
	if len(parts) == 0 {
		parts = native.Summary
	}
	return llm.ReasoningItem{
		ID: item.ID, Content: strings.Join(parts, ""),
		Signature: lo.FromPtr(item.EncryptedContent), Responses: native,
	}
}

func nativeReasoningItem(reasoning llm.ReasoningItem) Item {
	item := Item{
		ID: reasoning.ID, Type: "reasoning", Status: reasoning.Responses.Status,
		Summary: []ReasoningSummary{},
	}
	for _, text := range reasoning.Responses.Summary {
		item.Summary = append(item.Summary, ReasoningSummary{Type: "summary_text", Text: text})
	}
	if reasoning.Responses.Content != nil {
		item.Content = &Input{Items: make([]Item, 0, len(reasoning.Responses.Content))}
		for _, text := range reasoning.Responses.Content {
			item.Content.Items = append(item.Content.Items, Item{Type: "reasoning_text", Text: lo.ToPtr(text)})
		}
	}
	if reasoning.Signature != "" {
		item.EncryptedContent = lo.ToPtr(reasoning.Signature)
	}
	return item
}
