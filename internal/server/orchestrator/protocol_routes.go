package orchestrator

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type ProtocolRoute struct {
	ChannelID   int                `json:"channelID"`
	ChannelName string             `json:"channelName"`
	ActualModel string             `json:"actualModel"`
	APIFormat   string             `json:"apiFormat"`
	State       string             `json:"state"`
	Reason      string             `json:"reason"`
	Condition   *objects.Condition `json:"condition,omitempty"`
}

type ProtocolRouteSummary struct {
	ModelID     string `json:"modelID"`
	Name        string `json:"name"`
	Native      int    `json:"native"`
	Converted   int    `json:"converted"`
	Conditional int    `json:"conditional"`
	Excluded    int    `json:"excluded"`
}

// InspectProtocolRoutes is a structural read: no key selection, health mutation,
// quota reservation or upstream request. Matching and endpoint choice are shared
// with the live selector. Request-dependent conditions remain explicit.
func InspectProtocolRoutes(ctx context.Context, settings *biz.SystemModelSettings, model *ent.Model, requestID string, format llm.APIFormat, channels []*biz.Channel) []*ProtocolRoute {
	matches := biz.MatchAssociationsForModel(biz.EffectiveModelAssociationsForRequest(settings, model, requestID), channels, requestID)
	return InspectMatchedProtocolRoutes(ctx, requestID, format, matches)
}

// InspectMatchedProtocolRoutes inspects only the associations and channels
// supplied by the caller, including a public directory's key-scoped matches.
func InspectMatchedProtocolRoutes(ctx context.Context, requestID string, format llm.APIFormat, matches []*biz.AssociationMatch) []*ProtocolRoute {
	req := &llm.Request{Model: requestID, APIFormat: format, RequestType: llm.RequestTypeChat}
	routes := make(map[string][]*ProtocolRoute)
	settled := make(map[string]bool)
	for _, match := range matches {
		for _, conn := range match.Connections {
			ch := &biz.Channel{Channel: conn.Channel}
			for _, entry := range conn.Models {
				key := strconv.Itoa(ch.ID) + "\x00" + entry.ActualModel
				if settled[key] {
					continue
				}
				selected, reason := selectChannelAPIFormat(ctx, ch, []biz.ChannelModelEntry{entry}, req, requestID)
				route := &ProtocolRoute{ChannelID: ch.ID, ChannelName: ch.Name, ActualModel: entry.ActualModel, APIFormat: selected, State: "native"}
				if ch.Status != channel.StatusEnabled {
					reason = "channel_disabled"
				}
				if reason != "" {
					route.State, route.Reason = "excluded", reason
				} else if objects.ChatProtocol(format) != objects.ChatProtocol(llm.APIFormat(selected)) {
					route.State = "converted"
				}
				if when := match.Association.When; when != nil && when.Enabled && when.Condition != nil && route.State != "excluded" {
					known, value := objects.EvaluateProtocolCondition(when.Condition, format.String())
					if known && !value {
						route.State, route.Reason, route.Condition = "excluded", "request_condition", when.Condition
					} else if !known {
						route.State, route.Reason, route.Condition = "conditional", "request_condition", when.Condition
					}
				}
				previous := routes[key]
				if route.State == "excluded" {
					if len(previous) == 0 {
						routes[key] = []*ProtocolRoute{route}
					}
					continue
				}
				if len(previous) > 0 && previous[0].State == "excluded" {
					previous = nil
				}
				// An unconditional available alias wins in the same order as live
				// aggregation. Earlier unknown conditions can still select another
				// protocol, so retain those branches instead of inventing a winner.
				settled[key] = route.State != "conditional"
				index := slices.IndexFunc(previous, func(p *ProtocolRoute) bool { return p.APIFormat == route.APIFormat })
				if index < 0 {
					previous = append(previous, route)
				} else if settled[key] {
					previous[index] = route
				} else {
					conditions := []objects.Condition{*previous[index].Condition}
					if previous[index].Condition.Type == objects.ConditionTypeGroup && previous[index].Condition.Logic == "or" {
						conditions = slices.Clone(previous[index].Condition.Conditions)
					}
					previous[index].Condition = &objects.Condition{Type: objects.ConditionTypeGroup, Logic: "or", Conditions: append(conditions, *route.Condition)}
				}
				routes[key] = previous
			}
		}
	}
	result := make([]*ProtocolRoute, 0, len(routes))
	for _, branches := range routes {
		result = append(result, branches...)
	}
	slices.SortFunc(result, func(a, b *ProtocolRoute) int {
		if rank := protocolRouteRank(a.State) - protocolRouteRank(b.State); rank != 0 {
			return rank
		}
		if cmp := strings.Compare(a.ChannelName, b.ChannelName); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a.ActualModel, b.ActualModel); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.APIFormat, b.APIFormat)
	})
	return result
}

func protocolRouteRank(state string) int {
	switch state {
	case "native":
		return 0
	case "converted":
		return 1
	case "conditional":
		return 2
	default:
		return 3
	}
}

func SummarizeProtocolRoutes(id, name string, routes []*ProtocolRoute) *ProtocolRouteSummary {
	s := &ProtocolRouteSummary{ModelID: id, Name: name}
	// Counts are channels, not model aliases. A channel's best usable route wins.
	best := map[int]string{}
	for _, route := range routes {
		previous, ok := best[route.ChannelID]
		if !ok || protocolRouteRank(route.State) < protocolRouteRank(previous) {
			best[route.ChannelID] = route.State
		}
	}
	for _, state := range best {
		switch state {
		case "native":
			s.Native++
		case "converted":
			s.Converted++
		case "conditional":
			s.Conditional++
		default:
			s.Excluded++
		}
	}
	return s
}
