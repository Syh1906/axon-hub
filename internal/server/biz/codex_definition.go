package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/looplj/axonhub/internal/objects"
)

type codexDefinition struct {
	ClientVersion     string                       `json:"clientVersion"`
	SourceChannelID   int                          `json:"sourceChannelId"`
	SourceModelID     string                       `json:"sourceModelId"`
	Model             map[string]any               `json:"model"`
	Models            map[string]codexModelProfile `json:"models,omitempty"`
	Routes            []codexDefinitionRoute       `json:"routes"`
	SourceDifferences []codexSourceDifference      `json:"sourceDifferences,omitempty"`
}

type codexDefinitionRoute struct {
	ChannelID    int            `json:"channelId"`
	ModelID      string         `json:"modelId"`
	Limits       map[string]any `json:"limits,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
}

// Source-specific capabilities are kept on routes; instructions and tool dialect
// remain in the canonical model definition.
var codexCapabilityFields = []string{
	"context_window", "max_context_window", "auto_compact_token_limit", "effective_context_window_percent",
	"supported_reasoning_levels", "service_tiers", "additional_speed_tiers", "input_modalities", "experimental_supported_tools",
	"supported_in_api", "support_verbosity", "supports_parallel_tool_calls", "supports_experimental_context", "supports_image_detail_original", "supports_reasoning_effort_updates", "supports_search_tool", "use_responses_lite", "supports_reasoning_summary_parameter",
	"node_repl_disabled", "node_repl_auto_review_required", "default_reasoning_level", "default_service_tier", "default_verbosity",
}

func codexRouteCapabilities(metadata map[string]any) map[string]any {
	result := make(map[string]any)
	for _, field := range codexCapabilityFields {
		if value, ok := metadata[field]; ok {
			result[field] = value
		}
	}
	return result
}

var definitionVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func decodeCodexDefinition(raw []byte) (*codexDefinition, error) {
	var d codexDefinition
	if len(raw) > codexCatalogMaxBytes || json.Unmarshal(raw, &d) != nil {
		return nil, fmt.Errorf("invalid Codex model definition")
	}
	if len(d.ClientVersion) > 32 || !definitionVersion.MatchString(d.ClientVersion) || len(d.Routes) == 0 {
		return nil, fmt.Errorf("Codex definition requires clientVersion and confirmed routes")
	}
	body, _ := json.Marshal(map[string]any{"models": []any{d.Model}})
	parsed, err := parseCodexCatalog(body)
	if err != nil {
		return nil, err
	}
	for _, metadata := range parsed {
		d.Model = metadata
	}
	for id, profile := range d.Models {
		if id == "" || profile.SourceChannelID <= 0 || profile.SourceModelID == "" {
			return nil, fmt.Errorf("Codex model profiles require a public ID and source")
		}
		body, _ := json.Marshal(map[string]any{"models": []any{profile.Model}})
		models, err := parseCodexCatalog(body)
		if err != nil {
			return nil, fmt.Errorf("invalid Codex profile %s: %w", id, err)
		}
		profile.Model = models[profile.SourceModelID]
		if profile.Model == nil {
			return nil, fmt.Errorf("Codex profile source model does not match its metadata")
		}
		d.Models[id] = profile
	}
	seen := make(map[ChannelModelKey]bool)
	for _, r := range d.Routes {
		key := ChannelModelKey{ChannelID: r.ChannelID, ModelID: r.ModelID}
		if r.ChannelID <= 0 || r.ModelID == "" || seen[key] {
			return nil, fmt.Errorf("Codex definition routes must be unique channel/model pairs")
		}
		seen[key] = true
		candidate := make(map[string]any)
		for k, v := range d.Model {
			candidate[k] = v
		}
		if r.Capabilities != nil {
			known := codexRouteCapabilities(r.Capabilities)
			// .syh.11 wrote this client policy into route snapshots. Keep reading
			// those definitions; new imports only store it on the client profile.
			if policy, exists := r.Capabilities["truncation_policy"]; exists {
				known["truncation_policy"] = policy
			}
			if len(known) != len(r.Capabilities) {
				return nil, fmt.Errorf("unknown route capability")
			}
			for _, field := range codexCapabilityFields {
				delete(candidate, field)
			}
			for k, v := range known {
				candidate[k] = v
			}
			encoded, _ := json.Marshal(map[string]any{"models": []any{candidate}})
			if _, err := parseCodexCatalog(encoded); err != nil {
				return nil, fmt.Errorf("invalid route capabilities: %w", err)
			}
		}

		for field, value := range r.Limits {
			switch field {
			case "context_window", "max_context_window", "auto_compact_token_limit", "effective_context_window_percent":
				n, ok := value.(float64)
				if !ok || n <= 0 {
					return nil, fmt.Errorf("invalid Codex route limit %s", field)
				}
			case "supported_reasoning_levels", "service_tiers", "additional_speed_tiers", "input_modalities", "experimental_supported_tools":
				if _, ok := value.([]any); !ok {
					return nil, fmt.Errorf("invalid Codex route choices %s", field)
				}
			case "node_repl_disabled", "node_repl_auto_review_required", "supported_in_api", "support_verbosity", "supports_parallel_tool_calls", "supports_experimental_context", "supports_image_detail_original", "supports_reasoning_effort_updates", "supports_search_tool", "use_responses_lite", "supports_reasoning_summary_parameter":
				if _, ok := value.(bool); !ok {
					return nil, fmt.Errorf("invalid Codex route capability %s", field)
				}
			default:
				return nil, fmt.Errorf("unsupported Codex route limit %s", field)
			}
		}
		for k, v := range r.Limits {
			candidate[k] = v
		}
		if err := validateCodexWireModel(candidate); err != nil {
			return nil, fmt.Errorf("invalid route limits: %w", err)
		}
	}
	return &d, nil
}

// Only current, caller-visible routes participate. Never turn a new association
// into a confirmed route merely because its public model name matches.
func (d *codexDefinition) catalogModel(m ModelFacade, version string) (map[string]any, error) {
	if version != d.ClientVersion {
		return nil, fmt.Errorf("Codex definition does not support this client version")
	}
	canonical := d.Model
	if len(d.Models) > 0 {
		target := m.TargetID
		if target == "" {
			target = m.ID
		}
		profile, ok := d.Models[target]
		if !ok {
			return nil, fmt.Errorf("unconfirmed Codex public model %s", target)
		}
		canonical = profile.Model
	}
	bindings := make(map[ChannelModelKey]codexDefinitionRoute)
	for _, r := range d.Routes {
		bindings[ChannelModelKey{ChannelID: r.ChannelID, ModelID: r.ModelID}] = r
	}
	var variants []map[string]any
	for _, connection := range m.Connections {
		for _, entry := range connection.Models {
			r, ok := bindings[ChannelModelKey{ChannelID: connection.Channel.ID, ModelID: entry.ActualModel}]
			if !ok {
				return nil, fmt.Errorf("unconfirmed Codex route channel=%d model=%s", connection.Channel.ID, entry.ActualModel)
			}
			variant := make(map[string]any, len(canonical))
			for k, v := range canonical {
				variant[k] = v
			}

			if r.Capabilities != nil {
				for _, field := range codexCapabilityFields {
					delete(variant, field)
				}
				for k, v := range r.Capabilities {
					variant[k] = v
				}
			}
			// Truncation controls local tool output, not an upstream context limit.
			// Keep accepting old route snapshots but use the confirmed client policy.
			variant["truncation_policy"] = canonical["truncation_policy"]
			normalizeCodexWireDefaults(variant)
			limited := make(map[string]any, len(variant))
			for k, v := range variant {
				limited[k] = v
			}
			for k, v := range r.Limits {
				limited[k] = v
			}
			effective, err := mergeCodexModels(m.ID, []map[string]any{variant, limited})
			if err != nil {
				return nil, err
			}
			variants = append(variants, effective)
		}
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("no confirmed visible route")
	}
	return mergeCodexModels(m.ID, variants)
}

// Preview is read-only. Saving the returned definition through UpdateModel is
// the explicit confirmation of its instructions and all listed route bindings.
func (svc *ModelService) PreviewCodexCatalog(ctx context.Context, id int, version string) (objects.JSONRawMessage, error) {
	if !definitionVersion.MatchString(version) || len(version) > 32 {
		return nil, fmt.Errorf("invalid client version")
	}
	m, err := svc.entFromContext(ctx).Model.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var connections []*ModelChannelConnection
	byPublicID := make(map[string][]*ModelChannelConnection)
	seenRoutes := make(map[ChannelModelKey]bool)
	for _, publicID := range PublicModelIDs(m) {
		matches, queryErr := svc.QueryModelChannelConnectionsForModel(ctx, EffectiveModelAssociationsForRequest(svc.modelSettingsOrDefault(ctx), m, publicID), publicID)
		if queryErr != nil {
			return nil, queryErr
		}
		byPublicID[publicID] = matches
		for _, connection := range matches {
			copyConnection := *connection
			copyConnection.Models = nil
			for _, entry := range connection.Models {
				key := ChannelModelKey{ChannelID: connection.Channel.ID, ModelID: entry.ActualModel}
				if !seenRoutes[key] {
					seenRoutes[key] = true
					copyConnection.Models = append(copyConnection.Models, entry)
				}
			}
			if len(copyConnection.Models) > 0 {
				connections = append(connections, &copyConnection)
			}
		}
	}
	service := NewCodexCatalogService(svc, svc.channelService)
	channels := make(map[int]*Channel)
	for _, ch := range svc.channelService.GetEnabledChannels() {
		channels[ch.ID] = ch
	}
	sources := service.fetchCatalogSources(ctx, connections, channels, version)
	d := codexDefinition{ClientVersion: version, Models: make(map[string]codexModelProfile)}
	for _, publicID := range PublicModelIDs(m) {
		profile, differences := previewCodexModelProfile(publicID, byPublicID[publicID], sources)
		if profile == nil {
			continue
		}
		d.Models[publicID] = *profile
		d.SourceDifferences = append(d.SourceDifferences, differences...)
		if d.Model == nil {
			d.Model = profile.Model
			d.SourceChannelID = profile.SourceChannelID
			d.SourceModelID = profile.SourceModelID
		}
	}
	for _, c := range connections {
		for _, entry := range c.Models {
			route := codexDefinitionRoute{ChannelID: c.Channel.ID, ModelID: entry.ActualModel}
			if metadata := sources[c.Channel.ID][entry.ActualModel]; metadata != nil {
				route.Capabilities = codexRouteCapabilities(metadata)
			}
			d.Routes = append(d.Routes, route)
		}
	}
	if d.Model == nil {
		return nil, fmt.Errorf("no complete catalog source; import a complete model definition")
	}
	body, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if _, err = decodeCodexDefinition(body); err != nil {
		return nil, err
	}
	return objects.JSONRawMessage(body), nil
}
