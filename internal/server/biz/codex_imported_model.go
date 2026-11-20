package biz

import (
	"fmt"
	"maps"
	"slices"
)

func importedCodexRoute(ch *Channel, target, actual string, responses bool, profile map[string]any, r codexDefinitionRoute) codexTierRoute {
	limits := make(map[string]any, len(r.Limits))
	maps.Copy(limits, r.Limits)
	if ch.Settings != nil {
		for _, ctx := range ch.Settings.ModelContexts {
			if ctx.ModelID == actual && ctx.MaxContextWindow != nil {
				boundCodexRouteLimit(limits, "max_context_window", float64(*ctx.MaxContextWindow))
				break
			}
		}
	}
	if maximum, ok := limits["max_context_window"].(float64); ok {
		boundCodexRouteLimit(limits, "context_window", maximum)
	}
	for _, field := range []string{"node_repl_auto_review_required", "node_repl_disabled"} {
		if r.Capabilities[field] == true {
			limits[field] = true
		}
	}
	if levels, ok := limits["supported_reasoning_levels"].([]any); ok {
		limits["supported_reasoning_levels"] = slices.Clone(levels)
		constrainCodexReasoningProfile(limits, profile)
	}
	return codexTierRoute{channel: ch, responses: responses, models: &codexTierModels{RequestModel: target, Model: actual}, capabilities: profile, limits: limits}
}

func boundCodexRouteLimit(limits map[string]any, field string, bound float64) {
	if previous, ok := limits[field].(float64); !ok || previous > bound {
		limits[field] = bound
	}
}

// A missing limit leaves the model's declaration available through that route.
// For fields limited on every route, keep every value allowed by at least one.
func mergeImportedCodexRoutes(publicID string, profile map[string]any, routes []codexTierRoute) (map[string]any, error) {
	common := maps.Clone(routes[0].limits)
	for _, route := range routes[1:] {
		for field, value := range common {
			next, ok := route.limits[field]
			if !ok {
				delete(common, field)
				continue
			}
			switch allowed := value.(type) {
			case []any:
				options, ok := next.([]any)
				if !ok {
					return nil, fmt.Errorf("invalid Codex route choices %s", field)
				}
				common[field] = slices.Concat(allowed, options)
			case float64:
				limit, ok := next.(float64)
				if !ok {
					return nil, fmt.Errorf("invalid Codex route limit %s", field)
				}
				common[field] = max(allowed, limit)
			case bool:
				enabled, ok := next.(bool)
				if !ok {
					return nil, fmt.Errorf("invalid Codex route capability %s", field)
				}
				common[field] = allowed || enabled
			}
		}
	}
	restricted := maps.Clone(profile)
	maps.Copy(restricted, common)
	for _, route := range routes {
		for _, field := range []string{"node_repl_auto_review_required", "node_repl_disabled"} {
			if route.limits[field] == true {
				restricted[field] = true
			}
		}
	}
	result, err := mergeCodexModels(publicID, []map[string]any{profile, restricted})
	if err != nil {
		return nil, err
	}
	for _, field := range codexContextFields {
		if limit, bounded := common[field].(float64); bounded && profile[field] == nil {
			result[field] = limit
		}
	}
	return result, nil
}
