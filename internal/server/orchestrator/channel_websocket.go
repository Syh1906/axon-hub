package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/authz/identity"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type (
	wsProbeKey struct{}
	wsProbe    struct {
		mu      sync.Mutex
		channel *biz.Channel
		release func()
		result  biz.ChannelWebSocketEvidence
	}
)

func currentWSProbe(ctx context.Context) *wsProbe {
	probe, _ := ctx.Value(wsProbeKey{}).(*wsProbe)
	return probe
}

func (probe *wsProbe) record(result biz.ChannelWebSocketEvidence) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.result = result
}

func (probe *wsProbe) outcome() biz.ChannelWebSocketEvidence {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.result
}

var errNoWSEndpoint = errors.New("no eligible Responses endpoint for WebSocket verification")

func (p *TestChannelOrchestrator) webSocketChannel(ctx context.Context, id objects.GUID, model string) (*biz.Channel, string, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
		return nil, "", err
	}
	if identity.IsPlugin(ctx) {
		return nil, "", xerrors.ForbiddenError("channel testing is not available to plugins")
	}
	ch, err := p.channelService.WebSocketTestChannel(ctx, id.ID)
	if err != nil {
		return nil, "", err
	}
	entry, ok := ch.GetModelEntries()[model]
	if !ok {
		entry, ok = ch.GetDirectModelEntries()[model]
	}
	if !ok {
		return nil, "", errors.New("model is not configured on this channel")
	}
	return ch, entry.ActualModel, nil
}

func (p *TestChannelOrchestrator) WebSocketEvidence(ctx context.Context, id objects.GUID, model string) (*biz.ChannelWebSocketEvidence, error) {
	ch, actual, err := p.webSocketChannel(ctx, id, model)
	if err != nil {
		return nil, err
	}
	result, err := p.channelService.LoadWebSocketEvidence(ctx, ch, actual)
	return &result, err
}

func (p *TestChannelOrchestrator) VerifyChannelWebSocket(ctx context.Context, id objects.GUID, model string, reuse bool) (*biz.ChannelWebSocketEvidence, error) {
	ch, actual, err := p.webSocketChannel(ctx, id, model)
	if err != nil {
		return nil, err
	}
	if reuse || len(ch.GetEnabledAPIKeys()) <= 1 {
		result, err := p.channelService.LoadWebSocketEvidence(ctx, ch, actual)
		if err != nil {
			return nil, err
		}
		if result.Reason == "rate_limited" && result.ExpiresAt != nil && time.Now().Before(*result.ExpiresAt) && len(ch.GetEnabledAPIKeys()) <= 1 {
			return &result, nil
		}
		if reuse && result.State == "verified" {
			return &result, nil
		}
	}
	run := func() (*biz.ChannelWebSocketEvidence, error) {
		probe := &wsProbe{channel: ch, result: biz.ChannelWebSocketEvidence{Model: actual, State: "unknown", Stage: "preparation", Reason: "not_dispatched", Source: "probe"}}
		defer func() {
			if probe.release != nil {
				probe.release()
			}
		}()
		ctx := context.WithValue(ctx, wsProbeKey{}, probe)
		_, err := p.TestChannel(ctx, id, &model, nil)
		if err != nil && !errors.Is(err, errNoWSEndpoint) {
			return nil, err
		}
		result := probe.outcome()
		return &result, nil
	}
	return p.channelService.RunWebSocketProbe(ctx, ch, actual, run)
}

// Background jobs require the HTTP response lifecycle. Check the original
// request and channel rewrites before choosing the transport or preserving an ID.
func canUseHTTPResponsesSocket(body []byte, ch *biz.Channel) bool {
	if gjson.GetBytes(body, "background").Bool() {
		return false
	}
	return !channelRewritesBackground(ch)
}

func channelRewritesBackground(ch *biz.Channel) bool {
	return slices.ContainsFunc(ch.GetBodyOverrideOperations(), func(op objects.OverrideOperation) bool {
		return op.Path == "background" || op.To == "background"
	})
}

func (p *PersistentOutboundTransformer) selectHTTPResponsesTransport(ctx context.Context, c *ChannelModelsCandidate, entry biz.ChannelModelEntry, req *llm.Request) {
	if !shared.IsResponsesAPI(ctx) || shared.IsResponsesWebSocket(ctx) || c.APIFormat != string(llm.APIFormatOpenAIResponse) ||
		p.state.ChannelService == nil || req.APIFormat != llm.APIFormatOpenAIResponse {
		return
	}
	mode := c.Channel.ResponsesTransportMode()
	if mode == objects.ResponsesTransportAuto {
		scope, ok := shared.GetSessionScope(ctx)
		if !ok || scope == "" || !c.Channel.SessionReuseEnabled() {
			return
		}
	}
	if turn := p.state.ResponsesContinuation; turn != nil {
		if req.PreviousResponseID != nil {
			if owner := turn.owner; owner == nil || owner.Snapshot == nil && owner.Origin.Format == string(llm.APIFormatOpenAIResponse) {
				return
			}
		}
		if gjson.GetBytes(turn.original, "background").Bool() || mode == objects.ResponsesTransportAuto && channelRewritesBackground(c.Channel) {
			return
		}
	}
	ws := llm.APIFormatOpenAIResponseWebSocket
	if c.Channel.Outbounds[string(ws)] == nil || channelProtocolRejection(c.Channel, req, ws) != "" {
		return
	}
	formats := biz.ForcedAPIFormatsForCandidate(c.Channel, []biz.ChannelModelEntry{entry}, p.GetRequestedModel())
	if len(formats) != 0 && !slices.Contains(formats, string(ws)) {
		return
	}
	// This cheap check nominates the transport. configureWebSocket checks the
	// exact model, endpoint and credential after all overrides have run.
	var owner *shared.ResponsesRecord
	if turn := p.state.ResponsesContinuation; turn != nil {
		owner = turn.owner
	}
	if owner != nil && owner.Origin.ChannelID == c.Channel.ID && owner.Origin.Model == entry.ActualModel &&
		owner.Origin.Format == string(ws) && owner.Origin.Generation != 0 {
		c.APIFormat = string(ws)
		return
	}
	if mode != objects.ResponsesTransportAuto || p.state.ChannelService.LatestWebSocketEvidence(c.Channel, entry.ActualModel).State == "verified" {
		c.APIFormat = string(ws)
	}
}

func (p *PersistentOutboundTransformer) configureWebSocket(ctx context.Context, raw *httpclient.Request, execution *ent.RequestExecution) error {
	sel := execution.Selection
	if sel == nil {
		return nil
	}
	probe := currentWSProbe(ctx)
	if !shared.IsResponsesAPI(ctx) && !shared.IsResponsesWebSocket(ctx) {
		return nil
	}
	ch := p.GetCurrentChannel()
	if ch == nil || p.state.ChannelService == nil {
		return nil
	}
	mode := ch.ResponsesTransportMode()
	httpClient := !shared.IsResponsesWebSocket(ctx)
	if raw.APIFormat != string(llm.APIFormatOpenAIResponseWebSocket) && (probe == nil || !responses.IsResponsesFormat(llm.APIFormat(raw.APIFormat))) {
		if probe != nil || mode != objects.ResponsesTransportWSOnly {
			return nil
		}
		turn := p.state.ResponsesContinuation
		if httpClient && (gjson.GetBytes(raw.Body, "background").Bool() || turn != nil && gjson.GetBytes(turn.original, "background").Bool()) {
			return nil
		}
		return &llm.ResponseError{StatusCode: http.StatusUnprocessableEntity, Cause: httpclient.ErrRequestNotSent, Detail: llm.ErrorDetail{
			Type: "invalid_request_error", Code: "no_compatible_endpoint", Message: "This channel requires a compatible Responses WebSocket endpoint",
		}}
	}
	background := httpClient && gjson.GetBytes(raw.Body, "background").Bool()
	actual := gjson.GetBytes(raw.Body, "model").String()
	if actual == "" {
		actual = p.GetCurrentModelID()
	}
	key := biz.WebSocketScope{ChannelID: ch.ID, Profile: ch.WebSocketProfile(), Model: actual, EndpointRef: biz.ResponsesUpstreamRef(raw.URL, raw.Headers), CredentialRef: responses.WebSocketCredentialIdentity(raw)}
	credential := biz.SafeWebSocketCredential(sel.KeyRemark, sel.KeyHint)
	service := p.state.ChannelService
	policy := &httpclient.ResponsesWebSocketPolicy{Probe: probe != nil}
	if ch.Settings != nil {
		policy.HTTPDisabled = (mode == objects.ResponsesTransportWSOnly && !background) || slices.Contains(ch.Settings.DisabledEndpoints, string(llm.APIFormatOpenAIResponse))
	}
	raw.ResponsesWebSocket = policy
	sel.WebSocketProfile, sel.WebSocketEndpoint, sel.WebSocketIdentity = key.Profile, key.EndpointRef, key.CredentialRef
	started := time.Now()
	source := "traffic"
	if probe != nil {
		source = "probe"
		sel.WebSocketProbe = true
	}
	endpoint := raw.URL
	storeEvidence := sel.ObserveWebSocket()
	mappedModel := p.GetCurrentModelID()
	var handshake time.Duration
	policy.Observe = func(observation httpclient.WebSocketObservation) {
		if observation.Handshake > 0 {
			handshake = observation.Handshake
		}
		if observation.Reason == "verified" {
			observation.Handshake = handshake
		}
		result := service.RecordWebSocketObservation(key, mappedModel, endpoint, credential, source, started, observation)
		storeEvidence(&result)
		if probe != nil {
			probe.record(result)
		}
	}
	if probe != nil {
		_, policy.HandshakeTimeout = service.WebSocketDecision(key)
		return nil
	}
	if httpClient {
		switch mode {
		case objects.ResponsesTransportPreferWS:
			policy.HTTPReason, policy.HandshakeTimeout = service.WebSocketTrafficDecision(ctx, key)
		case objects.ResponsesTransportWSOnly:
			_, policy.HandshakeTimeout = service.WebSocketDecision(key)
		default:
			if scope, ok := shared.GetSessionScope(ctx); !ok || strings.TrimSpace(scope) == "" {
				policy.HTTPReason = "websocket_unverified"
			} else {
				policy.HTTPReason, policy.HandshakeTimeout = service.WebSocketDecision(key)
			}
		}
		if background {
			policy.HTTPReason = "background_requires_http"
		}
		policy.VerifiedHTTP = policy.HTTPReason == ""
		// The HTTP request may carry a stored response ID owned by a different
		// transport. Only the original socket can consume its own ID.
		if id := gjson.GetBytes(raw.Body, "previous_response_id").String(); id != "" {
			c := p.state.ResponsesContinuation
			if c == nil || c.owner == nil || c.owner.ID != id || c.owner.Origin.Generation == 0 ||
				c.owner.Origin.Format != string(llm.APIFormatOpenAIResponseWebSocket) {
				policy.VerifiedHTTP = false
				policy.HTTPReason = "continuation_uses_http_origin"
			} else {
				if policy.HTTPReason == "background_requires_http" {
					return shared.ResponsesRecoveryError()
				}
				// A WS continuation ID is unsafe on the HTTP fallback.
				policy.NativeOnly = true
				// Its scoped response ID and generation are direct evidence of a
				// previously completed turn. Checkout rechecks the live socket.
				policy.HTTPReason, policy.VerifiedHTTP = "", true
				policy.RestoreBeforeWrite = p.restoreNativeContinuation
			}
		}
	}
	c := p.state.ResponsesContinuation
	if c != nil && !c.complete && c.owner != nil && c.owner.Snapshot != nil && c.lane != nil &&
		c.owner.ID == c.previous && gjson.GetBytes(raw.Body, "previous_response_id").String() == c.previous {
		if attempt := shared.GetResponsesAttempt(ctx); attempt != nil && c.owner.Origin == attempt.Origin() {
			// Only the owning native channel can use this ID. Checkout decides
			// whether it can send the delta or needs the lane's complete copy.
			policy.NativeOnly = true
			policy.RestoreBeforeWrite = p.restoreNativeContinuation
			_, policy.HandshakeTimeout = service.WebSocketDecision(key)
			return nil
		}
	}
	if target, ok := ch.Outbounds[string(llm.APIFormatOpenAIResponse)].(interface {
		PrepareResponsesHTTP(*httpclient.Request) *httpclient.Request
	}); ok {
		policy.PrepareHTTP = target.PrepareResponsesHTTP
	}
	if contexts.GetSourceOrDefault(ctx, request.SourceAPI) == request.SourceTest {
		policy.HTTPURL = p.responsesHTTPURL()
		return nil
	}
	entry := p.state.CurrentCandidate.Models[p.state.CurrentModelIndex]
	if !ch.WebSocketHTTPAllowed([]biz.ChannelModelEntry{entry}, p.GetRequestedModel()) {
		policy.HTTPDisabled = true
		return nil
	}
	policy.HTTPURL = p.responsesHTTPURL()
	if policy.HTTPURL != "" && policy.HTTPURL != raw.URL {
		httpRef := biz.ResponsesUpstreamRef(policy.HTTPURL, raw.Headers)
		if httpRef != key.EndpointRef {
			ref := p.state.CurrentCandidate.continuationUpstreamRef
			if ref == "" && c != nil && c.owner != nil && c.owner.Origin.Endpoint != "" {
				ref = biz.ResponsesUpstreamRef(c.owner.Origin.Endpoint, raw.Headers)
			}
			if ref != "" && containsOpaqueResponsesInput(raw.Body) {
				switch ref {
				case key.EndpointRef:
					policy.HTTPDisabled = true
					policy.NativeOnly = true
					policy.HTTPURL = ""
					return nil
				case httpRef:
					policy.HTTPReason = "continuation_uses_http_origin"
					return nil
				default:
					return shared.ResponsesRecoveryError()
				}
			}
		}
	}
	if c != nil && !c.complete && c.lane != nil && c.owner != nil && c.previous == c.owner.ID && gjson.GetBytes(raw.Body, "previous_response_id").String() == c.previous {
		if attempt := shared.GetResponsesAttempt(ctx); attempt != nil && originMatches(c.owner.Origin, attempt.Origin()) && c.lane.CanContinue(*c.owner) {
			return nil
		}
	}
	if !httpClient && mode != objects.ResponsesTransportWSOnly {
		policy.HTTPReason, policy.HandshakeTimeout = service.WebSocketTrafficDecision(ctx, key)
	}
	return nil
}

// Inspect the actual restored input only when the two transports lead to different origins.
func containsOpaqueResponsesInput(body []byte) bool {
	found := false
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		found = item.Get("encrypted_content").String() != ""
		if !found && item.Get("content").IsArray() {
			item.Get("content").ForEach(func(_, part gjson.Result) bool { found = part.Get("encrypted_content").String() != ""; return !found })
		}
		return !found
	})
	return found
}

func (p *PersistentOutboundTransformer) responsesHTTPURL() string {
	ch := p.GetCurrentChannel()
	format := string(llm.APIFormatOpenAIResponse)
	out, ok := ch.Outbounds[format].(interface{ ResponsesURL() string })
	if !ok {
		return ""
	}
	endpoint := out.ResponsesURL()
	addresses := ch.EnabledUpstreamAddresses(format)
	if p.addressIndex >= len(addresses) {
		return endpoint
	}
	base, err := url.Parse(ch.NormalizeUpstreamAddress(ch.BaseURL, format))
	if err != nil {
		return endpoint
	}
	resolved, err := url.Parse(endpoint)
	if err != nil || resolved.Host != base.Host {
		return endpoint
	}
	prefix := strings.TrimRight(base.Path, "/")
	if resolved.Path != prefix && !strings.HasPrefix(resolved.Path, prefix+"/") {
		return endpoint
	}
	address := addresses[p.addressIndex].URL
	if p.continuationAddresses != nil && p.addressIndex < len(p.continuationAddresses) {
		for _, a := range addresses {
			if a.URL == p.continuationAddresses[p.addressIndex].URL {
				address = a.URL
				break
			}
		}
	}
	selected, err := url.Parse(ch.NormalizeUpstreamAddress(address, format))
	if err != nil {
		return endpoint
	}
	resolved.Scheme, resolved.Host = selected.Scheme, selected.Host
	resolved.Path = strings.TrimRight(selected.Path, "/") + strings.TrimPrefix(resolved.Path, prefix)
	resolved.RawPath = ""
	return resolved.String()
}
