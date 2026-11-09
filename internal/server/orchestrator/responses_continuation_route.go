package orchestrator

import (
	"context"
	"errors"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

type responsesContinuationRouteProvider interface {
	GetResponsesContinuationRoute(context.Context, string, ...string) (biz.ResponsesContinuationRoute, bool, error)
}

type responsesAffinityProvider interface {
	GetResponsesAffinity(context.Context, string, string, string) (biz.ResponsesContinuationRoute, bool, error)
}

type responsesPreviousIDContextKey struct{}

func responsePreviousID(body []byte) string {
	return gjson.GetBytes(body, "previous_response_id").String()
}

func requiresResponsesContinuationRoute(req *llm.Request) bool {
	if req == nil || !isResponsesFormat(req.APIFormat) {
		return false
	}
	if req.PreviousResponseID != nil && *req.PreviousResponseID != "" {
		return true
	}
	if req.RawRequest != nil {
		for _, item := range gjson.GetBytes(req.RawRequest.Body, "input").Array() {
			if item.Get("encrypted_content").String() != "" {
				return true
			}
		}
	}
	for _, message := range req.Messages {
		if message.ReasoningSignature != nil && *message.ReasoningSignature != "" {
			return true
		}
		for _, item := range message.ReasoningItems {
			if item.Signature != "" {
				return true
			}
		}
		for _, part := range message.Content.MultipleContent {
			if part.Compact != nil && part.Compact.EncryptedContent != "" {
				return true
			}
		}
	}
	return false
}

func selectResponsesContinuation(ctx context.Context, req *llm.Request, candidates []*ChannelModelsCandidate, provider PreviousChannelProvider) ([]*ChannelModelsCandidate, bool, error) {
	originalPreviousID, _ := ctx.Value(responsesPreviousIDContextKey{}).(string)
	if !requiresResponsesContinuationRoute(req) {
		return nil, false, nil
	}
	routes, ok := provider.(responsesContinuationRouteProvider)
	if !ok {
		return nil, false, nil
	}
	previousID := ""
	if req.PreviousResponseID != nil {
		previousID = *req.PreviousResponseID
	}
	if originalPreviousID != "" {
		previousID = originalPreviousID
	}
	strict := req.PreviousResponseID != nil && *req.PreviousResponseID != ""
	contents := responsesEncryptedContents(req)
	var route biz.ResponsesContinuationRoute
	var found bool
	var err error
	if strict {
		route, found, err = routes.GetResponsesContinuationRoute(ctx, previousID, contents...)
	} else {
		model := req.Model
		if req.RawRequest != nil {
			if original := gjson.GetBytes(req.RawRequest.Body, "model").String(); original != "" {
				model = original
			}
		}
		latest := ""
		if len(contents) > 0 {
			latest = contents[len(contents)-1]
		}
		if affinity, ok := provider.(responsesAffinityProvider); ok {
			route, found, err = affinity.GetResponsesAffinity(ctx, model, previousID, latest)
		}
	}
	if !strict {
		eligible := responsesContinuationCandidates(ctx, req, candidates)
		if err != nil {
			if !errors.Is(err, biz.ErrResponsesOriginConflict) {
				log.Warn(ctx, "Unable to load optional Responses origin", log.Cause(err))
			}
			return eligible, false, nil
		}
		if !found || route.UnavailableCode != "" {
			return eligible, false, nil
		}
		candidates = eligible
	}
	if errors.Is(err, biz.ErrResponsesOriginConflict) {
		return nil, true, continuationRouteError("continuation_origin_conflict", "当前历史包含来自不同上游或凭据的加密内容，请使用同一来源的会话分支。")
	}
	if err != nil {
		return nil, true, err
	}
	if !found {
		// Imported histories have no locally established origin. Preserve initial
		// routing rather than treating the absence of optional history as an outage.
		return nil, false, nil
	}
	if route.UnavailableCode != "" {
		message := "生成当前加密历史的渠道已停用，请恢复原渠道后继续会话。"
		if route.UnavailableCode == "continuation_channel_missing" {
			message = "生成当前加密历史的渠道已删除，无法确认可用的原历史来源。"
		}
		return nil, true, continuationRouteError(route.UnavailableCode, message)
	}
	var source *ChannelModelsCandidate
	for _, candidate := range candidates {
		if candidate.Channel.ID != route.ChannelID {
			continue
		}
		if !strict && !candidate.Channel.SessionReuseEnabled() {
			continue
		}
		if len(candidate.Models) > 0 && len(candidate.modelAPIFormats) == len(candidate.Models) {
			indices := make([]int, 0, len(candidate.Models))
			for index, format := range candidate.modelAPIFormats {
				if isResponsesFormat(llm.APIFormat(format)) {
					indices = append(indices, index)
				}
			}
			if len(indices) == 0 {
				continue
			}
			// Keep each model paired with its protocol before the health gate can
			// promote a later model. Chat endpoints cannot carry opaque history.
			candidate = cloneHealthGateCandidate(ctx, req, candidate, indices, nil)
		} else if candidate.APIFormat != "" && !isResponsesFormat(llm.APIFormat(candidate.APIFormat)) {
			continue
		}
		if source == nil || candidate.Priority < source.Priority {
			source = candidate
		}
	}
	if source != nil {
		selected := *source
		selected.continuationBound = true
		selected.continuationStrict = strict
		selected.continuationKeyRef = route.KeyRef
		selected.continuationKeyHint = route.KeyHint
		selected.continuationUpstreamRef = route.UpstreamRef
		selected.TraceSticky = true
		selected.SessionState = "reused"
		result := []*ChannelModelsCandidate{&selected}
		if !strict {
			for _, candidate := range candidates {
				if candidate.Channel.ID != source.Channel.ID {
					result = append(result, candidate)
				}
			}
		}
		return result, true, nil
	}
	if !strict {
		return candidates, false, nil
	}
	return nil, true, continuationRouteError("continuation_candidate_unavailable", "原历史渠道未进入当前模型和调用权限的可选范围，请检查模型映射、协议支持及调用权限。")
}

// Preserve opaque history on protocols that can carry it unchanged. Candidates
// have already passed model, project and API-key permission checks.
func responsesContinuationCandidates(ctx context.Context, req *llm.Request, candidates []*ChannelModelsCandidate) []*ChannelModelsCandidate {
	result := make([]*ChannelModelsCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if len(candidate.Models) > 0 && len(candidate.modelAPIFormats) == len(candidate.Models) {
			var indices []int
			for index, format := range candidate.modelAPIFormats {
				if isResponsesFormat(llm.APIFormat(format)) {
					indices = append(indices, index)
				}
			}
			if len(indices) == 0 {
				continue
			}
			candidate = cloneHealthGateCandidate(ctx, req, candidate, indices, nil)
		} else if candidate.APIFormat != "" && !isResponsesFormat(llm.APIFormat(candidate.APIFormat)) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func responsesEncryptedContents(req *llm.Request) []string {
	var values []string
	if req.RawRequest != nil {
		for _, item := range gjson.GetBytes(req.RawRequest.Body, "input").Array() {
			if value := item.Get("encrypted_content").String(); value != "" {
				values = append(values, value)
			}
		}
	}
	if len(values) > 0 {
		return values // The actual input order identifies the latest source.
	}
	for _, message := range req.Messages {
		if message.ReasoningSignature != nil {
			values = append(values, *message.ReasoningSignature)
		}
		for _, item := range message.ReasoningItems {
			values = append(values, item.Signature)
		}
		for _, part := range message.Content.MultipleContent {
			if part.Compact != nil {
				values = append(values, part.Compact.EncryptedContent)
			}
		}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func continuationCredential(channel *biz.Channel, route biz.ResponsesContinuationRoute) (string, error) {
	if channel.Credentials.IsOAuth() || (route.KeyRef == "" && route.KeyHint == "") {
		return "", nil
	}
	var match string
	for _, key := range channel.Credentials.GetEnabledAPIKeys(channel.DisabledAPIKeys) {
		matches := route.KeyRef != "" && upstream.KeyRef(key) == route.KeyRef ||
			route.KeyRef == "" && upstream.MaskKey(key) == route.KeyHint
		if matches {
			if match != "" && match != key {
				return "", continuationRouteError("continuation_credential_ambiguous", "无法唯一确定加密会话历史使用的上游凭据，请检查该渠道的凭据配置。")
			}
			match = key
		}
	}
	if match == "" {
		return "", continuationRouteError("continuation_credential_unavailable", "加密会话历史绑定的上游凭据当前不可用，请恢复原凭据后继续会话。")
	}
	return match, nil
}

func continuationRouteError(code, message string) error {
	return &llm.ResponseError{StatusCode: 409, Detail: llm.ErrorDetail{Type: "continuation_error", Code: code, Message: message}}
}

func invalidResponsesContinuation(err error) bool {
	var responseErr *llm.ResponseError
	var code, message string
	if errors.As(err, &responseErr) {
		code, message = responseErr.Detail.Code, responseErr.Detail.Message
	} else {
		// Raw-error middleware runs before the provider error is converted.
		var rawErr *httpclient.Error
		if !errors.As(err, &rawErr) {
			return false
		}
		code = gjson.GetBytes(rawErr.Body, "error.code").String()
		message = gjson.GetBytes(rawErr.Body, "error.message").String()
	}
	if code == "invalid_encrypted_content" {
		return true
	}
	message = strings.ToLower(message)
	return message == "invalid encrypted content." || strings.Contains(message, "encrypted content") &&
		(strings.Contains(message, "could not be verified") || strings.Contains(message, "could not be decrypted or parsed"))
}
