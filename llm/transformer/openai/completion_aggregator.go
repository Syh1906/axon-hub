package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func AggregateCompletionStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	if len(chunks) == 0 {
		data, err := json.Marshal(&llm.Response{})
		return data, llm.ResponseMeta{}, err
	}

	var (
		lastChunkResponse *CompletionResponse
		id                string
		model             string
		created           int64
		usage             *llm.Usage
		choices           = make(map[int]*llm.CompletionChoice)
	)

	for _, chunk := range chunks {
		if bytes.HasPrefix(chunk.Data, []byte("[DONE]")) {
			continue
		}

		var compResp CompletionResponse
		if err := json.Unmarshal(chunk.Data, &compResp); err != nil {
			continue
		}

		lastChunkResponse = &compResp

		if id == "" && compResp.ID != "" {
			id = compResp.ID
		}
		if model == "" && compResp.Model != "" {
			model = compResp.Model
		}
		if created == 0 && compResp.Created != 0 {
			created = compResp.Created
		}

		for _, choice := range compResp.Choices {
			combined := choices[choice.Index]
			if combined == nil {
				combined = &llm.CompletionChoice{Index: choice.Index}
				choices[choice.Index] = combined
			}
			combined.Text += choice.Text
			if choice.FinishReason != nil {
				combined.FinishReason = choice.FinishReason
			}
		}
		if parsed := completionUsageToLLM(compResp.Usage); parsed != nil {
			usage = parsed
		}
	}

	if lastChunkResponse == nil {
		data, err := json.Marshal(&llm.Response{})
		return data, llm.ResponseMeta{}, err
	}

	completed := len(choices) > 0
	indexes := lo.Keys(choices)
	sort.Ints(indexes)
	merged := make([]llm.CompletionChoice, 0, len(choices))
	for _, index := range indexes {
		choice := choices[index]
		if choice.FinishReason == nil {
			completed = false
			choice.FinishReason = lo.ToPtr("stop")
		}
		merged = append(merged, *choice)
	}

	response := &llm.Response{
		ID:          id,
		Model:       model,
		Object:      "text_completion",
		Created:     created,
		Completion:  &llm.CompletionResponse{Choices: merged},
		Usage:       usage,
		RequestType: llm.RequestTypeCompletion,
		APIFormat:   llm.APIFormatOpenAICompletion,
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, llm.ResponseMeta{}, err
	}

	return data, llm.ResponseMeta{
		ID:        id,
		Usage:     usage,
		Completed: completed,
	}, nil
}
