package biz

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// A regex publishes concrete IDs whose instructions and local tool policies may
// differ. Client profiles remain explicit; actual routing must still support
// their selected tool protocols.
type codexModelProfile struct {
	SourceChannelID int            `json:"sourceChannelId"`
	SourceModelID   string         `json:"sourceModelId"`
	Model           map[string]any `json:"model"`
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
			if selected == nil || (selected.SourceModelID != publicID && entry.ActualModel == publicID) {
				selected = candidate
			}
		}
	}
	if selected == nil {
		return nil, nil
	}
	var differences []codexSourceDifference
	seen := make(map[ChannelModelKey]bool)
	for _, connection := range connections {
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
	return selected, differences
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

func automaticCodexModel(ctx context.Context, m ModelFacade, connections []*ModelChannelConnection, sources map[int]map[string]map[string]any, channels map[int]*Channel, version string) (map[string]any, error) {
	// A source can describe an actual model even when that particular channel
	// is not configured to route it. Metadata discovery must use the complete
	// source catalog, while inference admission continues to use m.Connections.
	needed := map[string]bool{m.TargetID: true}
	for _, connection := range connections {
		for _, entry := range connection.Models {
			needed[entry.ActualModel] = true
		}
	}
	var metadataConnections []*ModelChannelConnection
	for _, connection := range connections {
		candidate := *connection
		candidate.Models = nil
		for actualModel := range needed {
			if sources[connection.Channel.ID][actualModel] != nil {
				candidate.Models = append(candidate.Models, ChannelModelEntry{ActualModel: actualModel})
			}
		}
		metadataConnections = append(metadataConnections, &candidate)
	}
	connections = metadataConnections
	profile, _ := previewCodexModelProfile(m.TargetID, connections, sources)
	if profile == nil {
		return nil, fmt.Errorf("%w for model %s", errCodexMetadataUnavailable, m.ID)
	}
	// Prefer metadata for the exact actual model. Explicit model associations
	// also define aliases whose providers may expose only a plain model list.
	byActualModel := make(map[string]map[string]any)
	for _, connection := range orderedCodexConnections(connections) {
		for _, entry := range connection.Models {
			metadata := sources[connection.Channel.ID][entry.ActualModel]
			if metadata != nil && byActualModel[entry.ActualModel] == nil {
				byActualModel[entry.ActualModel] = metadata
			}
		}
	}
	d := codexDefinition{ClientVersion: version, Model: profile.Model}
	var eligible []*ModelChannelConnection
	for _, connection := range m.Connections {
		ch := channels[connection.Channel.ID]
		if ch == nil {
			// The enabled-channel snapshot can change after listing visible models.
			continue
		}
		copyConnection := *connection
		copyConnection.Models = nil
		for _, entry := range connection.Models {
			if profile.Model["apply_patch_tool_type"] == "freeform" || profile.Model["tool_mode"] == "code_mode_only" {
				endpoints := ApplyForcedAPIFormats(ctx, ch, []ChannelModelEntry{entry}, m.TargetID, ch.ResolveEndpoints())
				if !slices.ContainsFunc(endpoints, func(ep objects.ChannelEndpoint) bool {
					return ep.APIFormat == llm.APIFormatOpenAIResponse.String()
				}) {
					continue
				}
			}
			metadata := sources[connection.Channel.ID][entry.ActualModel]
			if metadata == nil {
				metadata = byActualModel[entry.ActualModel]
			}
			if metadata == nil {
				metadata = profile.Model
			}
			d.Routes = append(d.Routes, codexDefinitionRoute{
				ChannelID: connection.Channel.ID, ModelID: entry.ActualModel,
				Capabilities: codexRouteCapabilities(metadata),
			})
			copyConnection.Models = append(copyConnection.Models, entry)
		}
		if len(copyConnection.Models) > 0 {
			eligible = append(eligible, &copyConnection)
		}
	}
	m.Connections = eligible
	return d.catalogModel(m)
}
