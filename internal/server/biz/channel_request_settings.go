package biz

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func ValidateChannelResponseTimeouts(settings *objects.ChannelSettings) error {
	for _, value := range []*int{settings.StreamFirstEventTimeoutSeconds, settings.NonStreamResponseTimeoutSeconds} {
		if value != nil && (*value < 0 || int64(*value) > math.MaxInt64/int64(time.Second)) {
			return fmt.Errorf("channel response timeout must be a non-negative duration in seconds")
		}
	}
	return nil
}

func (c *Channel) SessionReuseEnabled() bool {
	return c.Settings == nil || !c.Settings.DisableSessionReuse
}

func (c *Channel) ImageGenerationToolAllowed() bool {
	return c.Settings == nil || c.Settings.AllowImageGenerationTool == nil || *c.Settings.AllowImageGenerationTool
}

// A converted Gemini or Anthropic search uses the same tool at a Responses upstream.
func requestsResponsesWebSearch(req *llm.Request) bool {
	for _, tool := range req.Tools {
		if tool.Type == llm.ToolTypeWebSearch || tool.Type == llm.ToolTypeGoogleSearch {
			return true
		}
	}
	if ext := req.ProviderExtensions; ext != nil && ext.OpenAIResponses != nil && ext.OpenAIResponses.Request != nil {
		for _, tool := range ext.OpenAIResponses.Request.RawTools {
			if tool.Type == "web_search_preview" || strings.HasPrefix(tool.Type, "web_search_preview_") {
				return true
			}
		}
	}
	return false
}

func (c *Channel) BlocksResponsesWebSearch(req *llm.Request) bool {
	return c.Settings != nil && c.Settings.AllowWebSearchTool != nil && !*c.Settings.AllowWebSearchTool && requestsResponsesWebSearch(req)
}

// Adapted tools can use their verified Responses endpoint independently
// of whether ordinary Responses conversations are enabled for this channel.
func (c *Channel) UsesAdaptedResponsesTool(req *llm.Request) bool {
	if c == nil || c.Channel == nil || c.Settings == nil || req == nil {
		return false
	}
	for _, tool := range req.Tools {
		if tool.Type == llm.ToolTypeImageGeneration && c.ImageGenerationToolAllowed() && slices.Contains(c.Settings.AdaptedCapabilities, probeImageTool) {
			return true
		}
	}
	return c.Settings.AllowWebSearchTool != nil && *c.Settings.AllowWebSearchTool && slices.Contains(c.Settings.AdaptedCapabilities, probeWebSearch) && requestsResponsesWebSearch(req)
}
