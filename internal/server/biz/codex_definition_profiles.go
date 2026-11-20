package biz

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// A regex publishes concrete IDs whose instructions and local tool policies may
// differ. Client profiles remain explicit; actual routing must still support
// their selected tool protocols.
type codexModelProfile struct {
	SourceSHA256    string         `json:"sourceSHA256,omitempty"`
	SourceKind      string         `json:"sourceKind,omitempty"`
	SnapshotVersion string         `json:"snapshotVersion,omitempty"`
	SourceChannelID int            `json:"sourceChannelId"`
	SourceModelID   string         `json:"sourceModelId"`
	Model           map[string]any `json:"model"`
	CatalogDefaults map[string]any `json:"catalogDefaults,omitempty"`
	Overrides       map[string]any `json:"overrides,omitempty"`
}

type codexSourceDifference struct {
	PublicModelID string   `json:"publicModelId"`
	ChannelID     int      `json:"channelId"`
	ModelID       string   `json:"modelId"`
	Fields        []string `json:"fields"`
}

// Preview selects a complete client profile for each public ID and exposes all
// instruction/tool-policy differences for explicit administrator confirmation.
// Provider capabilities still stay on their individual route snapshots.
func previewCodexModelProfile(publicID string, connections []*ModelChannelConnection, sources map[int]map[string]map[string]any) (*codexModelProfile, []codexSourceDifference) {
	connections = orderedCodexConnections(connections)
	var selected *codexModelProfile
	for _, connection := range connections {
		for _, entry := range connection.Models {
			metadata := sources[connection.Channel.ID][entry.ActualModel]
			if metadata == nil {
				continue
			}
			candidate := &codexModelProfile{SourceChannelID: connection.Channel.ID, SourceModelID: entry.ActualModel, Model: metadata}
			if selected == nil || (selected.SourceModelID != publicID && entry.ActualModel == publicID) ||
				(selected.SourceModelID == entry.ActualModel && codexReasoningChoiceCount(metadata) > codexReasoningChoiceCount(selected.Model)) {
				selected = candidate
			}
		}
	}
	if selected == nil {
		return nil, nil
	}
	differences := codexProfileSourceDifferences(publicID, selected, connections, sources)
	selected.Model = maps.Clone(selected.Model)
	normalizeCodexWireDefaults(selected.Model)
	if _, present := selected.Model["experimental_supported_tools"]; !present {
		selected.Model["experimental_supported_tools"] = []any{}
	}
	return selected, differences
}

func codexProfileSourceDifferences(publicID string, selected *codexModelProfile, connections []*ModelChannelConnection, sources map[int]map[string]map[string]any) []codexSourceDifference {
	var differences []codexSourceDifference
	seen := make(map[ChannelModelKey]bool)
	for _, connection := range orderedCodexConnections(connections) {
		for _, entry := range connection.Models {
			key := ChannelModelKey{ChannelID: connection.Channel.ID, ModelID: entry.ActualModel}
			metadata := sources[key.ChannelID][key.ModelID]
			if metadata == nil || seen[key] {
				continue
			}
			seen[key] = true
			var fields []string
			for _, field := range []string{"base_instructions", "model_messages", "shell_type", "apply_patch_tool_type", "web_search_tool_type", "tool_mode", "multi_agent_version", "truncation_policy"} {
				if !reflect.DeepEqual(selected.Model[field], metadata[field]) {
					fields = append(fields, field)
				}
			}
			if len(fields) > 0 {
				differences = append(differences, codexSourceDifference{PublicModelID: publicID, ChannelID: key.ChannelID, ModelID: key.ModelID, Fields: fields})
			}
		}
	}
	return differences
}

// Prefer a complete reasoning profile over a proxy's default-only declaration
// for the same actual model. Equal coverage retains configured source precedence.
func codexReasoningChoiceCount(metadata map[string]any) int {
	levels, _ := metadata["supported_reasoning_levels"].([]any)
	seen := make(map[string]bool)
	for _, item := range levels {
		level, _ := item.(map[string]any)
		effort, _ := level["effort"].(string)
		if effort != "" && effort != "none" {
			seen[effort] = true
		}
	}
	return len(seen)
}

// Stable source precedence follows configured association priority, then channel
// ID. An exact actual-model match still takes precedence over an alias profile.
func orderedCodexConnections(connections []*ModelChannelConnection) []*ModelChannelConnection {
	ordered := slices.Clone(connections)
	for i, connection := range ordered {
		copyConnection := *connection
		copyConnection.Models = slices.Clone(connection.Models)
		slices.SortStableFunc(copyConnection.Models, func(a, b ChannelModelEntry) int {
			return cmp.Compare(a.ActualModel, b.ActualModel)
		})
		ordered[i] = &copyConnection
	}
	slices.SortStableFunc(ordered, func(a, b *ModelChannelConnection) int {
		if order := cmp.Compare(a.Priority, b.Priority); order != 0 {
			return order
		}
		return cmp.Compare(a.Channel.ID, b.Channel.ID)
	})
	return ordered
}

func selectCodexProfile(ctx context.Context, m ModelFacade, connections []*ModelChannelConnection, sources map[int]map[string]map[string]any, channels map[int]*Channel, snapshot *codexSnapshot, inherited *codexDefinition) (*codexModelProfile, []*ModelChannelConnection, error) {
	target := m.TargetID
	var overrides map[string]any
	if inherited != nil {
		overrides = inherited.Overrides
	}
	if inherited != nil && inherited.SourceModelID != "" {
		target = inherited.SourceModelID
	}
	// A richer profile must remain usable by the caller. Metadata can be shared
	// from an inaccessible channel, but its tool protocol cannot grant a route.
	canUseResponses := false
	for _, connection := range m.Connections {
		if ch := channels[connection.Channel.ID]; ch != nil {
			for _, entry := range connection.Models {
				if _, native := codexRouteProtocols(ctx, ch, entry, m.TargetID); native {
					canUseResponses = true
					break
				}
			}
		}
		if canUseResponses {
			break
		}
	}
	// A source can describe an actual model even when that particular channel
	// is not configured to route it. Metadata discovery must use the complete
	// source catalog, while inference admission continues to use m.Connections.
	needed := map[string]bool{target: true}
	for _, connection := range connections {
		for _, entry := range connection.Models {
			needed[entry.ActualModel] = true
		}
	}
	var metadataConnections []*ModelChannelConnection
	var profileConnections []*ModelChannelConnection
	hasMetadata := false
	for _, connection := range connections {
		candidate := *connection
		candidate.Models = nil
		profileCandidate := candidate
		for actualModel := range needed {
			metadata := sources[connection.Channel.ID][actualModel]
			if metadata == nil {
				continue
			}
			hasMetadata = true
			candidate.Models = append(candidate.Models, ChannelModelEntry{ActualModel: actualModel})
			if codexProfileRequiresResponses(metadata, overrides) && !canUseResponses {
				continue
			}
			profileCandidate.Models = append(profileCandidate.Models, ChannelModelEntry{ActualModel: actualModel})
		}
		metadataConnections = append(metadataConnections, &candidate)
		profileConnections = append(profileConnections, &profileCandidate)
	}
	connections = metadataConnections
	profile, _ := previewCodexModelProfile(target, profileConnections, sources)
	if inherited != nil && inherited.SourceModelID != "" && profile != nil && profile.SourceModelID != target {
		profile = nil
	}
	if official := snapshotCodexProfile(snapshot, target); official != nil {
		hasMetadata = true
		pinned := inherited != nil && inherited.SnapshotVersion != ""
		compatible := !codexProfileRequiresResponses(official.Model, overrides) || canUseResponses
		if official.SourceKind == importedCatalogKind && !compatible {
			return nil, nil, fmt.Errorf("no visible route")
		}
		if compatible && (pinned || isPublicCatalogKind(official.SourceKind) || profile == nil || profile.SourceModelID != target || codexReasoningChoiceCount(official.Model) > codexReasoningChoiceCount(profile.Model)) {
			profile = official
		}
	}
	if inherited != nil && inherited.SnapshotVersion != "" && (profile == nil || profile.SourceKind != "official_snapshot") {
		return nil, nil, fmt.Errorf("pinned official Codex profile unavailable for %s", target)
	}
	if profile == nil {
		if hasMetadata {
			return nil, nil, fmt.Errorf("no visible route")
		}
		return nil, nil, fmt.Errorf("%w for model %s", errCodexMetadataUnavailable, m.ID)
	}
	return profile, connections, nil
}

func automaticCodexModel(ctx context.Context, m ModelFacade, connections []*ModelChannelConnection, sources map[int]map[string]map[string]any, channels map[int]*Channel, version string, snapshot *codexSnapshot, inherited *codexDefinition) (map[string]any, error) {
	profile, metadataConnections, err := selectCodexProfile(ctx, m, connections, sources, channels, snapshot, inherited)
	if err != nil {
		return nil, err
	}
	base := profile.Model
	if inherited != nil {
		patched, err := applyCodexProfileOverrides(base, inherited.Overrides)
		if err != nil {
			return nil, err
		}
		base = patched
	}
	// Use all source metadata for actual-model capabilities, even when its client
	// tools cannot serve this caller. Profile filtering must not erase an alias's
	// context limit. Explicit associations can include plain-list-only providers.
	byActualModel := make(map[string]map[string]any)
	for _, connection := range orderedCodexConnections(metadataConnections) {
		for _, entry := range connection.Models {
			metadata := sources[connection.Channel.ID][entry.ActualModel]
			if metadata != nil && byActualModel[entry.ActualModel] == nil {
				byActualModel[entry.ActualModel] = metadata
			}
		}
	}
	d := codexDefinition{ClientVersion: version, SourceKind: profile.SourceKind, Model: base}
	if inherited != nil {
		d.Overrides = inherited.Overrides
	}
	for _, connection := range m.Connections {
		for _, entry := range connection.Models {
			metadata := sources[connection.Channel.ID][entry.ActualModel]
			capabilities := codexRouteCapabilities(metadata)
			if metadata == nil {
				// Another source may describe the alias's context, but cannot
				// attest what this route supports.
				fallback := byActualModel[entry.ActualModel]
				if fallback == nil {
					fallback = base
					if actual := snapshotCodexProfile(snapshot, entry.ActualModel); actual != nil {
						fallback = actual.Model
					}
				}
				for _, field := range codexContextFields {
					if value, exists := fallback[field]; exists {
						capabilities[field] = value
					}
				}
			}
			if isPublicCatalogKind(profile.SourceKind) && entry.ActualModel == profile.SourceModelID {
				for _, field := range []string{"context_window", "max_context_window"} {
					if value, exists := base[field]; exists {
						capabilities[field] = value
					}
				}
			}
			if inherited != nil {
				// Context overrides have separate Axon precedence. All other
				// model overrides already apply to the base before route limits.
				for _, field := range codexContextFields {
					if _, overridden := inherited.Overrides[field]; !overridden {
						continue
					}
					if value, exists := base[field]; exists {
						capabilities[field] = value
					} else {
						delete(capabilities, field)
					}
				}
			}
			d.Routes = append(d.Routes, codexDefinitionRoute{
				ChannelID: connection.Channel.ID, ModelID: entry.ActualModel,
				Capabilities: capabilities,
			})
		}
	}
	result, err := d.catalogModel(ctx, m, channels, false)
	if err == nil && isPublicCatalogKind(profile.SourceKind) {
		result["axonhub_catalog"] = map[string]any{"source": profile.SourceKind, "sha256": profile.SourceSHA256, "client_version": version}
	}
	if err == nil && profile.SourceKind == "official_snapshot" {
		result["axonhub_catalog"] = map[string]any{"source": profile.SourceKind, "version": profile.SnapshotVersion, "client_version": version}
	}
	return result, err
}

func codexProfileRequiresResponses(profile, overrides map[string]any) bool {
	patchTool, toolMode := profile["apply_patch_tool_type"], profile["tool_mode"]
	if value, exists := overrides["apply_patch_tool_type"]; exists {
		patchTool = value
	}
	if value, exists := overrides["tool_mode"]; exists {
		toolMode = value
	}
	return patchTool == "freeform" || toolMode == "code_mode_only"
}

func codexRouteProtocols(ctx context.Context, ch *Channel, entry ChannelModelEntry, target string) (chat, responses bool) {
	var policy *objects.ChannelProtocolPolicy
	if ch.Settings != nil {
		policy = ch.Settings.ProtocolPolicy
	}
	if !policy.AllowsClient(llm.APIFormatOpenAIResponse) {
		return false, false
	}
	endpoints := ApplyForcedAPIFormats(ctx, ch, []ChannelModelEntry{entry}, target, ch.ResolveEndpoints())
	capable := llm.CapableAPIFormats(llm.RequestTypeChat)
	for _, ep := range endpoints {
		if _, ok := capable[ep.APIFormat]; ok && policy.AllowsConversion(llm.APIFormatOpenAIResponse, llm.APIFormat(ep.APIFormat)) {
			chat = true
			responses = responses || isResponsesEndpoint(ep.APIFormat)
		}
	}
	return chat, responses
}

// These are client model metadata, not per-route admission constraints. Keep
// reading them in old route snapshots, but only explicit Limits may narrow them.
func codexReasoningProfileField(field string) bool {
	switch field {
	case "supported_reasoning_levels", "default_reasoning_level", "supports_reasoning_effort_updates":
		return true
	default:
		return false
	}
}

// Ultra sends the profile's underlying effort. An explicit limit that removes
// that effort must also remove Ultra; an unknown mapping is not guessed.
func constrainCodexReasoningProfile(result, profile map[string]any) {
	effort, _ := profile["multi_agent_reasoning_effort"].(string)
	if effort == "" || effort == "ultra" {
		return
	}
	profileLevels, _ := profile["supported_reasoning_levels"].([]any)
	if !slices.ContainsFunc(profileLevels, func(item any) bool {
		level, _ := item.(map[string]any)
		return level["effort"] == effort
	}) {
		return
	}
	levels, _ := result["supported_reasoning_levels"].([]any)
	for _, item := range levels {
		level, _ := item.(map[string]any)
		if level["effort"] == effort {
			return
		}
	}
	result["supported_reasoning_levels"] = slices.DeleteFunc(levels, func(item any) bool {
		level, _ := item.(map[string]any)
		return level["effort"] == "ultra"
	})
	if result["default_reasoning_level"] == "ultra" {
		result["default_reasoning_level"] = nil
	}
}
