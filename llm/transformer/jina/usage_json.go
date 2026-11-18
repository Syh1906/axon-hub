package jina

import (
	"encoding/json"

	"github.com/looplj/axonhub/llm/internal/pkg/xjson"
)

func (u *RerankUsage) UnmarshalJSON(data []byte) error {
	var aux struct {
		Input  *int            `json:"input_tokens"`
		Prompt *int            `json:"prompt_tokens"`
		Total  *int            `json:"total_tokens"`
		Cost   json.RawMessage `json:"cost"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*u = RerankUsage{Cost: xjson.ParseOptionalFloat64(aux.Cost)}
	input := aux.Input
	if input == nil {
		input = aux.Prompt
	}
	if input == nil {
		input = aux.Total
	}
	if input == nil || *input < 0 || (aux.Total != nil && *aux.Total < 0) {
		u.missingTokens = true
		return nil
	}
	u.PromptTokens = *input
	u.TotalTokens = *input
	if aux.Total != nil {
		u.TotalTokens = *aux.Total
	}
	return nil
}

func (u *EmbeddingUsage) UnmarshalJSON(data []byte) error {
	type alias EmbeddingUsage
	aux := struct {
		*alias

		Cost json.RawMessage `json:"cost"`
	}{alias: (*alias)(u)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	u.Cost = xjson.ParseOptionalFloat64(aux.Cost)
	return nil
}
