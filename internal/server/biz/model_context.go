package biz

import (
	"fmt"
	"maps"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
)

func validateChannelModelContexts(settings *objects.ChannelSettings) error {
	seen := make(map[string]bool)
	for _, value := range settings.ModelContexts {
		if strings.TrimSpace(value.ModelID) == "" || seen[value.ModelID] {
			return fmt.Errorf("channel context overrides require unique, nonempty model IDs")
		}
		seen[value.ModelID] = true
		if err := (&objects.ModelContextSettings{DefaultContextWindow: value.DefaultContextWindow, MaxContextWindow: value.MaxContextWindow}).Validate(); err != nil {
			return fmt.Errorf("model %s: %w", value.ModelID, err)
		}
	}
	return nil
}

// Resolve each route before computing the caller-visible common capabilities.
// Only these two fields are overridden; source instructions and tools stay live.
func applyCodexContextSettings(metadata map[string]any, modelSettings *objects.ModelSettings, channelSettings *objects.ChannelSettings, actualModel string) map[string]any {
	var defaultWindow, maxWindow *int
	if modelSettings != nil && modelSettings.ContextSettings != nil {
		defaultWindow = modelSettings.ContextSettings.DefaultContextWindow
		maxWindow = modelSettings.ContextSettings.MaxContextWindow
	}
	if channelSettings != nil {
		for _, value := range channelSettings.ModelContexts {
			if value.ModelID == actualModel {
				if value.DefaultContextWindow != nil {
					defaultWindow = value.DefaultContextWindow
				}
				if value.MaxContextWindow != nil {
					maxWindow = value.MaxContextWindow
				}
				break
			}
		}
	}
	result := maps.Clone(metadata)
	if maxWindow != nil {
		result["max_context_window"] = float64(*maxWindow)
	}
	if defaultWindow != nil {
		result["context_window"] = float64(*defaultWindow)
	} else if maximum, ok := result["max_context_window"].(float64); ok {
		// A maximum-only override intentionally chooses that maximum as default.
		// Unconfigured models keep an upstream default when one is present.
		if current, exists := result["context_window"].(float64); maxWindow != nil || !exists || current <= 0 {
			result["context_window"] = maximum
		}
	}
	// A later model-level change can otherwise raise an inherited default above
	// an independently configured channel maximum. Respect the route's maximum.
	if current, ok := result["context_window"].(float64); ok {
		if maximum, ok := result["max_context_window"].(float64); ok && current > maximum {
			result["context_window"] = maximum
		}
	}
	return result
}
