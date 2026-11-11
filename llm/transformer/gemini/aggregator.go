package gemini

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// candidateAggregator is a helper struct to aggregate data for each candidate.
type candidateAggregator struct {
	index             int64
	parts             []*Part
	text              strings.Builder
	textPart          *Part
	finishReason      string
	groundingMetadata *GroundingMetadata
}

// AggregateStreamChunks aggregates Gemini streaming response chunks into a complete response.
// The output is always in Gemini format.
func AggregateStreamChunks(
	ctx context.Context,
	chunks []*httpclient.StreamEvent,
) ([]byte, llm.ResponseMeta, error) {
	if len(chunks) == 0 {
		data, err := json.Marshal(&GenerateContentResponse{})
		return data, llm.ResponseMeta{}, err
	}

	var (
		lastResp      *GenerateContentResponse
		usage         *UsageMetadata
		responseID    string
		modelVersion  string
		candidateAggs = make(map[int64]*candidateAggregator)
	)

	for _, chunk := range chunks {
		// Skip empty or [DONE] events
		if chunk == nil || len(chunk.Data) == 0 || string(chunk.Data) == "[DONE]" {
			continue
		}

		var geminiResp GenerateContentResponse
		if err := json.Unmarshal(chunk.Data, &geminiResp); err != nil {
			continue // Skip invalid chunks
		}

		// Capture response ID and model version
		if responseID == "" && geminiResp.ResponseID != "" {
			responseID = geminiResp.ResponseID
		}

		if modelVersion == "" && geminiResp.ModelVersion != "" {
			modelVersion = geminiResp.ModelVersion
		}

		// Process each candidate
		for _, candidate := range geminiResp.Candidates {
			if candidate == nil {
				continue
			}

			candidateIndex := candidate.Index

			// Initialize candidate aggregator if it doesn't exist
			if _, ok := candidateAggs[candidateIndex]; !ok {
				candidateAggs[candidateIndex] = &candidateAggregator{
					index: candidateIndex,
				}
			}

			agg := candidateAggs[candidateIndex]

			// Process content parts
			if candidate.Content != nil {
				agg.appendParts(candidate.Content.Parts)
			}

			// Capture finish reason
			if candidate.FinishReason != "" {
				agg.finishReason = candidate.FinishReason
			}

			// Capture grounding metadata (use the last one if multiple chunks have it)
			if candidate.GroundingMetadata != nil {
				agg.groundingMetadata = candidate.GroundingMetadata
			}
		}

		// Extract usage information if present
		if geminiResp.UsageMetadata != nil {
			usage = geminiResp.UsageMetadata
		}

		lastResp = &geminiResp
	}

	// Build the final response in Gemini format
	if lastResp == nil {
		data, err := json.Marshal(&GenerateContentResponse{})
		return data, llm.ResponseMeta{}, err
	}

	return buildGeminiResponse(candidateAggs, responseID, modelVersion, usage)
}

// buildGeminiResponse builds a Gemini format response from aggregated data.
func buildGeminiResponse(
	candidateAggs map[int64]*candidateAggregator,
	responseID, modelVersion string,
	usage *UsageMetadata,
) ([]byte, llm.ResponseMeta, error) {
	candidates := make([]*Candidate, len(candidateAggs))
	completed := len(candidateAggs) > 0

	for i := range candidates {
		agg := candidateAggs[int64(i)]
		if agg == nil {
			continue
		}

		content := &Content{
			Role: "model",
		}

		agg.flushText()
		content.Parts = agg.parts

		// Determine finish reason
		finishReason := agg.finishReason
		if finishReason == "" {
			completed = false
			finishReason = "STOP"
		}

		candidates[i] = &Candidate{
			Index:             agg.index,
			Content:           content,
			FinishReason:      finishReason,
			GroundingMetadata: agg.groundingMetadata,
		}
	}

	// Build the final response
	response := &GenerateContentResponse{
		ResponseID:    responseID,
		ModelVersion:  modelVersion,
		Candidates:    candidates,
		UsageMetadata: usage,
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, llm.ResponseMeta{}, err
	}

	var llmUsage *llm.Usage
	if usage != nil {
		llmUsage = convertToLLMUsage(usage)
	}

	return data, llm.ResponseMeta{
		ID:        responseID,
		Usage:     llmUsage,
		Completed: completed,
	}, nil
}

func (a *candidateAggregator) flushText() {
	if a.textPart != nil {
		a.textPart.Text = a.text.String()
		a.textPart = nil
		a.text.Reset()
	}
}

func (a *candidateAggregator) appendParts(parts []*Part) {
	for i, part := range parts {
		if part == nil {
			continue
		}
		plainText := part.Text != "" && part.ThoughtSignature == "" && part.InlineData == nil && part.FileData == nil && part.FunctionCall == nil && part.FunctionResponse == nil
		// Merge only a continuation across chunk boundaries. Explicit parts in
		// one chunk and signed parts retain their own position and identity.
		if i == 0 && plainText && a.textPart != nil && a.textPart.Thought == part.Thought {
			a.text.WriteString(part.Text)
			continue
		}
		a.flushText()
		a.parts = append(a.parts, part)
		if plainText {
			a.textPart = part
			a.text.WriteString(part.Text)
		}
	}
}
