package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Native Completions accepts one prompt, text batches, token IDs, or token-ID
// batches. Preserve that shape in a single provider request rather than joining
// independent prompts into chat text.
func validateCompletionPrompt(prompt json.RawMessage) error {
	prompt = bytes.TrimSpace(prompt)
	if len(prompt) == 0 {
		return fmt.Errorf("prompt is required")
	}
	if prompt[0] == '"' {
		var text string
		if err := json.Unmarshal(prompt, &text); err == nil && text != "" {
			return nil
		}
		return fmt.Errorf("prompt must contain text")
	}
	var items []json.RawMessage
	if prompt[0] != '[' || json.Unmarshal(prompt, &items) != nil || len(items) == 0 {
		return fmt.Errorf("prompt must be text or a non-empty prompt array")
	}
	kind := bytes.TrimSpace(items[0])[0]
	for _, item := range items {
		item = bytes.TrimSpace(item)
		switch kind {
		case '"':
			var text string
			if item[0] != '"' || json.Unmarshal(item, &text) != nil {
				return fmt.Errorf("prompt text array must contain only strings")
			}
		case '[':
			var tokens []json.RawMessage
			if item[0] != '[' || json.Unmarshal(item, &tokens) != nil || !validCompletionTokenIDs(tokens) {
				return fmt.Errorf("prompt token batches must contain non-negative integer arrays")
			}
		default:
			if !validCompletionTokenIDs([]json.RawMessage{item}) {
				return fmt.Errorf("prompt token IDs must be non-negative integers")
			}
		}
	}
	return nil
}

func validCompletionTokenIDs(tokens []json.RawMessage) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, raw := range tokens {
		var token int64
		if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || json.Unmarshal(raw, &token) != nil || token < 0 {
			return false
		}
	}
	return true
}
