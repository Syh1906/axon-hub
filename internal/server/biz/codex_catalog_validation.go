package biz

import (
	"encoding/json"
	"fmt"
)

// Check known wire types before publishing a model. Codex decodes the complete
// response atomically, so one malformed known field would hide healthy models.
// Unknown fields remain in the original map for newer clients.
func validateCodexWireModel(m map[string]any) error {
	encoded, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var known struct {
		Messages     *codexWireModelMessages `json:"model_messages"`
		Availability *struct {
			Message *string `json:"message"`
		} `json:"availability_nux"`
		Specialty   *string `json:"model_specialty"`
		ReviewModel *string `json:"auto_review_model_override"`
		Summary     *string `json:"default_reasoning_summary"`
		Priority    int32   `json:"priority"`
		Description *string `json:"description"`
		Reasoning   []struct {
			Effort      *string `json:"effort"`
			Description *string `json:"description"`
		} `json:"supported_reasoning_levels"`
		DefaultReasoning *string `json:"default_reasoning_level"`
		DefaultTier      *string `json:"default_service_tier"`
		DefaultVerbosity *string `json:"default_verbosity"`
		Tiers            []struct {
			ID          *string `json:"id"`
			Name        *string `json:"name"`
			Description *string `json:"description"`
		} `json:"service_tiers"`
		SpeedTiers       []string `json:"additional_speed_tiers"`
		Tools            []string `json:"experimental_supported_tools"`
		Modalities       []string `json:"input_modalities"`
		Context          *int64   `json:"context_window"`
		MaxContext       *int64   `json:"max_context_window"`
		Compact          *int64   `json:"auto_compact_token_limit"`
		Percent          int64    `json:"effective_context_window_percent"`
		CompHash         *string  `json:"comp_hash"`
		ToolMode         *string  `json:"tool_mode"`
		MultiAgent       *string  `json:"multi_agent_version"`
		MultiAgentEffort *string  `json:"multi_agent_reasoning_effort"`
		Truncation       struct {
			Mode  string `json:"mode"`
			Limit *int64 `json:"limit"`
		} `json:"truncation_policy"`
	}
	if err := json.Unmarshal(encoded, &known); err != nil {
		return fmt.Errorf("invalid Codex model field type: %w", err)
	}
	switch m["shell_type"] {
	case "default", "local", "shell_command", "unified_exec", "disabled":
	default:
		return fmt.Errorf("invalid Codex shell_type")
	}
	switch m["visibility"] {
	case "list", "hide", "none":
	default:
		return fmt.Errorf("invalid Codex visibility")
	}
	if known.Truncation.Limit == nil || (known.Truncation.Mode != "tokens" && known.Truncation.Mode != "bytes") {
		return fmt.Errorf("invalid Codex truncation_policy")
	}
	if known.Availability != nil && known.Availability.Message == nil {
		return fmt.Errorf("invalid Codex availability_nux")
	}
	for _, choice := range known.Reasoning {
		if choice.Effort == nil || choice.Description == nil {
			return fmt.Errorf("invalid Codex reasoning choice")
		}
	}
	for _, tier := range known.Tiers {
		if tier.ID == nil || tier.Name == nil || tier.Description == nil {
			return fmt.Errorf("invalid Codex service tier")
		}
	}
	for _, modality := range known.Modalities {
		switch modality {
		case "text", "image", "audio":
		default:
			return fmt.Errorf("invalid Codex input modality")
		}
	}
	for _, field := range []string{"supported_reasoning_levels", "service_tiers", "additional_speed_tiers", "experimental_supported_tools", "input_modalities"} {
		if value, present := m[field]; present && value == nil {
			return fmt.Errorf("invalid null Codex array %s", field)
		}
		if values, ok := m[field].([]any); ok {
			for _, value := range values {
				if value == nil {
					return fmt.Errorf("invalid null Codex array element %s", field)
				}
			}
		}

	}
	for _, field := range []string{"supported_in_api", "support_verbosity", "supports_parallel_tool_calls", "supports_image_detail_original", "supports_search_tool", "supports_experimental_context", "use_responses_lite", "supports_reasoning_effort_updates", "node_repl_auto_review_required", "node_repl_disabled", "include_skills_usage_instructions", "include_plugin_usage_instructions", "include_apps_usage_instructions", "supports_reasoning_summary_parameter"} {
		if value, present := m[field]; present {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("invalid Codex boolean %s", field)
			}
		}
	}
	if value, present := m["effective_context_window_percent"]; present && value == nil {
		return fmt.Errorf("invalid Codex context percentage")
	}
	if value := m["apply_patch_tool_type"]; value != nil && value != "freeform" {
		return fmt.Errorf("invalid Codex apply_patch_tool_type")
	}
	if value, present := m["web_search_tool_type"]; present && value != "text" && value != "text_and_image" {
		return fmt.Errorf("invalid Codex web_search_tool_type")
	}
	if value := m["default_verbosity"]; value != nil && value != "low" && value != "medium" && value != "high" {
		return fmt.Errorf("invalid Codex default_verbosity")
	}
	if known.Messages != nil {
		if budget := known.Messages.TokenBudget; budget != nil {
			if budget.ReminderThresholdTokens == nil || budget.ReminderMessageTemplate == nil || budget.GuidanceMessage == nil || budget.AutoCompactFallbackPrompt == nil || budget.AutoCompactFallbackBufferTokens == nil {
				return fmt.Errorf("incomplete Codex model token budget")
			}
		}
		if messages, ok := m["model_messages"].(map[string]any); ok {
			if approvals, ok := messages["approvals"].(map[string]any); ok {
				_, a := approvals["request_permissions"]
				_, b := approvals["request_permissions_tool"]
				if a && b {
					return fmt.Errorf("duplicate Codex request permissions message")
				}
			}
		}
	}
	return nil
}

// Defaults follow Codex rust-v0.166.0 ModelInfo. Keep omission from turning
// a known false capability or 95% context budget into a different declaration.
func normalizeCodexWireDefaults(m map[string]any) {
	switch m["shell_type"] {
	case "default", "local", "shell_command":
		m["shell_type"] = "unified_exec"
	}
	if _, ok := m["web_search_tool_type"]; !ok {
		m["web_search_tool_type"] = "text"
	}

	for _, field := range []string{"supports_image_detail_original", "supports_search_tool", "supports_experimental_context", "use_responses_lite", "supports_reasoning_effort_updates", "node_repl_disabled", "node_repl_auto_review_required"} {
		if _, ok := m[field]; !ok {
			m[field] = false
		}
	}
	if _, ok := m["supports_reasoning_summary_parameter"]; !ok {
		m["supports_reasoning_summary_parameter"] = true
	}
	if _, ok := m["effective_context_window_percent"]; !ok {
		m["effective_context_window_percent"] = float64(95)
	}
	if _, ok := m["input_modalities"]; !ok {
		m["input_modalities"] = []any{"text", "image"}
	}
}
