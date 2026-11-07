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
// formats are used. Falls back to the first capable endpoint, then the first endpoint.
func SelectAPIFormat(endpoints []objects.ChannelEndpoint, req *llm.Request) string {
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

func requiresResponsesToolProtocol(req *llm.Request) bool {
	for _, tool := range req.Tools {
		if tool.Type == llm.ToolTypeResponsesCustomTool || tool.ResponseCustomTool != nil {
			return true
		}
	}
	return containsResponseCustomToolMessages(req.Messages)
}

func requiresExplicitEndpoint(requestType llm.RequestType) bool {
	return requestType == llm.RequestTypeAlphaSearch || requestType == llm.RequestTypeDecisions || requestType == llm.RequestTypeSystemOne
}
func applyForcedAPIFormatsForRequest(ctx context.Context, ch *biz.Channel, entries []biz.ChannelModelEntry, model string, requestType llm.RequestType, endpoints []objects.ChannelEndpoint) []objects.ChannelEndpoint {
	forced := biz.ForcedAPIFormatsForCandidate(ch, entries, model)
	filtered := biz.ApplyForcedAPIFormats(ctx, ch, entries, model, endpoints)
	if len(forced) > 0 && requiresExplicitEndpoint(requestType) && len(biz.FilterEndpointsByAPIFormats(endpoints, forced)) == 0 {
		return nil
	}
	return filtered
}
