package orchestrator

import (
	"context"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

// AnthropicNativeToolsSelector filters channels that cannot retain the native
// tool definitions or history in an Anthropic request. An empty compatible set
// stays empty instead of attempting a lossy fallback.
type AnthropicNativeToolsSelector struct {
	wrapped CandidateSelector
}

// WithAnthropicNativeToolsSelector creates a selector that prioritizes Anthropic native tool compatible candidates.
func WithAnthropicNativeToolsSelector(wrapped CandidateSelector) *AnthropicNativeToolsSelector {
	return &AnthropicNativeToolsSelector{
		wrapped: wrapped,
	}
}

func (s *AnthropicNativeToolsSelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}

	// The Anthropic native tools filter are applied only when the API format is Anthropic message.
	if req.APIFormat != llm.APIFormatAnthropicMessage {
		return candidates, nil
	}

	// If request doesn't contain Anthropic native tools, return all candidates
	if !anthropic.ContainsAnthropicNativeTools(req.Tools) && llm.AnthropicNativeToolFeature(req) == "" {
		return candidates, nil
	}

	// Filter: keep only candidates whose channels support Anthropic native tools
	compatible := lo.Filter(candidates, func(c *ChannelModelsCandidate, _ int) bool {
		format := llm.APIFormat(c.APIFormat)
		if format == "" && c.Channel.Outbound != nil {
			format = c.Channel.Outbound.APIFormat()
		}
		if format != "" && format != llm.APIFormatAnthropicMessage {
			// Existing web-search mappings may use Responses or Gemini. Native-only
			// definitions/history are rejected by the shared conversion contract.
			return llm.UnsupportedRequestFeature(req, format) == ""
		}
		return c.Channel.Type.SupportsAnthropicNativeTools()
	})

	if len(compatible) > 0 {
		if log.DebugEnabled(ctx) {
			log.Debug(ctx, "Filtered candidates for Anthropic native tools",
				log.Int("total_candidates", len(candidates)),
				log.Int("compatible_candidates", len(compatible)))
		}

		return compatible, nil
	}

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "No candidates support Anthropic native tools",
			log.Int("total_candidates", len(candidates)))
	}

	return compatible, nil
}
