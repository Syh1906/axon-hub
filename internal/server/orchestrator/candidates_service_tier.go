package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

type tierPreference int

const (
	tierExact tierPreference = iota
	tierConvertedExact
	tierUndeclared
	tierFallback
	tierPreferenceCount
)

func responsesTierFormat(format llm.APIFormat) bool {
	return format == llm.APIFormatOpenAIResponse || format == llm.APIFormatOpenAIResponseWebSocket
}

// Alternate aliases may map to the same upstream model through different
// protocols. Keep both until capability filtering has chosen a legal endpoint.
type tierModelKey struct {
	model  string
	format llm.APIFormat
}

func (c *ChannelModelsCandidate) tierModelKey(index int) tierModelKey {
	key := tierModelKey{model: c.Models[index].ActualModel}
	if settings := c.Channel.Settings; settings != nil && (settings.ResponsesTierRoutingEnabled || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported) {
		format := c.APIFormat
		if len(c.modelAPIFormats) == len(c.Models) {
			format = c.modelAPIFormats[index]
		}
		if responsesTierFormat(llm.APIFormat(format)) {
			key.format = llm.APIFormatOpenAIResponse
		} else if settings.ResponsesTierRoutingEnabled && llm.APIFormat(format) == llm.APIFormatOpenAIChatCompletion {
			key.format = llm.APIFormatOpenAIChatCompletion
		}
	}
	return key
}

func unsupportedResponsesTier(tier string) error {
	return &llm.ResponseError{StatusCode: http.StatusServiceUnavailable, Detail: llm.ErrorDetail{
		Code: "service_tier_unavailable", Type: "server_error", Message: fmt.Sprintf("当前模型没有可用的 %s 渠道。", tier),
	}}
}

func acceleratedChatRequest(req *llm.Request) bool {
	if req == nil || (req.RequestType != "" && req.RequestType != llm.RequestTypeChat) {
		return false
	}
	mode, _ := objects.NormalizeServiceTier(lo.FromPtr(req.ServiceTier))
	return mode == "fast" || mode == "ultrafast"
}

func usesTierRouting(req *llm.Request, candidates []*ChannelModelsCandidate) bool {
	if !acceleratedChatRequest(req) {
		return false
	}
	return slices.ContainsFunc(candidates, func(c *ChannelModelsCandidate) bool {
		settings := c.Channel.Settings
		return settings != nil && (settings.ResponsesTierRoutingEnabled || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported)
	})
}

// Historically, forced channels were ranked by tier only when a channel
// using the earlier supported-tier policy participated in the request.
func legacySupportPolicyPresent(candidates []*ChannelModelsCandidate) bool {
	return slices.ContainsFunc(candidates, func(c *ChannelModelsCandidate) bool {
		s := c.Channel.Settings
		return s != nil && !s.ResponsesTierRoutingEnabled && s.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported
	})
}

func rankResponsesTiers(ctx context.Context, req *llm.Request, candidates []*ChannelModelsCandidate) ([]*ChannelModelsCandidate, error) {
	mode, _ := objects.NormalizeServiceTier(lo.FromPtr(req.ServiceTier))
	legacySupport := legacySupportPolicyPresent(candidates)
	var groups [tierPreferenceCount][]*ChannelModelsCandidate
	for _, c := range candidates {
		var indices [tierPreferenceCount][]int
		for i := range c.Models {
			format := c.APIFormat
			if len(c.modelAPIFormats) == len(c.Models) {
				format = c.modelAPIFormats[i]
			} else if format == "" {
				format, _ = selectChannelAPIFormat(ctx, c.Channel, c.Models[i:i+1], req, req.Model)
				if format == "" && c.Channel.Outbound != nil {
					format = string(c.Channel.Outbound.APIFormat())
				}
			}
			rank, allowed := candidateTierPreference(c.Channel.Settings, req.APIFormat, llm.APIFormat(format), mode, legacySupport)
			if allowed {
				indices[rank] = append(indices[rank], i)
			}
		}
		for rank, selected := range indices {
			if len(selected) > 0 {
				candidate := cloneHealthGateCandidate(ctx, req, c, selected, c.healthGate)
				candidate.tierPreference = tierPreference(rank)
				groups[rank] = append(groups[rank], candidate)
			}
		}
	}
	result := slices.Concat(groups[:]...)
	if len(result) == 0 && len(candidates) > 0 {
		return nil, unsupportedResponsesTier(mode)
	}
	return result, nil
}

// Apply the policy to an eligible chat adapter only for opted-in Responses routes.
func convertedResponsesTier(settings *objects.ChannelSettings, input, target llm.APIFormat) bool {
	return settings != nil && settings.ResponsesTierRoutingEnabled && settings.ResponsesServiceTierOverride != objects.ResponsesServiceTierInherit &&
		responsesTierFormat(input) && (target == llm.APIFormatOpenAIChatCompletion || standardChatFormat(target))
}

// These chat adapters cannot encode OpenAI Fast or Ultrafast on the wire.
func standardChatFormat(format llm.APIFormat) bool {
	return format == llm.APIFormatAnthropicMessage || format == llm.APIFormatGeminiContents || format == llm.APIFormatOllamaChat
}

func outboundServiceTier(settings *objects.ChannelSettings, target llm.APIFormat, requested string) (string, bool) {
	if !standardChatFormat(target) {
		return settings.ResponsesTierForRequest(requested)
	}
	mode, _ := objects.NormalizeServiceTier(requested)
	switch settings.ResponsesServiceTierOverride {
	case objects.ResponsesServiceTierSupported:
		if (mode == "fast" || mode == "ultrafast") && (settings.ResponsesTierSupport == nil || !settings.ResponsesTierSupport.AllowDowngrade) {
			return "", false
		}
	case objects.ResponsesServiceTierStandard:
	default:
		return "", false
	}
	if target == llm.APIFormatAnthropicMessage {
		return "standard_only", true
	}
	return "", true
}

func candidateTierPreference(settings *objects.ChannelSettings, input, target llm.APIFormat, mode string, legacySupport bool) (tierPreference, bool) {
	if settings == nil || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit || (!settings.ResponsesTierRoutingEnabled && !legacySupport) {
		return tierUndeclared, true
	}
	converted := convertedResponsesTier(settings, input, target)

	if !responsesTierFormat(target) && !converted {
		return tierUndeclared, !(responsesTierFormat(input) && settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported)
	}
	wire, allowed := outboundServiceTier(settings, target, mode)
	if !allowed {
		return tierFallback, false
	}
	if actual, _ := objects.NormalizeServiceTier(wire); actual == mode {
		if converted {
			return tierConvertedExact, true
		}
		return tierExact, true
	}
	return tierFallback, true
}

// A sticky route may choose among peers, without promoting a downgraded attempt.
func preferredTierAmong(candidate *ChannelModelsCandidate, candidates []*ChannelModelsCandidate) bool {
	return candidate != nil && !slices.ContainsFunc(candidates, func(other *ChannelModelsCandidate) bool {
		return other.tierPreference < candidate.tierPreference
	})
}

// Multiple associations can point to the same history source. Keep the
// highest-priority surviving association only after checking model quotas.
func selectBoundTierAssociation(candidates []*ChannelModelsCandidate) []*ChannelModelsCandidate {
	var best *ChannelModelsCandidate
	count := 0
	for _, c := range candidates {
		if c.continuationBound {
			count++
			if best == nil || c.tierPreference < best.tierPreference || (c.tierPreference == best.tierPreference && c.Priority < best.Priority) {
				best = c
			}
		}
	}
	if count < 2 {
		return candidates
	}
	return slices.DeleteFunc(candidates, func(c *ChannelModelsCandidate) bool { return c.continuationBound && c != best })
}
