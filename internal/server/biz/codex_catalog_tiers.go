package biz

import (
	"slices"

	"github.com/looplj/axonhub/internal/objects"
)

type codexTierRoute struct {
	model     map[string]any
	settings  *objects.ChannelSettings
	responses bool
}

func codexTierID(value any) string {
	if row, ok := value.(map[string]any); ok {
		value = row["id"]
	}
	id, _ := value.(string)
	return id
}

func acceleratedTier(id string) bool {
	mode, _ := objects.NormalizeServiceTier(id)
	return mode == "fast" || mode == "ultrafast"
}

func (r codexTierRoute) supports(id string) bool {
	if !r.responses {
		return false
	}
	requested, _ := objects.NormalizeServiceTier(id)
	wire, allowed := r.settings.ResponsesTierForRequest(requested)
	actual, _ := objects.NormalizeServiceTier(wire)
	return allowed && actual == requested
}

// Only speed choices are routed per request. Other model capabilities keep the
// intersection produced by mergeCodexModels. Never synthesize a model tier.
func applyCodexRouteTiers(result map[string]any, routes []codexTierRoute) {
	if !slices.ContainsFunc(routes, func(r codexTierRoute) bool {
		return r.settings != nil && r.settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported
	}) {
		return
	}
	for _, field := range []string{"service_tiers", "additional_speed_tiers"} {
		existing, exists := result[field].([]any)
		values := make([]any, 0, len(existing))
		for _, value := range existing {
			if !acceleratedTier(codexTierID(value)) {
				values = append(values, value)
			}
		}
		seen := map[string]bool{}
		for _, route := range routes {
			items, _ := route.model[field].([]any)
			for _, value := range items {
				id := codexTierID(value)
				if acceleratedTier(id) && !seen[id] && route.supports(id) {
					seen[id] = true
					values = append(values, value)
				}
			}
		}
		if exists || len(values) > 0 {
			result[field] = values
		}
	}
	if id, _ := result["default_service_tier"].(string); id != "" {
		tiers, _ := result["service_tiers"].([]any)
		if !slices.ContainsFunc(tiers, func(v any) bool { return codexTierID(v) == id }) {
			result["default_service_tier"] = nil
		}
	}
}
