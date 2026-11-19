package biz

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"golang.org/x/sync/singleflight"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

// ChannelWebSocketEvidence is safe to expose to channel administrators.
type ChannelWebSocketEvidence = objects.ChannelWebSocketEvidence

type WebSocketScope struct {
	ChannelID     int
	Profile       string
	Model         string
	EndpointRef   string
	CredentialRef string
}

type wsSample struct {
	ChannelWebSocketEvidence

	started     time.Time
	mappedModel string
	handshakes  [32]time.Duration
	count, next int
	dialBudget  time.Duration
}

type wsEvidenceCache struct {
	mu      sync.Mutex
	entries map[WebSocketScope]wsSample
	probes  singleflight.Group
	trials  map[WebSocketScope]struct{}
	gates   map[string]*wsProbeGate
}

type wsProbeGate struct {
	slot  chan struct{}
	users int
}

func (s *ChannelService) RunWebSocketProbe(ctx context.Context, ch *Channel, model string, run func() (*ChannelWebSocketEvidence, error)) (*ChannelWebSocketEvidence, error) {
	cache := &s.wsEvidence
	key := strconv.Itoa(ch.ID) + "\x00" + ch.wsProfile + "\x00" + model
	done := cache.probes.DoChan(key, func() (any, error) { return run() })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if result.Err != nil {
			return nil, result.Err
		}
		evidence, ok := result.Val.(*ChannelWebSocketEvidence)
		if !ok {
			return nil, fmt.Errorf("unexpected WebSocket probe result %T", result.Val)
		}
		return evidence, nil
	}
}

// AcquireWebSocketProbe limits only explicit tests, by the final selected upstream host.
func (s *ChannelService) AcquireWebSocketProbe(ctx context.Context, endpoint string) (func(), error) {
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	site := strings.ToLower(target.Host)
	cache := &s.wsEvidence
	cache.mu.Lock()
	if cache.gates == nil {
		cache.gates = make(map[string]*wsProbeGate)
	}
	gate := cache.gates[site]
	if gate == nil {
		gate = &wsProbeGate{slot: make(chan struct{}, 1)}
		cache.gates[site] = gate
	}
	gate.users++
	cache.mu.Unlock()
	drop := func() {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		gate.users--
		if gate.users == 0 {
			delete(cache.gates, site)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case gate.slot <- struct{}{}:
		return func() { <-gate.slot; drop() }, nil
	}
}

// Compute once when loading a channel. Enabling the transport alone must not
// invalidate a probe performed before the operator enabled it.
func websocketProfile(c *Channel) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(c.Type)
	_ = enc.Encode(c.BaseURL)
	_ = enc.Encode(c.Credentials) // #nosec G117 -- The encoder writes only to the configuration hash.
	_ = enc.Encode(c.DisabledAPIKeys)
	if s := c.Settings; s != nil {
		_ = enc.Encode(s.Proxy) // #nosec G117 -- The encoder writes only to the configuration hash.
		_ = enc.Encode(s.UpstreamAddresses)
		_ = enc.Encode(s.HeaderOverrideOperations)
		_ = enc.Encode(s.OverrideHeaders)
		_ = enc.Encode(s.BodyOverrideOperations)
		_ = enc.Encode(s.OverrideParameters)
	}
	// Before WS is enabled the probe uses the HTTP Responses URL with an
	// upgrade. Saving the identical derived WS endpoint must reuse that scope.
	for _, format := range []string{"openai/responses-ws", "openai/responses"} {
		if backend, ok := c.Outbounds[format].(interface{ ResponsesURL() string }); ok {
			address := backend.ResponsesURL()
			if u, err := url.Parse(address); err == nil {
				u.Scheme = strings.Replace(u.Scheme, "ws", "http", 1)
				address = u.String()
			}
			_ = enc.Encode(address)
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *ChannelService) WebSocketTestChannel(ctx context.Context, channelID int) (*Channel, error) {
	return authz.RunAsHost(ctx, s.entFromContext(ctx), "read configured channel for websocket verification", func(ctx context.Context) (*Channel, error) {
		return s.GetChannel(ctx, channelID)
	})
}

func (c *Channel) WebSocketProfile() string { return c.wsProfile }

func (s *ChannelService) WebSocketDecision(key WebSocketScope) (reason string, handshakeTimeout time.Duration) {
	s.wsEvidence.mu.Lock()
	defer s.wsEvidence.mu.Unlock()
	return s.wsEvidence.entries[key].decision()
}

// WebSocketTrafficDecision admits one unknown route per scope until the owning turn ends.
// Other turns can use HTTP immediately. A model result takes effect as soon as it arrives.
func (s *ChannelService) WebSocketTrafficDecision(ctx context.Context, key WebSocketScope) (string, time.Duration) {
	cache := &s.wsEvidence
	cache.mu.Lock()
	defer cache.mu.Unlock()
	reason, budget := cache.entries[key].decision()
	if reason != "websocket_unverified" {
		return reason, budget
	}
	if _, active := cache.trials[key]; active {
		return "websocket_discovery_in_progress", budget
	}
	if cache.trials == nil {
		cache.trials = make(map[WebSocketScope]struct{})
	}
	cache.trials[key] = struct{}{}
	context.AfterFunc(ctx, func() {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		delete(cache.trials, key)
	})
	return "", budget
}

func (v wsSample) decision() (reason string, handshakeTimeout time.Duration) {
	if v.ExpiresAt == nil || !time.Now().Before(*v.ExpiresAt) {
		return "websocket_unverified", 3 * time.Second
	}
	if v.State != "verified" {
		return "websocket_temporarily_unavailable", 3 * time.Second
	}
	return "", cmp.Or(v.dialBudget, 3*time.Second)
}

func (s *ChannelService) LatestWebSocketEvidence(ch *Channel, model string) ChannelWebSocketEvidence {
	s.wsEvidence.mu.Lock()
	defer s.wsEvidence.mu.Unlock()
	var latest wsSample
	latest.State, latest.Model = "unknown", model
	for key, sample := range s.wsEvidence.entries {
		if key.ChannelID == ch.ID && key.Profile == ch.wsProfile && (key.Model == model || sample.mappedModel == model) && sample.started.After(latest.started) {
			latest = sample
		}
	}
	if latest.ExpiresAt != nil && !time.Now().Before(*latest.ExpiresAt) {
		latest.State = "unknown"
	}
	return latest.ChannelWebSocketEvidence
}

// LoadWebSocketEvidence is only used by the management read/probe. Live routing
// never queries the database. Old executions without scoped evidence stay unknown.
func (s *ChannelService) LoadWebSocketEvidence(ctx context.Context, ch *Channel, model string) (ChannelWebSocketEvidence, error) {
	result := s.LatestWebSocketEvidence(ch, model)
	if result.CheckedAt != nil {
		return result, nil
	}
	db := s.entFromContext(ctx)
	row, err := authz.RunAsHost(ctx, db, "channel websocket evidence", func(ctx context.Context) (*ent.RequestExecution, error) {
		return db.RequestExecution.Query().Where(requestexecution.ChannelIDEQ(ch.ID), requestexecution.CreatedAtGTE(time.Now().UTC().Add(-6*time.Hour)),
			func(q *sql.Selector) {
				c := q.C(requestexecution.FieldSelection)
				q.Where(sqljson.ValueEQ(c, ch.wsProfile, sqljson.Path("ws_profile")))
				q.Where(sqljson.HasKey(c, sqljson.Path("ws_evidence")))
				q.Where(sql.Or(sql.EQ(q.C(requestexecution.FieldModelID), model), sqljson.ValueEQ(c, model, sqljson.Path("ws_evidence", "model"))))
			}).Select(requestexecution.FieldSelection, requestexecution.FieldCreatedAt, requestexecution.FieldModelID).
			Order(ent.Desc(requestexecution.FieldCreatedAt)).First(ctx)
	})
	if ent.IsNotFound(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	sel := row.Selection
	if sel == nil || sel.WebSocketEvidence == nil || sel.WebSocketIdentity == "" || sel.WebSocketEndpoint == "" {
		return result, nil
	}
	stored := *sel.WebSocketEvidence
	if stored.Stage == "handshake" && stored.Reason == "connection_failed" && stored.StatusCode == http.StatusBadRequest {
		stored.Reason = "handshake_bad_request"
	}
	key := WebSocketScope{ChannelID: ch.ID, Profile: ch.wsProfile, Model: stored.Model, EndpointRef: sel.WebSocketEndpoint, CredentialRef: sel.WebSocketIdentity}
	s.wsEvidence.mu.Lock()
	if old, ok := s.wsEvidence.entries[key]; !ok || old.started.Before(row.CreatedAt) {
		s.wsEvidence.put(key, wsSample{ChannelWebSocketEvidence: stored, mappedModel: row.ModelID, started: row.CreatedAt})
	}
	s.wsEvidence.mu.Unlock()
	return s.LatestWebSocketEvidence(ch, model), nil
}

// RecordWebSocketObservation orders evidence by attempt start. A slow old
// connection cannot replace a more recent result or another identity's result.
func (s *ChannelService) RecordWebSocketObservation(key WebSocketScope, mappedModel, endpoint, credential, source string, started time.Time, observation httpclient.WebSocketObservation) ChannelWebSocketEvidence {
	now := time.Now()
	state, ttl := "unavailable", 30*time.Second
	switch observation.Reason {
	case "verified":
		state, ttl = "verified", 6*time.Hour
	case "accepted", "empty_output":
		state = "handshake_only"
	case "upgrade_rejected":
		ttl = 10 * time.Minute
	case "auth_error", "rate_limited":
		state = "unknown"
	}
	ttl = max(ttl, observation.RetryAfter)
	expiry := now.Add(ttl)
	safeURL, err := url.Parse(endpoint)
	display := ""
	if err == nil && safeURL.Host != "" {
		display = safeURL.Host + safeURL.EscapedPath()
	}
	evidence := ChannelWebSocketEvidence{State: state, Stage: observation.Stage, Reason: observation.Reason, Source: source, Model: key.Model, Endpoint: display, Credential: credential, CheckedAt: &now, ExpiresAt: &expiry, StatusCode: observation.Status}
	cache := &s.wsEvidence
	cache.mu.Lock()
	defer cache.mu.Unlock()
	previous, found := cache.entries[key]
	if found && previous.started.After(started) {
		return evidence
	}
	if observation.Reason == "accepted" && found && previous.State == "verified" && now.Before(*previous.ExpiresAt) {
		return evidence
	}
	previous.ChannelWebSocketEvidence, previous.started, previous.mappedModel = evidence, started, mappedModel
	if observation.Reason == "verified" && observation.Handshake > 0 {
		previous.handshakes[previous.next] = observation.Handshake
		previous.next = (previous.next + 1) % len(previous.handshakes)
		previous.count = min(previous.count+1, len(previous.handshakes))
		sorted := previous.handshakes
		slices.Sort(sorted[:previous.count])
		p95 := sorted[(previous.count*95+99)/100-1]
		previous.dialBudget = min(5*time.Second, max(2*time.Second, 4*p95))
	}
	cache.put(key, previous)
	return evidence
}

func (cache *wsEvidenceCache) put(key WebSocketScope, sample wsSample) {
	if cache.entries == nil {
		cache.entries = make(map[WebSocketScope]wsSample)
	}
	if _, found := cache.entries[key]; !found && len(cache.entries) >= 1024 {
		var oldest WebSocketScope
		var at time.Time
		for k, v := range cache.entries {
			if at.IsZero() || v.started.Before(at) {
				oldest, at = k, v.started
			}
		}
		delete(cache.entries, oldest)
	}
	cache.entries[key] = sample
}

// WebSocketHTTPAllowed accounts for explicit WS-only legacy endpoints/model
// policies. A channel with both saved transport variants can choose automatically.
func (c *Channel) WebSocketHTTPAllowed(entries []ChannelModelEntry, requested string) bool {
	if forced := ForcedAPIFormatsForCandidate(c, entries, requested); len(forced) > 0 {
		return slices.Contains(forced, "openai/responses")
	}
	return c.wsAutoHTTP
}

func channelHasWSHTTP(c *Channel) bool {
	var ws, http bool
	for _, ep := range NormalizeEndpoints(c.Endpoints, c.BaseURL) {
		switch ep.APIFormat {
		case "openai/responses":
			http = true
		case "openai/responses-ws":
			ws = true
		}
	}
	if ws && !http {
		return false
	}
	for _, ep := range c.ResolveEndpoints() {
		if ep.APIFormat == "openai/responses" {
			return true
		}
	}
	return false
}

func SafeWebSocketCredential(remark, hint string) string {
	return cmp.Or(strings.TrimSpace(remark), hint)
}
