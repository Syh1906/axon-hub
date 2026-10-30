package biz

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func parseCodexCatalog(body []byte) (map[string]map[string]any, error) {
	if len(body) > codexCatalogMaxBytes {
		return nil, fmt.Errorf("Codex catalog exceeds 1 MiB")
	}
	var wrapper struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil || wrapper.Models == nil {
		return nil, fmt.Errorf("Codex catalog requires a models array, not an OpenAI data list")
	}
	result := make(map[string]map[string]any, len(wrapper.Models))
	for _, m := range wrapper.Models {
		slug, _ := m["slug"].(string)
		if strings.TrimSpace(slug) == "" || result[slug] != nil {
			return nil, fmt.Errorf("catalog slugs must be unique and nonempty")
		}
		for _, field := range []string{"display_name", "shell_type", "visibility"} {
			if _, ok := m[field].(string); !ok {
				return nil, fmt.Errorf("catalog model %q requires %s", slug, field)
			}
		}
		for _, field := range []string{"supported_reasoning_levels", "experimental_supported_tools"} {
			if _, ok := m[field].([]any); !ok {
				return nil, fmt.Errorf("catalog model %q requires %s array", slug, field)
			}
		}
		for _, field := range []string{"supported_in_api", "support_verbosity"} {
			if _, ok := m[field].(bool); !ok {
				return nil, fmt.Errorf("catalog model %q requires %s boolean", slug, field)
			}
		}
		if _, ok := m["priority"].(float64); !ok {
			return nil, fmt.Errorf("catalog model %q requires priority", slug)
		}
		if _, ok := m["truncation_policy"].(map[string]any); !ok {
			return nil, fmt.Errorf("catalog model %q requires truncation_policy", slug)
		}
		instructions, legacy := m["base_instructions"].(string)
		if messages, ok := m["model_messages"].(map[string]any); ok {
			if template, ok := messages["instructions_template"].(string); ok {
				instructions, legacy = template, true
			}
		}
		if !legacy {
			return nil, fmt.Errorf("catalog model %q requires model instructions", slug)
		}
		// Preserve both modern instructions and the legacy wire field used by older clients.
		m["base_instructions"] = instructions
		result[slug] = m
	}
	return result, nil
}

func mergeCodexModels(publicID string, variants []map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(variants[0])
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	for _, next := range variants[1:] {
		// Different tool dialects or instructions cannot be safely combined into one picker entry.
		for _, field := range []string{"base_instructions", "model_messages", "shell_type", "apply_patch_tool_type", "web_search_tool_type", "truncation_policy", "tool_mode", "multi_agent_version"} {
			if !reflect.DeepEqual(result[field], next[field]) {
				return nil, fmt.Errorf("incompatible Codex metadata for %s", publicID)
			}
		}
		for field, value := range result {
			switch typed := value.(type) {
			case bool:
				other, _ := next[field].(bool)
				result[field] = typed && other
			case []any:
				other, _ := next[field].([]any)
				common := make([]any, 0)
				for _, item := range typed {
					for _, candidate := range other {
						if reflect.DeepEqual(item, candidate) {
							common = append(common, item)
							break
						}
					}
				}
				result[field] = common
			}
		}
		for _, field := range []string{"context_window", "max_context_window", "auto_compact_token_limit", "effective_context_window_percent"} {
			a, okA := result[field].(float64)
			b, okB := next[field].(float64)
			if okA && okB {
				result[field] = min(a, b)
			} else {
				delete(result, field)
			}
		}
		for _, field := range []string{"default_reasoning_level", "default_service_tier", "default_verbosity"} {
			if !reflect.DeepEqual(result[field], next[field]) {
				result[field] = nil
			}
		}
	}
	result["slug"] = publicID
	result["display_name"] = publicID
	result["upgrade"] = nil
	// A catalog never forces transport. The provider/client chooses HTTP or WS.
	delete(result, "prefer_websockets")
	for field, list := range map[string]string{"default_reasoning_level": "supported_reasoning_levels", "default_service_tier": "service_tiers"} {
		selected, _ := result[field].(string)
		if selected == "" {
			continue
		}
		found := false
		choices, _ := result[list].([]any)
		for _, item := range choices {
			if obj, ok := item.(map[string]any); ok && (obj["effort"] == selected || obj["id"] == selected) {
				found = true
			}
		}
		if !found {
			result[field] = nil
		}
	}
	return result, nil
}
