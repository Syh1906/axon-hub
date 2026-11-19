package orchestrator

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

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
	"github.com/tidwall/gjson"
)

type wsProbeKey struct{}
type wsProbe struct {
	mu      sync.Mutex
	channel *biz.Channel
	release func()
	result  biz.ChannelWebSocketEvidence
}

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

func (p *PersistentOutboundTransformer) configureWebSocket(ctx context.Context, raw *httpclient.Request, execution *ent.RequestExecution) error {
	sel := execution.Selection
	if sel == nil {
		return nil
	}
	probe := currentWSProbe(ctx)
	if !shared.IsResponsesWebSocket(ctx) || !responses.IsResponsesFormat(llm.APIFormat(raw.APIFormat)) {
		return nil
	}
	ch := p.GetCurrentChannel()
	if ch == nil || p.state.ChannelService == nil {
		return nil
	}
	if probe == nil && raw.APIFormat != string(llm.APIFormatOpenAIResponseWebSocket) {
		return nil
	}
	actual := gjson.GetBytes(raw.Body, "model").String()
	if actual == "" {
		actual = p.GetCurrentModelID()
	}
	key := biz.WebSocketScope{ChannelID: ch.ID, Profile: ch.WebSocketProfile(), Model: actual, EndpointRef: biz.ResponsesUpstreamRef(raw.URL, raw.Headers), CredentialRef: responses.WebSocketCredentialIdentity(raw)}
	credential := biz.SafeWebSocketCredential(sel.KeyRemark, sel.KeyHint)
	service := p.state.ChannelService
	reason, deadline := service.WebSocketDecision(key)
	policy := &httpclient.ResponsesWebSocketPolicy{Probe: probe != nil}
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
		policy.HandshakeTimeout = deadline
		return nil
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
		policy.NativeOnly = true
		return nil
	}
	policy.HTTPURL = p.responsesHTTPURL()
	c := p.state.ResponsesContinuation
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
	policy.HandshakeTimeout = deadline
	policy.HTTPReason = reason
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
