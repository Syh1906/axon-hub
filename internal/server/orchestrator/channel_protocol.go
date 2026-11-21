package orchestrator

import (
	"context"
	"fmt"
	"slices"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
)

func channelProtocolPolicy(ch *biz.Channel) *objects.ChannelProtocolPolicy {
	if ch == nil || ch.Channel == nil || ch.Settings == nil {
		return nil
	}
	return ch.Settings.ProtocolPolicy
}

// channelProtocolRejection is shared by selection, inspection and final dispatch.
// Policy applies to chat requests; dedicated image/audio/etc APIs are unaffected.
func channelProtocolRejection(ch *biz.Channel, req *llm.Request, target llm.APIFormat) string {
	if target != "" && ch != nil && ch.Channel != nil && ch.Settings != nil && req != nil {
		disabled := ch.Settings.DisabledEndpoints
		if slices.Contains(disabled, string(target)) && !(target == llm.APIFormatOpenAIResponse && ch.UsesAdaptedResponsesTool(req)) {
			return "no_compatible_endpoint"
		}
		if req.RequestType != llm.RequestTypeChat && req.RequestType != "" && slices.Contains(disabled, string(req.APIFormat)) {
			// Only a real alternative can serve a disabled native endpoint.
			// OpenAI Images uses the inbound operation to build the final URL even
			// when selection nominates a different OpenAI Images transformer.
			if _, ok := llm.CapableAPIFormats(req.RequestType)[string(target)]; !ok || req.RequestType == llm.RequestTypeImage && (target == llm.APIFormatOpenAIImageGeneration || target == llm.APIFormatOpenAIImageEdit || target == llm.APIFormatOpenAIImageVariation) {
				return "no_compatible_endpoint"
			}
		}
		if isResponsesFormat(target) && ch.BlocksResponsesWebSearch(req) {
			return "web_search_tool_not_allowed"
		}
	}
	if req == nil || (req.RequestType != "" && req.RequestType != llm.RequestTypeChat && req.RequestType != llm.RequestTypeTokenCount) {
		return ""
	}
	policy := channelProtocolPolicy(ch)
	clientFormat := req.APIFormat
	if req.RequestType == llm.RequestTypeTokenCount {
		clientFormat = llm.APIFormatAnthropicMessage
		if target == llm.APIFormatAnthropicTokenCount {
			target = llm.APIFormatAnthropicMessage
		}
		if target != "" && target != llm.APIFormatAnthropicMessage {
			return "no_compatible_endpoint"
		}
		if target == llm.APIFormatAnthropicMessage {
			out := selectOutboundForCandidate(&ChannelModelsCandidate{Channel: ch, APIFormat: target.String()})
			if endpoint, ok := out.(interface{ ValidateTokenCountEndpoint() error }); ok && endpoint.ValidateTokenCountEndpoint() != nil {
				return "no_compatible_endpoint"
			}
		}
	}
	if !policy.AllowsClient(clientFormat) {
		return "client_protocol_denied"
	}
	if target == "" {
		return ""
	}
	if req.RequestType == llm.RequestTypeChat {
		if _, capable := llm.CapableAPIFormats(llm.RequestTypeChat)[target.String()]; !capable {
			return "no_compatible_endpoint"
		}
	}
	if !policy.AllowsConversion(clientFormat, target) {
		return "conversion_disabled"
	}
	if ch != nil && ch.Channel != nil && (ch.Type == channel.TypeDeepseek || ch.Type == channel.TypeDeepseekAnthropic) && target == llm.APIFormatOpenAIChatCompletion && req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		return "provider_output_constraint"
	}
	return ""
}

func requiresChannelProtocolSelection(ch *biz.Channel, req *llm.Request) bool {
	if ch != nil && ch.Channel != nil && ch.Settings != nil && len(ch.Settings.DisabledEndpoints) > 0 {
		return true
	}
	if req.RequestType == llm.RequestTypeChat || req.RequestType == llm.RequestTypeTokenCount {
		return true
	}
	policy := channelProtocolPolicy(ch)
	return req.RequestType == "" && policy != nil && (policy.SameProtocolOnly || len(policy.AllowedProtocols) > 0)
}

func selectChannelAPIFormat(ctx context.Context, ch *biz.Channel, entries []biz.ChannelModelEntry, req *llm.Request, requestModel string) (string, string) {
	if reason := channelProtocolRejection(ch, req, ""); reason != "" {
		return "", reason
	}
	resolved := ch.ResolveEndpoints()
	if ch.UsesAdaptedResponsesTool(req) && !slices.ContainsFunc(resolved, func(ep objects.ChannelEndpoint) bool { return ep.APIFormat == string(llm.APIFormatOpenAIResponse) }) {
		resolved = append(resolved, objects.ChannelEndpoint{APIFormat: string(llm.APIFormatOpenAIResponse)})
	}
	endpoints, forced := applyForcedAPIFormatsForRequest(ctx, ch, entries, requestModel, req.RequestType, resolved)
	allowed := make([]objects.ChannelEndpoint, 0, len(endpoints))
	reason := "no_compatible_endpoint"
	for _, ep := range endpoints {
		if rejected := channelProtocolRejection(ch, req, llm.APIFormat(ep.APIFormat)); rejected != "" {
			reason = rejected
			continue
		}
		allowed = append(allowed, ep)
	}
	format := selectEndpointAPIFormat(allowed, req, forced)
	if format == "" {
		return "", reason
	}
	return format, ""
}

func validateChannelProtocol(ch *biz.Channel, req *llm.Request, target llm.APIFormat) error {
	if reason := channelProtocolRejection(ch, req, target); reason != "" {
		return fmt.Errorf("%w: %w: channel protocol policy: %s (%s -> %s)", transformer.ErrUnsupportedConversion, transformer.ErrInvalidRequest, reason, req.APIFormat, target)
	}
	return nil
}
