package api

import (
	"context"
	"slices"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
)

type discoveryRoutes struct {
	channels       []*ent.Channel
	nativeMessages bool
	forwardsEffort bool
}

func modelDiscoveryRoutes(ctx context.Context, m biz.ModelFacade, formats ...llm.APIFormat) discoveryRoutes {
	channels := make(map[int]*ent.Channel, len(m.Connections))
	for _, conn := range m.Connections {
		channels[conn.Channel.ID] = conn.Channel
	}
	result := discoveryRoutes{nativeMessages: true, forwardsEffort: true}
	matches := m.DiscoveryMatches()
	for _, format := range formats {
		for _, route := range orchestrator.InspectMatchedProtocolRoutes(ctx, m.TargetID, format, matches) {
			if route.State == "excluded" {
				continue
			}
			if ch := channels[route.ChannelID]; ch != nil {
				result.channels = append(result.channels, ch)
				delete(channels, route.ChannelID)
			}
			result.nativeMessages = result.nativeMessages && route.APIFormat == llm.APIFormatAnthropicMessage.String()
			// The Ollama chat outbound has no request option for reasoning effort.
			result.forwardsEffort = result.forwardsEffort && route.APIFormat != llm.APIFormatOllamaChat.String()
		}
	}
	return result
}

func (r discoveryRoutes) acceptsMappedEffort(level string, accepted []string) bool {
	if len(r.channels) == 0 || !r.forwardsEffort {
		return false
	}
	for _, ch := range r.channels {
		mapped := level
		if ch.Settings != nil {
			mapped = llm.ApplyReasoningEffortMapping(level, ch.Settings.TransformOptions.ReasoningEffortMapping)
		}
		if !slices.Contains(accepted, mapped) {
			return false
		}
	}
	return true
}

func (r discoveryRoutes) reasoningLevels(card *objects.ModelCard) []string {
	if !card.Reasoning.Supported {
		return nil
	}
	var levels []string
	for _, level := range card.ReasoningEfforts {
		if level != "" && r.acceptsMappedEffort(level, card.ReasoningEfforts) && !slices.Contains(levels, level) {
			levels = append(levels, level)
		}
	}
	return levels
}
