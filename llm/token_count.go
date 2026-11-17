package llm

import "encoding/json"

type TokenCountRequest struct {
	Body json.RawMessage `json:"body"`
}

type TokenCountResponse struct {
	InputTokens int64 `json:"input_tokens"`
}
