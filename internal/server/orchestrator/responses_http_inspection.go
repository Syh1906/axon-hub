package orchestrator

import (
	"context"
	"errors"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

// Only consulted for a scoped, previously identified upstream socket.
func (p *ChatCompletionOrchestrator) canInspectHTTPDelta(ctx context.Context, s *PersistenceState, body []byte, origin shared.ResponsesOrigin) bool {
	if p.ChannelService == nil {
		return false
	}
	owner := p.ChannelService.GetEnabledChannel(origin.ChannelID)
	if owner == nil || !owner.SessionReuseEnabled() || owner.Outbounds[origin.Format] == nil || !canUseHTTPResponsesSocket(body, owner) {
		return false
	}
	if s.PluginHooks.Request != nil || s.PluginHooks.Attempt != nil {
		return false
	}
	if p.PromptProtecter != nil {
		protector, ok := p.PromptProtecter.(interface {
			ListEnabledRules(context.Context) ([]*ent.PromptProtectionRule, error)
		})
		if !ok {
			return false
		}
		rules, err := protector.ListEnabledRules(ctx)
		if err != nil || len(rules) > 0 {
			return false
		}
	}
	model := gjson.GetBytes(body, "model").String()
	if s.APIKey != nil {
		model = s.ModelMapper.MapModel(ctx, s.APIKey, model)
	}
	if p.PromptProvider != nil {
		if project, ok := contexts.GetProjectID(ctx); ok {
			prompts, err := p.PromptProvider.GetEnabledPrompts(ctx, project)
			var keyID int
			if s.APIKey != nil {
				keyID = s.APIKey.ID
			}
			if err != nil || len(biz.NewPromptMatcher().FilterMatchingPrompts(prompts, model, keyID)) != 0 {
				return false
			}
		}
	}
	selector, ok := p.channelSelector.(*DefaultSelector)
	if !ok || selector.ModelService == nil {
		return false
	}
	settings := selector.modelSettings(ctx)
	for definition, err := range selector.ModelService.MatchingModels(ctx, model) {
		if err != nil {
			return false
		}
		for _, association := range biz.EffectiveModelAssociationsForRequest(settings, definition, model) {
			if association != nil && association.When != nil && association.When.Enabled && conditionUsesHistory(association.When.Condition) {
				return false
			}
		}
	}
	return true
}

func conditionUsesHistory(condition *objects.Condition) bool {
	if condition == nil {
		return false
	}
	switch condition.Field {
	case objects.ModelAssociationConditionFieldPromptTokens, objects.ModelAssociationConditionFieldHasImage,
		objects.ModelAssociationConditionFieldHasVideo, objects.ModelAssociationConditionFieldHasDocument, objects.ModelAssociationConditionFieldHasAudio:
		return true
	}
	for i := range condition.Conditions {
		if conditionUsesHistory(&condition.Conditions[i]) {
			return true
		}
	}
	return false
}

// When the original channel leaves the real candidate set, start selection
// again with the complete, portable history and the same access filters.
func restoreHTTPRouteAfterOriginLoss(ctx context.Context, s *PersistenceState, req *llm.Request, selectionErr error) (bool, error) {
	routeErr, ok := errors.AsType[*llm.ResponseError](selectionErr)
	if !ok || routeErr.Detail.Code != "continuation_candidate_unavailable" && routeErr.Detail.Code != "continuation_channel_disabled" && routeErr.Detail.Code != "continuation_channel_missing" {
		return false, selectionErr
	}
	c := s.ResponsesContinuation
	if shared.IsResponsesWebSocket(ctx) || c == nil || c.store == nil || c.previous == "" || req.RawRequest == nil {
		return false, selectionErr
	}
	owner := c.store.lookup(ctx, c.previous)
	if owner == nil || owner.origin.Generation == 0 || owner.origin.Format != string(llm.APIFormatOpenAIResponseWebSocket) {
		return false, selectionErr
	}
	// This ID was deliberately preserved through preparation. The full-history
	// prompt/association/plugin checks have already excluded dependent requests.
	body, _, err := c.store.prepare(ctx, c.original)
	if err != nil {
		return false, err
	}
	if responsePreviousID(body) != "" || containsOpaqueResponsesInput(body) {
		return false, selectionErr
	}
	raw := *req.RawRequest
	raw.Body, raw.JSONBody = body, body
	restored, err := responses.NewInboundTransformer().TransformRequest(ctx, &raw)
	if err != nil {
		return false, err
	}
	req.Messages, req.ProviderExtensions, req.PreviousResponseID = restored.Messages, restored.ProviderExtensions, nil
	req.RawRequest.Body, req.RawRequest.JSONBody = body, body
	c.original, c.previous = body, ""
	return true, nil
}
