package biz

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var errCodexPlainModelList = errors.New("Codex catalog requires a models array, not an OpenAI data list")
var errCodexInvalidEntries = errors.New("Codex catalog contains invalid or duplicate model definitions")

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
		if err := validateCodexWireModel(m); err != nil {
			return nil, fmt.Errorf("catalog model %q: %w", slug, err)
		}
		normalizeCodexWireDefaults(m)
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
		for _, field := range []string{"base_instructions", "model_messages", "shell_type", "apply_patch_tool_type", "web_search_tool_type", "tool_mode", "multi_agent_version"} {
			if !reflect.DeepEqual(result[field], next[field]) {
				return nil, fmt.Errorf("incompatible Codex metadata field %s for %s", field, publicID)
			}
		}
		policy, okA := result["truncation_policy"].(map[string]any)
		otherPolicy, okB := next["truncation_policy"].(map[string]any)
		if !okA || !okB || !reflect.DeepEqual(policy["mode"], otherPolicy["mode"]) {
			return nil, fmt.Errorf("incompatible Codex truncation policy for %s", publicID)
		}
		if a, ok := policy["limit"].(float64); ok {
			if b, ok := otherPolicy["limit"].(float64); ok {
				policy["limit"] = min(a, b)
			}
		}
		for _, field := range []string{"supported_in_api", "support_verbosity", "supports_parallel_tool_calls", "supports_experimental_context", "supports_image_detail_original", "supports_reasoning_effort_updates", "supports_search_tool", "use_responses_lite", "supports_reasoning_summary_parameter"} {
			value, exists := result[field].(bool)
			other, ok := next[field].(bool)
			switch field {
			case "supports_reasoning_summary_parameter":
				if !exists {
					value = true
				}
				if !ok {
					other = true
				}
				result[field] = value && other
			case "supports_image_detail_original", "supports_search_tool", "supports_experimental_context", "use_responses_lite", "supports_reasoning_effort_updates":
				result[field] = value && other
			default:
				if exists && ok {
					result[field] = value && other
				} else {
					delete(result, field)
				}
			}
		}
		for _, field := range []string{"node_repl_disabled", "node_repl_auto_review_required"} {
			a, _ := result[field].(bool)
			b, _ := next[field].(bool)
			result[field] = a || b
		}
		for _, field := range []string{"supported_reasoning_levels", "service_tiers", "additional_speed_tiers", "input_modalities", "experimental_supported_tools"} {
			values, exists := result[field].([]any)
			if !exists {
				continue
			}
			other, _ := next[field].([]any)
			common := make([]any, 0)
			for _, value := range values {
				for _, candidate := range other {
					equal := reflect.DeepEqual(value, candidate)
					key := ""
					if field == "supported_reasoning_levels" {
						key = "effort"
					} else if field == "service_tiers" || field == "additional_speed_tiers" {
						key = "id"
					}
					if key != "" {
						a, okA := value.(map[string]any)
						b, okB := candidate.(map[string]any)
						if okA && okB {
							id, valid := a[key].(string)
							equal = valid && id != "" && id == b[key]
						}
					}
					if equal {
						common = append(common, value)
						break
					}
				}
			}
			result[field] = common
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
	if err := validateCodexWireModel(result); err != nil {
		return nil, err
	}
	return result, nil
}

// Isolate invalid remote entries; saved model definitions use strict validation.
// Partial results retain a diagnostic so callers can use valid entries and warn.
func parseRemoteCodexCatalog(body []byte) (map[string]map[string]any, error) {
	if len(body) > codexCatalogMaxBytes {
		return nil, fmt.Errorf("Codex catalog exceeds 1 MiB")
	}
	var wrapper struct {
		Models []json.RawMessage `json:"models"`
		Data   []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &wrapper) != nil {
		return nil, fmt.Errorf("Codex catalog requires a models array, not an OpenAI data list")
	}
	if wrapper.Models == nil && wrapper.Data != nil {
		return nil, errCodexPlainModelList
	}
	if wrapper.Models == nil {
		return nil, fmt.Errorf("Codex catalog requires a models array, not an OpenAI data list")
	}
	result := make(map[string]map[string]any)
	seen := make(map[string]bool)
	for _, raw := range wrapper.Models {
		parsed, err := parseCodexCatalog(append(append([]byte(`{"models":[`), raw...), []byte(`]}`)...))
		if err != nil {
			continue
		}
		for slug, metadata := range parsed {
			if seen[slug] {
				delete(result, slug)
				continue
			}
			seen[slug] = true
			result[slug] = metadata
		}
	}
	if omitted := len(wrapper.Models) - len(result); omitted > 0 {
		return result, fmt.Errorf("%w: %d entries omitted", errCodexInvalidEntries, omitted)
	}
	return result, nil
}
