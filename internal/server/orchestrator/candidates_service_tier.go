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
	tierUndeclared
	tierFallback
)

func responsesTierFormat(format llm.APIFormat) bool {
	return format == llm.APIFormatOpenAIResponse || format == llm.APIFormatOpenAIResponseWebSocket
}

// Alternate aliases may map to the same upstream model through different
// protocols. Keep both until capability filtering has chosen a legal endpoint.
type tierModelKey struct {
	model     string
	responses bool
}

func (c *ChannelModelsCandidate) tierModelKey(index int) tierModelKey {
	key := tierModelKey{model: c.Models[index].ActualModel}
	if c.Channel.Settings != nil && c.Channel.Settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported {
		format := c.APIFormat
		if len(c.modelAPIFormats) == len(c.Models) {
			format = c.modelAPIFormats[index]
		}
		key.responses = responsesTierFormat(llm.APIFormat(format))
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
	return acceleratedChatRequest(req) && slices.ContainsFunc(candidates, func(c *ChannelModelsCandidate) bool {
		return c.Channel.Settings != nil && c.Channel.Settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported
	})
}

func rankResponsesTiers(ctx context.Context, req *llm.Request, candidates []*ChannelModelsCandidate) ([]*ChannelModelsCandidate, error) {
	mode, _ := objects.NormalizeServiceTier(lo.FromPtr(req.ServiceTier))
	var groups [3][]*ChannelModelsCandidate
	for _, c := range candidates {
		var indices [3][]int
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
			rank, allowed := candidateTierPreference(c.Channel.Settings, req.APIFormat, llm.APIFormat(format), mode)
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
	result := slices.Concat(groups[0], groups[1], groups[2])
	if len(result) == 0 && len(candidates) > 0 {
		return nil, unsupportedResponsesTier(mode)
	}
	return result, nil
}

func candidateTierPreference(settings *objects.ChannelSettings, input, target llm.APIFormat, mode string) (tierPreference, bool) {
	if settings == nil || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit {
		return tierUndeclared, true
	}
	if !responsesTierFormat(target) {
		return tierUndeclared, !(responsesTierFormat(input) && settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierSupported)
	}
	wire, allowed := settings.ResponsesTierForRequest(mode)
	if !allowed {
		return tierFallback, false
	}
	if actual, _ := objects.NormalizeServiceTier(wire); actual == mode {
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
