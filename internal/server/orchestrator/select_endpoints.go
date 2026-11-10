package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

// SelectAPIFormat selects the most appropriate APIFormat from a channel's resolved endpoints
// based on the request type and inbound API format. Prefers an endpoint whose API format
// matches the inbound request format so that pass-through can be enabled when identical
// formats are used. For ordinary endpoint surfaces, Responses HTTP and WebSocket are
// transport variants of the same protocol family, so either can be preferred before
// falling back to an unrelated format. Finally, falls back to the first capable endpoint,
// then the first endpoint.
func SelectAPIFormat(endpoints []objects.ChannelEndpoint, req *llm.Request) string {
	return selectAPIFormat(endpoints, req, true)
}

// selectForcedAPIFormat selects from endpoints already narrowed and ordered by
// ModelProtocols. The configured order is authoritative: use an exact inbound match when
// present, otherwise the first capable configured format. Implicit Responses transport
// upgrades must not reorder an explicit operator configuration.
func selectForcedAPIFormat(endpoints []objects.ChannelEndpoint, req *llm.Request) string {
	return selectAPIFormat(endpoints, req, false)
}

func selectEndpointAPIFormat(
	endpoints []objects.ChannelEndpoint,
	req *llm.Request,
	forced bool,
) string {
	if forced {
		return selectForcedAPIFormat(endpoints, req)
	}

	return SelectAPIFormat(endpoints, req)
}

func selectAPIFormat(
	endpoints []objects.ChannelEndpoint,
	req *llm.Request,
	allowResponsesTransportFamily bool,
) string {
	if len(endpoints) == 0 {
		return ""
	}

	preferredFormat := string(req.APIFormat)
	allowed := llm.CapableAPIFormats(req.RequestType)
	if requiresResponsesToolProtocol(req) {
		// Custom tool definitions and history cannot survive a Chat Completions
		// conversion. Keep them on Responses instead of silently deleting them.
		for _, ep := range endpoints {
			_, capable := allowed[ep.APIFormat]
			if isResponsesFormat(llm.APIFormat(ep.APIFormat)) && (allowed == nil || capable) {
				return ep.APIFormat
			}
		}
		return ""
	}

	if allowed != nil {
		if preferredFormat != "" {
			for _, ep := range endpoints {
				if _, ok := allowed[ep.APIFormat]; ok && ep.APIFormat == preferredFormat {
					return ep.APIFormat
				}
			}

			if allowResponsesTransportFamily {
				if compatibleFormat := compatibleResponsesAPIFormat(req.APIFormat); compatibleFormat != "" {
					for _, ep := range endpoints {
						if _, ok := allowed[ep.APIFormat]; ok && ep.APIFormat == compatibleFormat {
							return ep.APIFormat
						}
					}
				}
			}
		}

		for _, ep := range endpoints {
			if _, ok := allowed[ep.APIFormat]; ok {
				return ep.APIFormat
			}
		}

		if requiresExplicitEndpoint(req.RequestType) {
			return ""
		}
	}

	return endpoints[0].APIFormat
}

func compatibleResponsesAPIFormat(format llm.APIFormat) string {
	switch format {
	case llm.APIFormatOpenAIResponse:
		return llm.APIFormatOpenAIResponseWebSocket.String()
	case llm.APIFormatOpenAIResponseWebSocket:
		return llm.APIFormatOpenAIResponse.String()
	default:
		return ""
	}
}

func requiresExplicitEndpoint(requestType llm.RequestType) bool {
	return requestType == llm.RequestTypeAlphaSearch ||
		requestType == llm.RequestTypeDecisions ||
		requestType == llm.RequestTypeSystemOne
}

func requiresResponsesToolProtocol(req *llm.Request) bool {
	for _, tool := range req.Tools {
		if tool.Type == llm.ToolTypeResponsesCustomTool || tool.ResponseCustomTool != nil {
			return true
		}
	}
	return containsResponseCustomToolMessages(req.Messages)
}

// applyForcedAPIFormats uses the same configured ordering as catalog filtering.
func applyForcedAPIFormats(ctx context.Context, ch *biz.Channel, entries []biz.ChannelModelEntry, model string, endpoints []objects.ChannelEndpoint) ([]objects.ChannelEndpoint, bool) {
	forced := biz.ForcedAPIFormatsForCandidate(ch, entries, model)
	active := len(biz.FilterEndpointsByAPIFormats(endpoints, forced)) > 0
	return biz.ApplyForcedAPIFormats(ctx, ch, entries, model, endpoints), active
}
func applyForcedAPIFormatsForRequest(ctx context.Context, ch *biz.Channel, entries []biz.ChannelModelEntry, model string, requestType llm.RequestType, endpoints []objects.ChannelEndpoint) ([]objects.ChannelEndpoint, bool) {
	forced := biz.ForcedAPIFormatsForCandidate(ch, entries, model)
	filtered, active := applyForcedAPIFormats(ctx, ch, entries, model, endpoints)
	if len(forced) > 0 && requiresExplicitEndpoint(requestType) && !active {
		return nil, true
	}
	return filtered, active
}
