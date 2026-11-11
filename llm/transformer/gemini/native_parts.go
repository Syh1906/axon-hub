package gemini

import (
	"strings"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
)

// captureNativeParts records only references and signatures. Text and media
// remain in the unified fields, so prompt edits are not undone by raw replay.
func captureNativeParts(msg *llm.Message, parts []*Part) {
	if !needsNativePartLayout(parts) {
		return
	}
	var contentIndex, toolIndex int
	var reasoning strings.Builder
	for _, part := range parts {
		ref := llm.GeminiPartRef{Thought: part.Thought, Signature: part.ThoughtSignature}
		switch {
		case part.Text != "" && part.Thought:
			ref.Kind, ref.Index = "reasoning", len(msg.ReasoningItems)
			msg.ReasoningItems = append(msg.ReasoningItems, llm.ReasoningItem{Content: part.Text, Signature: part.ThoughtSignature})
			reasoning.WriteString(part.Text)
		case part.Text != "" || part.InlineData != nil || part.FileData != nil:
			ref.Kind, ref.Index = "content", contentIndex
			contentIndex++
		case part.FunctionCall != nil:
			ref.Kind, ref.Index = "tool_call", toolIndex
			toolIndex++
		case part.ThoughtSignature != "":
			ref.Kind = "signature"
		default:
			continue
		}
		msg.GeminiParts = append(msg.GeminiParts, ref)
	}
	if reasoning.Len() > 0 {
		msg.ReasoningContent = lo.ToPtr(reasoning.String())
	}
}

func needsNativePartLayout(parts []*Part) bool {
	var hasContent, hasCall bool
	for _, part := range parts {
		if part.Thought || part.ThoughtSignature != "" {
			return true
		}
		hasCall = hasCall || part.FunctionCall != nil
		hasContent = hasContent || part.Text != "" || part.InlineData != nil || part.FileData != nil
	}
	return hasContent && hasCall
}

// restoreNativeParts uses the newly converted payloads, then restores original
// positions and signatures. Foreign messages keep the existing signature policy.
func restoreNativeParts(msg *llm.Message, generated []*Part) []*Part {
	if len(msg.GeminiParts) == 0 {
		return generated
	}
	var content, calls []*Part
	for _, part := range generated {
		switch {
		case part.FunctionCall != nil:
			calls = append(calls, part)
		case !part.Thought:
			content = append(content, part)
		}
	}
	result := make([]*Part, 0, len(msg.GeminiParts))
	usedContent, usedCalls := make([]bool, len(content)), make([]bool, len(calls))
	for _, ref := range msg.GeminiParts {
		var part *Part
		switch ref.Kind {
		case "content":
			if ref.Index < 0 || ref.Index >= len(content) {
				continue
			}
			part = content[ref.Index]
			usedContent[ref.Index] = true
		case "tool_call":
			if ref.Index < 0 || ref.Index >= len(calls) {
				continue
			}
			part = calls[ref.Index]
			usedCalls[ref.Index] = true
		case "reasoning":
			if ref.Index < 0 || ref.Index >= len(msg.ReasoningItems) {
				continue
			}
			part = &Part{Text: msg.ReasoningItems[ref.Index].Content}
		case "signature":
			part = &Part{}
		default:
			continue
		}
		part.Thought, part.ThoughtSignature = ref.Thought, ref.Signature
		result = append(result, part)
	}
	// Retain newly added content/tools from request middleware as well.
	for i, part := range content {
		if !usedContent[i] {
			result = append(result, part)
		}
	}
	for i, part := range calls {
		if !usedCalls[i] {
			result = append(result, part)
		}
	}
	return result
}
