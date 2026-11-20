package biz

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/looplj/axonhub/internal/objects"
)

type codexDefinition struct {
	Mode              string                       `json:"mode,omitempty"`
	SourceKind        string                       `json:"sourceKind,omitempty"`
	SnapshotVersion   string                       `json:"snapshotVersion,omitempty"`
	Overrides         map[string]any               `json:"overrides,omitempty"`
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
var codexContextFields = []string{"context_window", "max_context_window", "auto_compact_token_limit", "effective_context_window_percent"}

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
	if d.Mode == "inherit" {
		if d.Model != nil || len(d.Models) > 0 || len(d.Routes) > 0 || d.SourceKind != "" || d.SourceChannelID != 0 {
			return nil, fmt.Errorf("inherited Codex definitions use sourceModelId, snapshotVersion and overrides, not fixed profiles or routes")
		}
		if d.SnapshotVersion != "" && !validCodexSnapshotVersion(d.SnapshotVersion) {
			return nil, fmt.Errorf("invalid Codex snapshotVersion")
		}
		if _, exists := d.Overrides["slug"]; exists {
			return nil, fmt.Errorf("use sourceModelId to select a Codex profile; slug is assigned by the gateway")
		}
		return &d, nil
	}
	if d.Mode != "" || d.Overrides != nil {
		return nil, fmt.Errorf("invalid Codex definition mode")
	}
	if len(d.ClientVersion) > 32 || !definitionVersion.MatchString(d.ClientVersion) {
		return nil, fmt.Errorf("Codex definition requires a valid clientVersion")
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
		official := profile.SourceKind == "official_snapshot" && validCodexSnapshotVersion(profile.SnapshotVersion) && profile.SourceChannelID == 0
		if isPublicCatalogKind(profile.SourceKind) {
			digest, err := hex.DecodeString(profile.SourceSHA256)
			official = err == nil && len(digest) == 32 && profile.SourceChannelID == 0
		}
		if id == "" || (!official && profile.SourceChannelID <= 0) || profile.SourceModelID == "" {
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
		if len(profile.Overrides) > 0 {
			if _, err := applyCodexProfileOverrides(profile.Model, profile.Overrides); err != nil {
				return nil, err
			}
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

// The saved model profile supplies the baseline for every caller-visible route.
// Saved route entries only refine capabilities for the channel/model pairs they match.
func (d *codexDefinition) catalogModel(ctx context.Context, m ModelFacade, channels map[int]*Channel, fixedDefinition bool) (map[string]any, error) {
	// ClientVersion records import provenance, not a client allowlist. Schema
	// validation and declared model capabilities are independent of version equality.
	canonical := d.Model
	sourceKind := d.SourceKind
	var catalogDefaults map[string]any
	explicit := d.Overrides
	source := ChannelModelKey{ChannelID: d.SourceChannelID, ModelID: d.SourceModelID}
	target := m.TargetID
	if target == "" {
		target = m.ID
	}
	if len(d.Models) > 0 {
		profile, ok := d.Models[target]
		if !ok {
			return nil, fmt.Errorf("unconfirmed Codex public model %s", target)
		}
		canonical = profile.Model
		sourceKind = profile.SourceKind
		catalogDefaults = profile.CatalogDefaults
		explicit = profile.Overrides
		source = ChannelModelKey{ChannelID: profile.SourceChannelID, ModelID: profile.SourceModelID}
	}
	if fixedDefinition {
		var err error
		canonical, explicit, err = fixedCodexModelFields(canonical, catalogDefaults, explicit)
		if err != nil {
			return nil, err
		}
	}
	if page := codexModelPageFields(canonical, m.ConfiguredModel); len(page) > 0 {
		canonical = maps.Clone(canonical)
		maps.Copy(canonical, page)
		for field := range explicit {
			page[field] = canonical[field]
		}
		explicit = page
	}
	bindings := make(map[ChannelModelKey]codexDefinitionRoute)
	for _, r := range d.Routes {
		bindings[ChannelModelKey{ChannelID: r.ChannelID, ModelID: r.ModelID}] = r
	}
	var variants []map[string]any
	var tierRoutes []codexTierRoute
	requiresResponses := codexProfileRequiresResponses(canonical, nil)
	for _, connection := range m.Connections {
		ch := channels[connection.Channel.ID]
		if ch == nil {
			continue
		}
		for _, entry := range connection.Models {
			chatEndpoint, responsesEndpoint := codexRouteProtocols(ctx, ch, entry, m.TargetID)
			if !chatEndpoint || (requiresResponses && !responsesEndpoint) {
				continue
			}
			key := ChannelModelKey{ChannelID: connection.Channel.ID, ModelID: entry.ActualModel}
			r := bindings[key]
			if sourceKind == importedCatalogKind {
				tierRoutes = append(tierRoutes, importedCodexRoute(ch, target, entry.ActualModel, responsesEndpoint, canonical, r))
				continue
			}
			variant := maps.Clone(canonical)
			limited := maps.Clone(canonical)
			for field, value := range r.Capabilities {
				if codexReasoningProfileField(field) || field == "truncation_policy" {
					continue
				}
				limited[field] = value
				if fixedDefinition {
					// The saved route is authoritative for its declared capabilities.
					// Security requirements and picker tiers still use the model baseline.
					switch field {
					case "node_repl_auto_review_required", "node_repl_disabled", "service_tiers", "additional_speed_tiers", "default_service_tier":
					default:
						variant[field] = value
					}
				}
			}
			// Route context values have their own precedence (including values
			// larger than the base profile); keep the existing context contract.
			if r.Capabilities != nil {
				for _, field := range codexContextFields {
					if value, ok := r.Capabilities[field]; ok {
						variant[field], limited[field] = value, value
					} else {
						delete(variant, field)
						delete(limited, field)
					}
				}
			}
			// Truncation controls local tool output, not an upstream context limit.
			// Keep accepting old route snapshots but use the confirmed client policy.
			limited["truncation_policy"] = canonical["truncation_policy"]
			normalizeCodexWireDefaults(variant)
			normalizeCodexWireDefaults(limited)
			inputs := []map[string]any{variant, limited}
			if len(r.Limits) > 0 {
				explicit := maps.Clone(limited)
				maps.Copy(explicit, r.Limits)
				inputs = append(inputs, explicit)
			}
			effective, err := mergeCodexModels(m.ID, inputs)
			if err != nil {
				return nil, err
			}
			tierRoute := codexTierRoute{channel: ch, responses: responsesEndpoint, capabilities: r.Capabilities, limits: r.Limits}
			if key == source {
				// Older fixed definitions stored this route's tier declaration on the profile itself.
				_, services := r.Capabilities["service_tiers"]
				_, speeds := r.Capabilities["additional_speed_tiers"]
				if !services && !speeds {
					tierRoute.capabilities = canonical
				}
			}
			tierRoutes = append(tierRoutes, tierRoute)
			if isPublicCatalogKind(d.SourceKind) && !fixedDefinition {
				variants = append(variants, capPublicCodexRouteContext(effective, connection.Channel.Settings, entry.ActualModel))
			} else {
				variants = append(variants, applyCodexContextSettings(effective, nil, connection.Channel.Settings, entry.ActualModel))
			}
		}
	}
	if len(tierRoutes) == 0 {
		return nil, fmt.Errorf("no visible route")
	}
	var result map[string]any
	var err error
	if sourceKind == importedCatalogKind {
		result, err = mergeImportedCodexRoutes(m.ID, canonical, tierRoutes)
	} else {
		result, err = mergeCodexModels(m.ID, variants)
	}
	if err != nil {
		return nil, err
	}
	// Missing context metadata on a manual route must not erase known windows
	// or compaction thresholds from other caller-visible routes.
	if fixedDefinition && sourceKind != importedCatalogKind {
		for _, field := range []string{"context_window", "max_context_window", "auto_compact_token_limit"} {
			var bound float64
			for _, variant := range variants {
				if value, ok := variant[field].(float64); ok && value > 0 && (bound == 0 || value < bound) {
					bound = value
				}
			}
			if bound > 0 {
				result[field] = bound
			}
		}
	}
	applyCodexRouteTiers(result, canonical, tierRoutes)
	return finalizeCodexModelFields(result, canonical, explicit, m.ConfiguredModel.ModelCard), nil
}

// Preview is read-only. UpdateModel saves the selected model profiles and any
// route-specific capability overrides.
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
		associations := EffectiveModelAssociationsForRequest(svc.modelSettingsOrDefault(ctx), m, publicID)
		associations = slices.DeleteFunc(slices.Clone(associations), excludesCodexRequests)
		matches, queryErr := svc.QueryModelChannelConnectionsForModel(ctx, associations, publicID)
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
	sources, snapshots := service.fetchCatalogInputs(ctx, connections, channels, version, map[string]bool{"": true})
	snapshot := snapshots[""]
	d := codexDefinition{ClientVersion: version, Models: make(map[string]codexModelProfile)}
	for _, publicID := range PublicModelIDs(m) {
		facade := ModelFacade{ID: publicID, TargetID: publicID, ConfiguredModel: m, Connections: byPublicID[publicID]}
		profile, _, err := selectCodexProfile(ctx, facade, facade.Connections, sources, channels, snapshot, nil)
		if err != nil {
			continue
		}
		saved := *profile
		saved.CatalogDefaults = codexRouteCapabilities(saved.Model)
		d.Models[publicID] = saved
		d.SourceDifferences = append(d.SourceDifferences, codexProfileSourceDifferences(publicID, profile, byPublicID[publicID], sources)...)
		if d.Model == nil {
			d.Model = profile.Model
			d.SourceChannelID = profile.SourceChannelID
			d.SourceModelID = profile.SourceModelID
			d.SourceKind = profile.SourceKind
			d.SnapshotVersion = profile.SnapshotVersion
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
