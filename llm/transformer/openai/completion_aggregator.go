package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func AggregateCompletionStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	a := newCompletionAggregation()
	for _, ev := range chunks {
		a.Add(ctx, ev)
	}
	return a.Result()
}

type completionAggregation struct {
	text      map[int]*strings.Builder
	seen      bool
	id, model string
	created   int64
	usage     *llm.Usage
	choices   map[int]*CompletionChoice
}

func newCompletionAggregation() *completionAggregation {
	return &completionAggregation{text: make(map[int]*strings.Builder), choices: make(map[int]*CompletionChoice)}
}
func (a *completionAggregation) Add(ctx context.Context, chunk *httpclient.StreamEvent) {

	if bytes.HasPrefix(chunk.Data, []byte("[DONE]")) {
		return
	}

	var compResp CompletionResponse
	if err := json.Unmarshal(chunk.Data, &compResp); err != nil {
		return
	}

	a.seen = true

	if a.id == "" && compResp.ID != "" {
		a.id = compResp.ID
	}
	if a.model == "" && compResp.Model != "" {
		a.model = compResp.Model
	}
	if a.created == 0 && compResp.Created != 0 {
		a.created = compResp.Created
	}

	for _, choice := range compResp.Choices {
		combined := a.choices[choice.Index]
		if combined == nil {
			combined = &CompletionChoice{Index: choice.Index}
			a.choices[choice.Index] = combined
		}
		if choice.Text != "" {
			b := a.text[choice.Index]
			if b == nil {
				b = new(strings.Builder)
				a.text[choice.Index] = b
			}
			b.WriteString(choice.Text)
			combined.Text = b.String()
		}
		if choice.FinishReason != nil {
			combined.FinishReason = choice.FinishReason
		}
	}
	if parsed := completionUsageToLLM(compResp.Usage); parsed != nil {
		a.usage = parsed
	}
}

func (a *completionAggregation) Result() ([]byte, llm.ResponseMeta, error) {

	if !a.seen {
		return nil, llm.ResponseMeta{}, nil
	}

	completed := len(a.choices) > 0
	indexes := lo.Keys(a.choices)
	sort.Ints(indexes)
	merged := make([]CompletionChoice, 0, len(a.choices))
	for _, index := range indexes {
		choice := a.choices[index]
		if choice.FinishReason == nil {
			completed = false
		}
		merged = append(merged, *choice)
	}

	response := CompletionResponse{
		ID: a.id, Model: a.model, Object: "text_completion", Created: a.created,
		Choices: merged, Usage: completionUsageFromLLM(a.usage),
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, llm.ResponseMeta{}, err
	}

	return data, llm.ResponseMeta{
		ID:        a.id,
		Usage:     a.usage,
		Completed: completed,
	}, nil
}
