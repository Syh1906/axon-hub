package api

import (
	"encoding/json"
	"slices"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

// ModelCapabilities has required nested members. Only publish a complete native
// declaration; generic vision/tool/reasoning switches cannot fill missing members.
func anthropicModelCapabilities(card *objects.ModelCard, routes discoveryRoutes) map[string]any {
	if len(card.AnthropicCapabilities) == 0 || string(card.AnthropicCapabilities) == "null" || !routes.nativeMessages {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(card.AnthropicCapabilities, &raw) != nil {
		return nil
	}
	nativeTools := !slices.ContainsFunc(routes.channels, func(ch *ent.Channel) bool {
		return !ch.Type.SupportsAnthropicNativeTools()
	})
	result := make(map[string]any)
	for _, name := range []string{"batch", "citations", "code_execution", "image_input", "pdf_input", "structured_outputs", "context_management", "server_tools", "effort", "thinking"} {
		source, _ := raw[name].(map[string]any)
		supported, ok := source["supported"].(bool)
		if !ok {
			return nil
		}
		fields := map[string]any{"supported": supported}
		result[name] = fields
		switch name {
		case "batch":
			// AxonHub exposes Messages, but does not serve a Message Batches endpoint.
			fields["supported"] = false
		case "pdf_input", "citations":
			// Ordinary Messages reconstruction cannot preserve document blocks
			// or their citation settings. Search-result citations are independent.
			fields["supported"] = false
		case "code_execution":
			fields["supported"] = supported && nativeTools
		case "image_input":
			if len(card.Modalities.Input) > 0 && !slices.Contains(card.Modalities.Input, "image") {
				fields["supported"] = false
			}
		case "context_management":
			// The Messages transformer has no field for context edit directives.
			fields["supported"] = false
			for _, key := range []string{"clear_thinking_20251015", "clear_tool_uses_20250919", "compact_20260112"} {
				if source[key] != nil {
					if _, ok := copyCapability(fields, source, key, false); !ok {
						return nil
					}
				}
			}
		case "server_tools":
			supported = supported && nativeTools
			fields["supported"] = supported
			for _, key := range []string{"code_execution", "web_search"} {
				if _, ok := copyCapability(fields, source, key, supported); !ok {
					return nil
				}
			}
		case "thinking":
			if card.Reasoning.SupportDeclared && !card.Reasoning.Supported {
				supported = false
				fields["supported"] = false
			}
			types, _ := source["types"].(map[string]any)
			published := make(map[string]any)
			for _, key := range []string{"enabled", "adaptive", "disabled", "between_tools"} {
				if _, ok := copyCapability(published, types, key, supported || key == "disabled" || key == "between_tools"); !ok {
					return nil
				}
			}
			fields["types"] = published
		case "effort":
			levels := []string{"low", "medium", "high", "max", "xhigh"}
			var accepted []string
			for _, level := range levels {
				declared, _ := source[level].(map[string]any)
				if declared["supported"] == true && (len(card.ReasoningEfforts) == 0 || slices.Contains(card.ReasoningEfforts, level)) {
					accepted = append(accepted, level)
				}
			}
			anyLevel := false
			for _, key := range levels {
				if key == "xhigh" && source[key] == nil {
					continue
				}
				allowed := supported && (card.Reasoning.Supported || !card.Reasoning.SupportDeclared)
				allowed = allowed && slices.Contains(accepted, key) && routes.acceptsMappedEffort(key, accepted)
				enabled, ok := copyCapability(fields, source, key, allowed)
				if !ok {
					return nil
				}
				anyLevel = anyLevel || enabled
			}
			fields["supported"] = anyLevel
		}
	}
	return result
}

func copyCapability(dest, source map[string]any, key string, allowed bool) (enabled, valid bool) {
	item, _ := source[key].(map[string]any)
	supported, ok := item["supported"].(bool)
	if ok {
		dest[key] = map[string]bool{"supported": allowed && supported}
	}
	return allowed && supported, ok
}
