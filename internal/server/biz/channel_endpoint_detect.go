package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/modelscope"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
)

// endpointDetectTimeout bounds each upstream probe so a slow or hanging
// provider cannot stall the whole detection request.
const endpointDetectTimeout = 20 * time.Second

// Detection outcome reasons. They are stable values consumed by the frontend
// to explain why a protocol was or was not marked as supported.
const (
	endpointDetectReasonSupported       = "supported"
	endpointDetectReasonNotFound        = "not_found"
	endpointDetectReasonAuthError       = "auth_error"
	endpointDetectReasonRateLimited     = "rate_limited"
	endpointDetectReasonServerError     = "server_error"
	endpointDetectReasonUnreachable     = "unreachable"
	endpointDetectReasonInvalid         = "invalid_request"
	endpointDetectReasonInvalidResponse = "invalid_response"
)

// DetectChannelEndpointsInput is the input for automatic endpoint detection.
type DetectChannelEndpointsInput struct {
	Draft     *ChannelProbeDraftInput `json:"draft,omitempty"`
	ChannelID *objects.GUID           `json:"channelID"`
	// Model optionally pins the model used for the probe. When empty the
	// channel default test model (or first supported model) is used.
	Model *string `json:"model,omitempty"`
}

type ChannelProbeDraftInput struct {
	Type              channel.Type
	BaseURL           string
	Credentials       objects.ChannelCredentials
	Endpoints         []objects.ChannelEndpoint
	UpstreamAddresses []objects.ChannelUpstreamAddress
	Proxy             *httpclient.ProxyConfig
}

// DetectedChannelEndpoint reports the detection outcome for a single protocol.
type DetectedChannelEndpoint struct {
	Model      string `json:"model"`
	APIFormat  string `json:"apiFormat"`
	Supported  bool   `json:"supported"`
	StatusCode int    `json:"statusCode"`
	Reason     string `json:"reason"`
}

// DetectChannelEndpointsPayload is the result of automatic endpoint detection.
type DetectChannelEndpointsPayload struct {
	Endpoints []DetectedChannelEndpoint `json:"endpoints"`
}

// DetectChannelEndpoints uses either the saved connection or an in-memory draft.
func (svc *ChannelService) DetectChannelEndpoints(ctx context.Context, input DetectChannelEndpointsInput) (*DetectChannelEndpointsPayload, error) {
	if (input.Draft == nil) == (input.ChannelID == nil) {
		return nil, errors.New("provide either a saved channel or a draft")
	}
	var entity *ent.Channel
	if input.ChannelID != nil {
		var err error
		entity, err = svc.upstreamChannel(ctx, input.ChannelID.ID, scopes.ScopeWriteChannels)
		if err != nil {
			return nil, err
		}
	} else {
		if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
			return nil, err
		}
		draft := input.Draft
		if _, err := parseChannelAddress(strings.TrimRight(draft.BaseURL, "#")); err != nil {
			return nil, err
		}
		if err := validateEndpointsForChannelType(draft.Type, draft.Endpoints, draft.BaseURL); err != nil {
			return nil, err
		}
		entity = &ent.Channel{Type: draft.Type, BaseURL: draft.BaseURL, Name: "probe", Credentials: draft.Credentials,
			Endpoints: draft.Endpoints, Settings: &objects.ChannelSettings{Proxy: draft.Proxy, UpstreamAddresses: draft.UpstreamAddresses}}
		if err := validateChannelAddresses(entity.Settings, entity.Type); err != nil {
			return nil, err
		}
	}
	ch, err := svc.buildChannelWithOutbounds(entity)
	if err != nil {
		// A malformed custom endpoint must not abort detection: fall back to the
		// primary transformer and probe the remaining formats generically.
		log.Warn(ctx, "endpoint detection: failed to build channel outbounds, falling back to primary",
			log.String("channel", entity.Name),
			log.Cause(err))

		ch, err = svc.buildChannelWithTransformer(entity)
		if err != nil {
			return nil, fmt.Errorf("failed to build channel %s: %w", entity.Name, err)
		}

		ch.Outbounds = nil
	}

	defer stopChannelOutbounds(ch)
	if ch.HTTPClient != svc.httpClient {
		defer ch.HTTPClient.CloseIdleConnections()
	}
	model := resolveEndpointDetectModel(entity, input.Model)

	if model == "" {
		fetch := FetchModelsInput{ChannelType: entity.Type.String(), BaseURL: entity.BaseURL}
		if addresses := ch.EnabledUpstreamAddresses(""); len(addresses) > 0 {
			fetch.BaseURL = addresses[0].URL
		}
		if input.ChannelID != nil && fetch.BaseURL == entity.BaseURL {
			fetch.ChannelID = &entity.ID
		} else if keys := entity.Credentials.GetEnabledAPIKeys(entity.DisabledAPIKeys); len(keys) > 0 && !entity.Credentials.IsOAuth() {
			fetch.APIKey = &keys[0]
			if binding, ok := entity.Credentials.VertexAPIKey(keys[0]); ok {
				fetch.ProjectID = &binding.ProjectID
				fetch.Region = &binding.Region
			}
		}
		fetchCtx, cancel := context.WithTimeout(ctx, endpointDetectTimeout)
		models, err := NewModelFetcher(ch.HTTPClient, svc).FetchModels(fetchCtx, fetch)
		cancel()
		if err == nil && models != nil && len(models.Models) > 0 {
			model = models.Models[0].ID
		}
		if model == "" {
			return nil, errors.New("no test model available: enter a test model")
		}
	}
	formats := endpointProbeFormats(entity.Type)
	for _, ep := range NormalizeEndpoints(entity.Endpoints, entity.BaseURL) {
		if !slices.Contains(formats, ep.APIFormat) {
			formats = append(formats, ep.APIFormat)
		}
	}
	results := make([]DetectedChannelEndpoint, len(formats))
	var group errgroup.Group
	group.SetLimit(4)
	for i, format := range formats {
		group.Go(func() (err error) {
			defer func() {
				if failure := recover(); failure != nil {
					log.Error(ctx, "channel capability probe panicked", log.String("capability", format))
					results[i] = DetectedChannelEndpoint{APIFormat: format, Model: model, Reason: endpointDetectReasonInvalid}
				}
			}()
			if err := ctx.Err(); err != nil {
				return err
			}
			results[i] = svc.detectChannelEndpoint(ctx, entity, ch, format, model)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return &DetectChannelEndpointsPayload{Endpoints: results}, nil
}

// detectChannelEndpoint probes a single api_format and classifies the result.
// Only a successful response with the requested protocol's shape proves support.
func (svc *ChannelService) detectChannelEndpoint(
	ctx context.Context,
	entity *ent.Channel,
	ch *Channel,
	apiFormat string,
	model string,
) DetectedChannelEndpoint {
	result := DetectedChannelEndpoint{
		APIFormat: apiFormat, Model: model,
		Reason: endpointDetectReasonUnreachable,
	}

	// Reuse the outbound the channel already has for this format when present:
	// it carries the channel type's real platform, auth scheme, base URL and
	// path. Only fall back to a generic transformer for formats the channel does
	// not expose yet, which is exactly what detection is trying to discover.
	format := probeOutboundFormat(apiFormat)
	outbound := ch.Outbounds[format]
	if outbound == nil {
		// Generic outbounds authenticate with an API key. OAuth channels can
		// discover only surfaces with a provider-native credential adapter.
		nativeCodex := entity.Type == channel.TypeCodex && (isResponsesEndpoint(format) || format == string(llm.APIFormatOpenAIAlphaSearch) || isNativeImageFormat(format))
		if !nativeCodex && (entity.Credentials.IsOAuth() || len(ch.cachedEnabledAPIKeys) == 0) {
			result.Reason = endpointDetectReasonInvalid
			return result
		}
		var err error

		outbound, err = svc.buildNonDefaultEndpointOutbound(entity, ch, probeEndpoint(entity, format))
		if err != nil {
			log.Debug(ctx, "endpoint detection: cannot build outbound",
				log.String("channel", entity.Name),
				log.String("api_format", apiFormat),
				log.Cause(err))
			result.Reason = endpointDetectReasonInvalid

			return result
		}
		if stoppable, ok := outbound.(stoppableOutbound); ok {
			defer stoppable.Stop()
		}
	}

	timeout := endpointDetectTimeout
	switch apiFormat {
	case probeImageTool, "modelscope/image_generation", "openai/image_generation", "openai/image_edit", "openai/image_variation":
		timeout = 90 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := outbound.TransformRequest(probeCtx, buildCapabilityProbe(apiFormat, model))
	if err != nil {
		log.Debug(ctx, "endpoint detection: cannot build request",
			log.String("channel", entity.Name),
			log.String("api_format", apiFormat),
			log.Cause(err))
		result.Reason = endpointDetectReasonInvalid

		return result
	}

	// Use the same wire URL mapping as live dispatch. Explicit endpoint URLs
	// and provider-specific services outside the channel base keep their target.
	addressFormat := format
	if format == string(llm.APIFormatOpenAIResponseWebSocket) && !slices.ContainsFunc(entity.Endpoints, func(ep objects.ChannelEndpoint) bool { return ep.APIFormat == format }) {
		addressFormat = string(llm.APIFormatOpenAIResponse)
	}
	if addresses := ch.EnabledUpstreamAddresses(addressFormat); len(addresses) > 0 {
		route, err := MatchUpstreamAddress(httpReq.URL, ch.NormalizeUpstreamAddress(entity.BaseURL, addressFormat))
		if err != nil {
			result.Reason = endpointDetectReasonInvalid
			return result
		}
		if route != nil {
			target, err := route.URLAt(ch.NormalizeUpstreamAddress(addresses[0].URL, addressFormat))
			if err != nil {
				result.Reason = endpointDetectReasonInvalid
				return result
			}
			if apiFormat == string(llm.APIFormatModelScopeImage) && target != httpReq.URL {
				// Task polling is bound to the address that accepted this image request.
				outbound, err = modelscope.NewOutboundTransformerWithConfig(&modelscope.Config{BaseURL: strings.TrimSuffix(target, "/images/generations"), APIKeyProvider: getAPIKeyProvider(ch), HTTPClient: ch.HTTPClient})
				if err != nil {
					result.Reason = endpointDetectReasonInvalid
					return result
				}
			}
			httpReq.URL = target
		}
	}
	if apiFormat == string(llm.APIFormatOpenAIAlphaSearch) {
		id := gjson.GetBytes(httpReq.Body, "id").Str
		httpReq.Headers.Set(codex.SessionHeader, id)
		httpReq.Headers.Set(codex.SessionHeaderHyphen, id)
		httpReq.Headers.Set("Originator", codex.AxonHubOriginator)
	}
	var executor pipeline.Executor = ch.HTTPClient
	if customized, ok := outbound.(pipeline.ChannelCustomizedExecutor); ok {
		executor = customized.CustomizeExecutor(executor)
	}
	isWS := apiFormat == string(llm.APIFormatOpenAIResponseWebSocket)
	var handshakeAccepted bool
	if isWS {
		httpReq.ResponsesWebSocket = &httpclient.ResponsesWebSocketPolicy{Probe: true, NativeOnly: true,
			Observe: func(observation httpclient.WebSocketObservation) {
				if observation.Stage == "handshake" && observation.Reason == "accepted" {
					handshakeAccepted = true
				}
			}}
	}
	resp, err := executor.Do(probeCtx, httpReq)
	if err == nil {
		result.StatusCode = resp.StatusCode
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices && (!isWS || handshakeAccepted) {
			if apiFormat == string(llm.APIFormatModelScopeImage) {
				// Native ModelScope may return a task ID. Its transformer follows that
				// specific task using the credential that submitted it.
				output, err := outbound.TransformResponse(probeCtx, resp)
				result.Supported = err == nil && output != nil && output.Image != nil && slices.ContainsFunc(output.Image.Data, func(item llm.ImageData) bool { return validProbeImage(item.URL, item.B64JSON) })
			} else {
				result.Supported = validCapabilityResponse(apiFormat, resp)
			}
		}
		result.Reason = endpointDetectReasonInvalidResponse
		if result.Supported {
			result.Reason = endpointDetectReasonSupported
		}

		return result
	}

	var httpErr *httpclient.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		result.Reason = "timeout"
		return result
	}
	if !errors.As(err, &httpErr) {
		log.Debug(ctx, "endpoint detection: transport error",
			log.String("channel", entity.Name),
			log.String("api_format", apiFormat),
			log.Cause(err))

		return result
	}

	result.StatusCode = httpErr.StatusCode

	code := strings.ToLower(gjson.GetBytes(httpErr.Body, "error.code").String())
	message := strings.ToLower(gjson.GetBytes(httpErr.Body, "error.message").String())
	switch {
	case httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden:
		result.Reason = endpointDetectReasonAuthError
	case httpErr.StatusCode == http.StatusTooManyRequests:
		result.Reason = endpointDetectReasonRateLimited
	case strings.Contains(code, "model") || strings.Contains(message, "model") && (strings.Contains(message, "not found") || strings.Contains(message, "does not exist") || strings.Contains(message, "not support")):
		result.Reason = "model_unavailable"
	case httpErr.StatusCode == http.StatusMethodNotAllowed || httpErr.StatusCode == http.StatusNotFound && (code == "endpoint_not_found" || code == "route_not_found" || strings.Contains(message, "endpoint") || strings.Contains(message, "route") || strings.TrimSpace(string(httpErr.Body)) == "404 page not found"):
		result.Reason = endpointDetectReasonNotFound
	case code == "unsupported_tool" || code == "tool_not_supported":
		result.Reason = "unsupported_tool"
	case httpErr.StatusCode >= http.StatusInternalServerError:
		result.Reason = endpointDetectReasonServerError
	default:
		result.Reason = endpointDetectReasonInvalid
	}

	return result
}

func validEndpointDetectResponse(format string, body []byte) bool {
	var response struct {
		Object  string            `json:"object"`
		Type    string            `json:"type"`
		Role    string            `json:"role"`
		Status  string            `json:"status"`
		Choices []json.RawMessage `json:"choices"`
		Content []json.RawMessage `json:"content"`
		Output  []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	switch llm.APIFormat(format) {
	case llm.APIFormatOpenAIChatCompletion:
		return response.Object == "chat.completion" && len(response.Choices) > 0
	case llm.APIFormatOpenAIResponse:
		return response.Object == "response" && response.Output != nil && (response.Status == "completed" || response.Status == "incomplete")
	case llm.APIFormatAnthropicMessage:
		return response.Type == "message" && response.Role == "assistant" && response.Content != nil
	default:
		return false
	}
}

// buildEndpointDetectRequest builds a minimal, cheap probe request. The model is
// always sent even when empty so upstreams that validate the field still reply
// with a route-level 4xx instead of a routing 404.
func buildEndpointDetectRequest(model string) *llm.Request {
	return &llm.Request{
		Model:               model,
		Messages:            []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("ping")}}},
		MaxCompletionTokens: lo.ToPtr(int64(1)),
		Stream:              lo.ToPtr(false),
	}
}

// resolveEndpointDetectModel picks the model used for probing, preferring an
// explicit request, then the channel default test model, then the first
// supported model.
func resolveEndpointDetectModel(entity *ent.Channel, requested *string) string {
	if requested != nil {
		if model := strings.TrimSpace(*requested); model != "" {
			return model
		}
	}

	if model := strings.TrimSpace(entity.DefaultTestModel); model != "" {
		return model
	}

	for _, model := range entity.SupportedModels {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
