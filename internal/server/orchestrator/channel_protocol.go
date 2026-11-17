package orchestrator

import (
	"context"
	"fmt"

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
	endpoints, forced := applyForcedAPIFormatsForRequest(ctx, ch, entries, requestModel, req.RequestType, ch.ResolveEndpoints())
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
