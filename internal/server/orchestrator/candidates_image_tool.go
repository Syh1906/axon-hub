package orchestrator

import (
	"context"
	"net/http"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
)

// ImageGenerationToolSelector filters channel permissions without changing tools
// or falling back outside the caller's eligible candidates.
type ImageGenerationToolSelector struct {
	wrapped CandidateSelector
}

func WithImageGenerationToolSelector(wrapped CandidateSelector) *ImageGenerationToolSelector {
	return &ImageGenerationToolSelector{wrapped: wrapped}
}

func (s *ImageGenerationToolSelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}
	// Standalone image requests may be converted into tools later by an outbound
	// transformer. This setting only governs tools supplied by the caller.
	if len(candidates) == 0 || req.RequestType == llm.RequestTypeImage || !lo.ContainsBy(req.Tools, func(tool llm.Tool) bool {
		return tool.Type == llm.ToolTypeImageGeneration
	}) {
		return candidates, nil
	}
	allowed := lo.Filter(candidates, func(candidate *ChannelModelsCandidate, _ int) bool {
		return candidate.Channel.ImageGenerationToolAllowed()
	})
	if len(allowed) == 0 {
		return nil, &llm.ResponseError{
			StatusCode: http.StatusServiceUnavailable,
			Detail: llm.ErrorDetail{
				Code: "image_generation_tool_not_allowed", Type: "server_error",
				Message: "没有允许内置生图工具的可用渠道。",
			},
		}
	}
	return allowed, nil
}
