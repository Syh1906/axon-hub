package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xredis"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

type healthGateSessionFixture struct {
	ctx        context.Context
	client     *ent.Client
	projectID  int
	request    *biz.RequestService
	selector   *LoadBalancedSelector
	gate       *biz.HealthGate
	policy     *biz.RetryPolicy
	candidates []*ChannelModelsCandidate
}

func newHealthGateSessionFixture(t *testing.T) *healthGateSessionFixture {
	t.Helper()
	ctx, client := setupTest(t)
	project := createTestProject(t, ctx, client)
	_, requests, system, _ := setupTestServices(t, client)
	one := healthGateSelectorCandidate(1, 0, "first")
	one.Channel.Name = "primary"
	two := healthGateSelectorCandidate(2, 1, "second")
	two.Channel.Name = "backup"
	candidates := []*ChannelModelsCandidate{one, two}
	policy := &biz.RetryPolicy{
		Enabled: true, MaxChannelRetries: 1,
		LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated,
		TraceStickyMode:      biz.TraceStickyPreferPreviousChannel,
		HealthGate: &biz.HealthGatePolicy{FailureThreshold: 1, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600,
			ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300, OwnerFailoverThreshold: 2},
	}
	require.NoError(t, system.SetRetryPolicy(ctx, policy))
	gate := biz.NewHealthGate(nil)
	selector := WithTraceStickyLoadBalancedSelector(&staticChannelSelector{candidates: candidates},
		NewLoadBalancer(system, nil).WithHealthGate(gate), system, requests)
	return &healthGateSessionFixture{ctx: ctx, client: client, projectID: project.ID, request: requests, selector: selector, gate: gate, policy: policy, candidates: candidates}
}

func (f *healthGateSessionFixture) selectCandidates(t *testing.T, ctx context.Context) []*ChannelModelsCandidate {
	t.Helper()
	candidates, err := f.selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	return candidates
}

func (f *healthGateSessionFixture) begin(t *testing.T, ctx context.Context, candidates []*ChannelModelsCandidate, index int, raw *httpclient.Request) *healthGateAttemptTracker {
	t.Helper()
	if raw == nil {
		raw = &httpclient.Request{}
	}
	row, err := f.client.Request.Create().SetProjectID(f.projectID).SetSource("api").SetStatus("pending").SetModelID("alias").SetRequestBody([]byte(`{"model":"alias"}`)).Save(ctx)
	require.NoError(t, err)
	state := &PersistenceState{
		Request: row, RequestService: f.request,
		RoutingPolicy:           EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
		ChannelModelsCandidates: candidates, CurrentCandidate: candidates[index],
		LlmRequest: &llm.Request{Model: "alias", RawRequest: &httpclient.Request{
			APIFormat: raw.APIFormat, Body: append([]byte(nil), raw.Body...),
		}},
	}
	outbound := &PersistentOutboundTransformer{state: state}
	finalRaw, err := applyOverrideRequestBody(outbound).OnOutboundRawRequest(ctx, raw)
	require.NoError(t, err)
	state.RawProviderRequest = finalRaw
	tracker := withHealthGate(outbound, f.gate, f.policy.HealthGateOrDefault())
	_, err = tracker.OnOutboundRawRequest(ctx, finalRaw)
	require.NoError(t, err)
	return tracker
}

func (f *healthGateSessionFixture) finish(t *testing.T, ctx context.Context, tracker *healthGateAttemptTracker, success bool, stream bool) objects.RequestRoutingDecision {
	t.Helper()
	state := tracker.outbound.state
	if stream {
		streamWrapper, err := tracker.OnOutboundLlmStream(ctx, streams.SliceStream([]*llm.Response{}))
		require.NoError(t, err)
		require.NoError(t, tracker.finalize(ctx, nil, true))
		if success {
			state.StreamCompleted = true
		}
		require.NoError(t, streamWrapper.Close())
	} else if success {
		require.NoError(t, tracker.finalize(ctx, nil, false))
	} else {
		tracker.OnOutboundRawError(ctx, errors.New("local conversion error"))
		require.Error(t, tracker.finalize(ctx, errors.New("local conversion error"), false))
	}
	persisted, err := f.client.Request.Get(ctx, state.Request.ID)
	require.NoError(t, err)
	require.NotNil(t, persisted.RoutingDecision)
	return *persisted.RoutingDecision
}

func (f *healthGateSessionFixture) complete(t *testing.T, ctx context.Context, candidates []*ChannelModelsCandidate, index int, success bool, stream bool, raw ...*httpclient.Request) objects.RequestRoutingDecision {
	t.Helper()
	var request *httpclient.Request
	if len(raw) > 0 {
		request = raw[0]
	}
	return f.finish(t, ctx, f.begin(t, ctx, candidates, index, request), success, stream)
}

func setHealthGateSessionOwner(svc *biz.RequestService, ctx context.Context, threadID, traceID int, owner biz.SessionOwner) biz.SessionOwner {
	if owner.ExpiresAt.IsZero() {
		owner.ExpiresAt = time.Now().Add(5 * time.Minute)
	}
	svc.UpdateSessionOwner(ctx, threadID, traceID, func(biz.SessionOwner, bool) (biz.SessionOwner, bool) {
		return owner, true
	})
	return owner
}

func requireHealthGateSessionOwner(t *testing.T, owner biz.SessionOwner, channelID, count int, ttl time.Duration) {
	t.Helper()
	require.WithinDuration(t, time.Now().Add(ttl), owner.ExpiresAt, 5*time.Second)
	require.Equal(t, channelID, owner.ChannelID)
	require.Equal(t, count, owner.ConsecutiveFailovers)
}

func TestHealthGateSession_ThreadOwnerPrecedence(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 11, ThreadID: 22})
	setHealthGateSessionOwner(f.request, ctx, 0, 11, biz.SessionOwner{ChannelID: 1})
	setHealthGateSessionOwner(f.request, ctx, 22, 0, biz.SessionOwner{ChannelID: 2})
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 2, selected[0].Channel.ID)
	require.True(t, selected[0].TraceSticky)
	require.Equal(t, 2, selected[0].healthGate.decision.record.Owner.ChannelID)
}

func TestHealthGateSession_TemporaryThenConsecutiveMigration(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	expected := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1})
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 1, selected[0].Channel.ID)
	first := f.complete(t, ctx, selected, 1, true, false)
	require.True(t, first.TemporaryFailover)
	require.Equal(t, 1, first.ConsecutiveFailovers)
	require.Nil(t, first.Migration)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	expected.ConsecutiveFailovers = 1
	require.Equal(t, expected, owner)
	selected = f.selectCandidates(t, ctx)
	require.Equal(t, 1, selected[0].Channel.ID, "the next request returns to the original owner")
	second := f.complete(t, ctx, selected, 1, true, false)
	require.False(t, second.TemporaryFailover)
	require.Equal(t, 0, second.ConsecutiveFailovers)
	require.Equal(t, objects.RoutingMigrationReasonConsecutiveFailovers, second.Migration.Reason)
	require.Equal(t, "primary", second.Migration.FromChannelName)
	require.Equal(t, "backup", second.Migration.ToChannelName)
	owner, _, err = f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
	require.Equal(t, 2, f.selectCandidates(t, ctx)[0].Channel.ID, "the migrated owner stays selected")
}

func TestHealthGateSession_OwnerOpenMigratesAndRecordsSkipped(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1, ConsecutiveFailovers: 1})
	openHealthGateModel(t, f.gate, f.policy, f.candidates[0], "first")
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 2, selected[0].Channel.ID)
	require.True(t, selected[0].healthGate.decision.ownerOpen)
	require.Equal(t, []objects.RoutingDecisionCombo{{ChannelID: 1, ChannelName: "primary", ActualModel: "first", State: "open"}}, selected[0].healthGate.decision.record.Skipped)
	decision := f.complete(t, ctx, selected, 0, true, false)
	require.Equal(t, objects.RoutingMigrationReasonOwnerOpen, decision.Migration.Reason)
	require.Equal(t, "first", decision.Owner.ActualModel)
	require.Equal(t, "open", decision.Owner.State)
	owner, _, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
}

func TestHealthGateSession_FailureAndStreamCompletionOnly(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	expected := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1, ConsecutiveFailovers: 1})
	decision := f.complete(t, ctx, f.selectCandidates(t, ctx), 1, false, false)
	require.Equal(t, 1, decision.ConsecutiveFailovers)
	require.False(t, decision.TemporaryFailover)
	owner, _, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.Equal(t, expected, owner)
	selected := f.selectCandidates(t, ctx)
	decision = f.complete(t, ctx, selected, 0, true, false)
	require.Equal(t, 0, decision.ConsecutiveFailovers)
	owner, _, err = f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	requireHealthGateSessionOwner(t, owner, 1, 0, 5*time.Minute)
	expected = owner
	selected = f.selectCandidates(t, ctx)
	decision = f.complete(t, ctx, selected, 1, false, true)
	require.Equal(t, 0, decision.ConsecutiveFailovers)
	owner, _, err = f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.Equal(t, expected, owner)
	f.gate.Reset(f.candidates[1].Channel.ID, "second")
	selected = f.selectCandidates(t, ctx)
	decision = f.complete(t, ctx, selected, 1, true, true)
	require.True(t, decision.TemporaryFailover)
}

func TestHealthGateSession_LastResortExcludedFromSkippedAndStickyDisabled(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1})
	openHealthGateModel(t, f.gate, f.policy, f.candidates[0], "first")
	openHealthGateModel(t, f.gate, f.policy, f.candidates[1], "second")
	selected := f.selectCandidates(t, ctx)
	require.Len(t, selected, 1)
	require.True(t, selected[0].healthGate.decision.record.LastResort)
	require.Len(t, selected[0].healthGate.decision.record.Skipped, 1)
	require.NotEqual(t, selected[0].Channel.ID, selected[0].healthGate.decision.record.Skipped[0].ChannelID)
	f.policy.TraceStickyMode = biz.TraceStickyDisabled
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	selected = f.selectCandidates(t, ctx)
	require.Nil(t, selected[0].healthGate.decision.record.Owner)
	require.False(t, selected[0].healthGate.decision.sticky)
}

func TestHealthGateSession_StickyDisabledDoesNotChangeOwner(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	expected := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1, ConsecutiveFailovers: 1})
	f.policy.TraceStickyMode = biz.TraceStickyDisabled
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	selected := f.selectCandidates(t, ctx)
	require.Nil(t, selected[0].healthGate.decision.record.Owner)
	decision := f.complete(t, ctx, selected, 1, true, false)
	require.Nil(t, decision.Owner)
	require.False(t, decision.TemporaryFailover)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, owner)
}

func TestHealthGateSession_StreamCommitsOnlyAfterTerminalClose(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	selected := f.selectCandidates(t, ctx)
	state := &PersistenceState{
		RoutingPolicy:    EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
		CurrentCandidate: selected[0], RequestService: f.request,
	}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, f.gate, f.policy.HealthGateOrDefault())
	_, err := tracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	require.NotNil(t, tracker.decision)
	require.True(t, tracker.decision.sticky)
	require.Equal(t, 20, tracker.decision.threadID)
	require.True(t, tracker.active)
	wrapper, err := tracker.OnOutboundLlmStream(ctx, streams.SliceStream([]*llm.Response{}))
	require.NoError(t, err)
	require.NoError(t, tracker.finalize(ctx, nil, true))
	require.True(t, tracker.committed)
	_, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.False(t, found)
	state.StreamCompleted = true
	require.NoError(t, wrapper.Close())
	require.False(t, tracker.active)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, owner, selected[0].Channel.ID, 0, 5*time.Minute)
}

func TestHealthGateSession_LateFormerOwnerCannotUndoMigration(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	third := healthGateSelectorCandidate(3, 2, "third")
	third.Channel.Name = "new owner"
	f.candidates = append(f.candidates, third)
	f.selector.wrapped.(*staticChannelSelector).candidates = f.candidates
	f.policy.MaxChannelRetries = 2
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1, ConsecutiveFailovers: 1})
	fast := f.selectCandidates(t, ctx)
	slow := f.selectCandidates(t, ctx)
	require.Equal(t, 1, fast[0].Channel.ID)
	require.Equal(t, 1, slow[0].Channel.ID)
	decision := f.complete(t, ctx, fast, 2, true, false)
	require.Equal(t, objects.RoutingMigrationReasonConsecutiveFailovers, decision.Migration.Reason)
	migrated, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, migrated, 3, 0, 5*time.Minute)
	decision = f.complete(t, ctx, slow, 0, true, false)
	require.Equal(t, 1, decision.Owner.ChannelID)
	require.Nil(t, decision.Migration)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, migrated, owner, "late former-owner success must preserve the migrated deadline and count")
	require.Equal(t, 3, f.selectCandidates(t, ctx)[0].Channel.ID)
}

func TestHealthGateSession_TwoSelectedFailoversCountBoth(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1})
	first, second := f.selectCandidates(t, ctx), f.selectCandidates(t, ctx)
	firstDecision := f.complete(t, ctx, first, 1, true, false)
	require.True(t, firstDecision.TemporaryFailover)
	require.Equal(t, 1, firstDecision.ConsecutiveFailovers)
	secondDecision := f.complete(t, ctx, second, 1, true, false)
	require.Equal(t, objects.RoutingMigrationReasonConsecutiveFailovers, secondDecision.Migration.Reason)
	require.Zero(t, secondDecision.ConsecutiveFailovers)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
}

func TestHealthGateSession_PartiallyGatedOwnerIsNotOwnerOpen(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	f.candidates[0].Models = append(f.candidates[0].Models, biz.ChannelModelEntry{RequestModel: "alias", ActualModel: "other"})
	f.candidates[0].modelAPIFormats = append(f.candidates[0].modelAPIFormats, "format-other")
	expected := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1})
	openHealthGateModel(t, f.gate, f.policy, f.candidates[0], "first")
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 1, selected[0].Channel.ID)
	require.Equal(t, "other", selected[0].Models[0].ActualModel)
	require.False(t, selected[0].healthGate.decision.ownerOpen)
	decision := f.complete(t, ctx, selected, 1, true, false)
	require.True(t, decision.TemporaryFailover)
	require.Nil(t, decision.Migration)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	expected.ConsecutiveFailovers = 1
	require.Equal(t, expected, owner)
}

func TestHealthGateSession_MissingOwnerHasAuditableMigration(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ownerRow := createTestChannel(t, f.ctx, f.client)
	require.Equal(t, 1, ownerRow.ID)
	ctx := contexts.WithTrace(authz.WithTestBypass(context.Background()), &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 1})
	f.selector.wrapped.(*staticChannelSelector).candidates = f.candidates[1:]
	first := f.selectCandidates(t, ctx)
	require.Len(t, first, 1)
	require.NotNil(t, first[0].healthGate.decision.record.Owner)
	require.Equal(t, ownerRow.Name, first[0].healthGate.decision.record.Owner.ChannelName)
	require.Empty(t, first[0].healthGate.decision.record.Owner.ActualModel)
	firstDecision := f.complete(t, ctx, first, 0, true, false)
	require.True(t, firstDecision.TemporaryFailover)
	require.Equal(t, 1, firstDecision.ConsecutiveFailovers)
	require.Equal(t, ownerRow.Name, firstDecision.Owner.ChannelName)
	secondDecision := f.complete(t, ctx, f.selectCandidates(t, ctx), 0, true, false)
	require.Equal(t, objects.RoutingMigrationReasonConsecutiveFailovers, secondDecision.Migration.Reason)
	require.Equal(t, 1, secondDecision.Migration.FromChannelID)
	require.Equal(t, ownerRow.Name, secondDecision.Migration.FromChannelName)
	require.Equal(t, 2, secondDecision.Migration.ToChannelID)
}

func TestHealthGateSession_LegacyDoesNotSeed(t *testing.T) {
	for _, scope := range []string{"trace", "thread"} {
		for _, version := range []string{"legacy", "v1", "v2_without_deadline"} {
			t.Run(scope+"/"+version, func(t *testing.T) {
				f := newHealthGateSessionFixture(t)
				cache := miniredis.RunT(t)
				f.request = biz.NewRequestService(f.client, xcache.Config{
					Mode: xcache.ModeRedis, Redis: xredis.Config{Addr: cache.Addr()},
				}, nil, nil, nil, nil)
				f.selector.previousChannelProvider = f.request
				trace, err := f.client.Trace.Create().SetProjectID(f.projectID).SetTraceID("legacy-no-seed").Save(f.ctx)
				require.NoError(t, err)
				if scope == "thread" {
					trace.ThreadID = 20
				}
				ctx := contexts.WithTrace(f.ctx, trace)
				row, err := f.client.Request.Create().SetProjectID(f.projectID).SetTraceID(trace.ID).
					SetSource("api").SetStatus("completed").SetModelID("alias").SetRequestBody([]byte(`{"model":"alias"}`)).Save(ctx)
				require.NoError(t, err)
				require.NoError(t, f.request.UpdateRequestChannelID(ctx, row.ID, 2))
				id := trace.ID
				if scope == "thread" {
					id = trace.ThreadID
				}
				keyVersion := "v1"
				if version == "v2_without_deadline" {
					keyVersion = "v2"
				}
				if version != "legacy" {
					require.NoError(t, cache.Set(fmt.Sprintf("axonhub:routing:session-owner:%s:%s:%d", keyVersion, scope, id),
						`{"channel_id":2,"consecutive_failovers":1}`))
				}
				for range 2 {
					_, found, err := f.request.GetSessionOwner(ctx, trace.ThreadID, trace.ID)
					require.NoError(t, err)
					require.False(t, found, "cache reads must not seed an upgrade lease")
					selected := f.selectCandidates(t, ctx)
					require.Equal(t, 1, selected[0].Channel.ID, "fresh routing must ignore the legacy channel 2")
					require.False(t, selected[0].TraceSticky)
					require.Nil(t, selected[0].healthGate.decision.record.Owner)
					require.Zero(t, selected[0].healthGate.decision.record.ConsecutiveFailovers)
				}
				decision := f.complete(t, ctx, f.selectCandidates(t, ctx), 0, true, false)
				require.Nil(t, decision.Owner)
				require.Nil(t, decision.Migration)
				require.False(t, decision.TemporaryFailover)
				require.Zero(t, decision.ConsecutiveFailovers)
				owner, found, err := f.request.GetSessionOwner(ctx, trace.ThreadID, trace.ID)
				require.NoError(t, err)
				require.True(t, found, "only a new success establishes the v2 lease")
				requireHealthGateSessionOwner(t, owner, 1, 0, 5*time.Minute)
				selected := f.selectCandidates(t, ctx)
				require.Equal(t, 1, selected[0].Channel.ID)
				require.True(t, selected[0].TraceSticky)
			})
		}
	}
}

func TestHealthGateSession_SuccessProtectionWindow(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  *httpclient.Request
		ttl  time.Duration
	}{
		{name: "default", ttl: 5 * time.Minute},
		{name: "anthropic_without_marker", raw: &httpclient.Request{
			APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"model":"alias","messages":[]}`),
		}, ttl: 5 * time.Minute},
		{name: "implicit_5m", raw: &httpclient.Request{
			APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral"}}`),
		}, ttl: 5 * time.Minute},
		{name: "explicit_5m", raw: &httpclient.Request{
			APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"5m"}}`),
		}, ttl: 5 * time.Minute},
		{name: "1h", raw: &httpclient.Request{
			APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`),
		}, ttl: time.Hour},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, stream), func(t *testing.T) {
				f := newHealthGateSessionFixture(t)
				ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
				selected := f.selectCandidates(t, ctx)
				decision := f.complete(t, ctx, selected, 0, true, stream, tt.raw)
				require.Nil(t, decision.Owner)
				require.Nil(t, decision.Migration)
				require.False(t, decision.TemporaryFailover)
				require.Zero(t, decision.ConsecutiveFailovers)
				owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
				require.NoError(t, err)
				require.True(t, found)
				requireHealthGateSessionOwner(t, owner, selected[0].Channel.ID, 0, tt.ttl)
			})
		}
	}
}

func TestHealthGateSession_FinalBodyOverrideDeterminesProtectionWindow(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		op       objects.OverrideOperation
		finalTTL string
		ttl      time.Duration
	}{
		{
			name: "delete_1h", body: `{"cache_control":{"type":"ephemeral","ttl":"1h"}}`,
			op:  objects.OverrideOperation{Op: objects.OverrideOpDelete, Path: "cache_control"},
			ttl: 5 * time.Minute,
		},
		{
			name: "add_1h", body: `{"cache_control":{"type":"ephemeral","ttl":"5m"}}`,
			op:       objects.OverrideOperation{Op: objects.OverrideOpSet, Path: "cache_control.ttl", Value: "1h"},
			finalTTL: "1h", ttl: time.Hour,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newHealthGateSessionFixture(t)
			ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
			selected := f.selectCandidates(t, ctx)
			selected[0].Channel.Settings = &objects.ChannelSettings{BodyOverrideOperations: []objects.OverrideOperation{tt.op}}
			raw := &httpclient.Request{
				APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(tt.body),
				JSONBody: []byte(tt.body),
			}
			tracker := f.begin(t, ctx, selected, 0, raw)
			state := tracker.outbound.state
			require.Equal(t, tt.body, string(state.LlmRequest.RawRequest.Body), "inbound body remains at its original TTL")
			require.Equal(t, tt.finalTTL, gjson.GetBytes(state.RawProviderRequest.Body, "cache_control.ttl").String())
			if tt.finalTTL == "" {
				require.False(t, gjson.GetBytes(state.RawProviderRequest.Body, "cache_control").Exists())
			}
			require.NotEqual(t, gjson.GetBytes(state.RawProviderRequest.JSONBody, "cache_control.ttl").String(), tt.finalTTL,
				"stale JSONBody must not determine the protection window")
			f.finish(t, ctx, tracker, true, false)
			owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
			require.NoError(t, err)
			require.True(t, found)
			requireHealthGateSessionOwner(t, owner, selected[0].Channel.ID, 0, tt.ttl)
		})
	}
}

func TestHealthGateSession_TemporaryFailoverDoesNotRenewProtection(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	expected := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
		ChannelID: 1, ExpiresAt: time.Now().Add(time.Minute),
	})
	selected := f.selectCandidates(t, ctx)
	raw := &httpclient.Request{APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`)}
	decision := f.complete(t, ctx, selected, 1, true, false, raw)
	require.True(t, decision.TemporaryFailover)
	require.Equal(t, 1, decision.ConsecutiveFailovers)
	require.Nil(t, decision.Migration)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	expected.ConsecutiveFailovers = 1
	require.Equal(t, expected, owner, "backup success changes only the failover count, even with a 1h marker")
}

func TestHealthGateSession_ActiveOwnerSuccessRenewsProtection(t *testing.T) {
	for _, ttl := range []time.Duration{5 * time.Minute, time.Hour} {
		t.Run(ttl.String(), func(t *testing.T) {
			f := newHealthGateSessionFixture(t)
			ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
			previous := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
				ChannelID: 1, ConsecutiveFailovers: 1, ExpiresAt: time.Now().Add(time.Minute),
			})
			var raw *httpclient.Request
			if ttl == time.Hour {
				raw = &httpclient.Request{APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`)}
			}
			selected := f.selectCandidates(t, ctx)
			require.True(t, selected[0].TraceSticky)
			require.Equal(t, 1, selected[0].Channel.ID)
			decision := f.complete(t, ctx, selected, 0, true, false, raw)
			require.False(t, decision.TemporaryFailover)
			require.Nil(t, decision.Migration)
			require.Zero(t, decision.ConsecutiveFailovers)
			owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
			require.NoError(t, err)
			require.True(t, found)
			requireHealthGateSessionOwner(t, owner, 1, 0, ttl)
			require.True(t, owner.ExpiresAt.After(previous.ExpiresAt))
		})
	}
}

func TestHealthGateSession_ExpiredOwnerUsesFreshRoutingWithoutLegacy(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
		ChannelID: 1, ConsecutiveFailovers: 1, ExpiresAt: time.Now().Add(-time.Minute),
	})
	legacy, err := f.request.GetPreviousChannelIDByThread(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, legacy, "legacy entry deliberately outlives the owner lease")
	f.candidates[1].Priority = -1
	for range 2 {
		_, found, err := f.request.GetSessionOwner(ctx, 20, 10)
		require.NoError(t, err)
		require.False(t, found)
		selected := f.selectCandidates(t, ctx)
		require.Equal(t, 2, selected[0].Channel.ID)
		require.False(t, selected[0].TraceSticky)
		require.Nil(t, selected[0].healthGate.decision.record.Owner)
		require.Zero(t, selected[0].healthGate.decision.record.ConsecutiveFailovers)
	}
	decision := f.complete(t, ctx, f.selectCandidates(t, ctx), 0, true, false)
	require.Nil(t, decision.Owner)
	require.Nil(t, decision.Migration)
	require.False(t, decision.TemporaryFailover)
	require.Zero(t, decision.ConsecutiveFailovers)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
}

func TestHealthGateSession_DifferentSessionsDoNotInheritProtection(t *testing.T) {
	for _, threaded := range []bool{false, true} {
		t.Run(fmt.Sprintf("thread=%t", threaded), func(t *testing.T) {
			f := newHealthGateSessionFixture(t)
			firstTrace := &ent.Trace{ID: 10}
			secondTrace := &ent.Trace{ID: 11}
			if threaded {
				firstTrace.ThreadID, secondTrace.ThreadID = 20, 21
			}
			firstCtx := contexts.WithTrace(f.ctx, firstTrace)
			secondCtx := contexts.WithTrace(f.ctx, secondTrace)
			raw := &httpclient.Request{APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`)}
			f.complete(t, firstCtx, f.selectCandidates(t, firstCtx), 0, true, false, raw)
			original, found, err := f.request.GetSessionOwner(firstCtx, firstTrace.ThreadID, firstTrace.ID)
			require.NoError(t, err)
			require.True(t, found)
			requireHealthGateSessionOwner(t, original, 1, 0, time.Hour)
			f.candidates[1].Priority = -1
			_, found, err = f.request.GetSessionOwner(secondCtx, secondTrace.ThreadID, secondTrace.ID)
			require.NoError(t, err)
			require.False(t, found)
			selected := f.selectCandidates(t, secondCtx)
			require.Equal(t, 2, selected[0].Channel.ID)
			require.False(t, selected[0].TraceSticky)
			require.Nil(t, selected[0].healthGate.decision.record.Owner)
			f.complete(t, secondCtx, selected, 0, true, false)
			owner, found, err := f.request.GetSessionOwner(secondCtx, secondTrace.ThreadID, secondTrace.ID)
			require.NoError(t, err)
			require.True(t, found)
			requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
			owner, found, err = f.request.GetSessionOwner(firstCtx, firstTrace.ThreadID, firstTrace.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, original, owner)
			require.Equal(t, 1, f.selectCandidates(t, firstCtx)[0].Channel.ID)
		})
	}
}

func TestHealthGateSession_LateExpiredOwnerCannotRestoreBinding(t *testing.T) {
	for _, freshSucceeded := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh_succeeded=%t", freshSucceeded), func(t *testing.T) {
			f := newHealthGateSessionFixture(t)
			ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
			expired := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
				ChannelID: 1, ConsecutiveFailovers: 1, ExpiresAt: time.Now().Add(time.Minute),
			})
			selected := f.selectCandidates(t, ctx)
			require.Equal(t, 1, selected[0].Channel.ID)
			slow := f.begin(t, ctx, selected, 0, &httpclient.Request{
				APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`),
			})
			require.True(t, slow.active, "the former owner request starts before its lease expires")
			// Advance the stored lease past its logical deadline without sleeping.
			expired.ExpiresAt = time.Now().Add(-time.Minute)
			setHealthGateSessionOwner(f.request, ctx, 20, 10, expired)
			f.candidates[1].Priority = -1
			fresh := f.selectCandidates(t, ctx)
			require.Equal(t, 2, fresh[0].Channel.ID, "channel 2 represents fresh channel 15")
			require.False(t, fresh[0].TraceSticky)
			require.Nil(t, fresh[0].healthGate.decision.record.Owner)
			fast := f.begin(t, ctx, fresh, 0, nil)
			var current biz.SessionOwner
			if freshSucceeded {
				decision := f.finish(t, ctx, fast, true, false)
				require.Nil(t, decision.Migration)
				var found bool
				var err error
				current, found, err = f.request.GetSessionOwner(ctx, 20, 10)
				require.NoError(t, err)
				require.True(t, found)
				requireHealthGateSessionOwner(t, current, 2, 0, 5*time.Minute)
			}
			decision := f.finish(t, ctx, slow, true, false)
			require.Equal(t, 1, decision.Owner.ChannelID)
			require.Nil(t, decision.Migration)
			require.False(t, decision.TemporaryFailover)
			owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
			require.NoError(t, err)
			if freshSucceeded {
				require.True(t, found)
				require.Equal(t, current, owner, "late owner 1 cannot alter the current channel, deadline or count")
			} else {
				require.False(t, found, "late success cannot resurrect an expired binding while fresh routing is in flight")
				require.False(t, f.selectCandidates(t, ctx)[0].TraceSticky)
				f.finish(t, ctx, fast, true, false)
				owner, found, err = f.request.GetSessionOwner(ctx, 20, 10)
				require.NoError(t, err)
				require.True(t, found)
				requireHealthGateSessionOwner(t, owner, 2, 0, 5*time.Minute)
			}
			require.Equal(t, 2, f.selectCandidates(t, ctx)[0].Channel.ID)
		})
	}
}

func TestHealthGateSession_LateFreshCompletionCannotReplaceEstablishedOwner(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	f.policy.HealthGate.OwnerFailoverThreshold = 1
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	selected := f.selectCandidates(t, ctx)
	require.False(t, selected[0].healthGate.decision.found)
	require.Equal(t, 1, selected[0].Channel.ID)
	slow := f.begin(t, ctx, selected, 0, nil)
	f.candidates[1].Priority = -1
	fresh := f.selectCandidates(t, ctx)
	require.Equal(t, 2, fresh[0].Channel.ID)
	require.False(t, fresh[0].healthGate.decision.found)
	f.complete(t, ctx, fresh, 0, true, false)
	current, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, current, 2, 0, 5*time.Minute)
	decision := f.finish(t, ctx, slow, true, false)
	require.Nil(t, decision.Owner)
	require.Nil(t, decision.Migration, "threshold 1 must not turn the late fresh request into a migration")
	require.False(t, decision.TemporaryFailover)
	require.Zero(t, decision.ConsecutiveFailovers)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, current, owner)
	require.Equal(t, 2, f.selectCandidates(t, ctx)[0].Channel.ID)
}

func TestHealthGateSession_LateFreshSameChannelSuccessRenewsProtection(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	selected := f.selectCandidates(t, ctx)
	require.False(t, selected[0].healthGate.decision.found)
	slow := f.begin(t, ctx, selected, 0, &httpclient.Request{
		APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`),
	})
	fresh := f.selectCandidates(t, ctx)
	require.False(t, fresh[0].healthGate.decision.found)
	require.Equal(t, selected[0].Channel.ID, fresh[0].Channel.ID)
	f.complete(t, ctx, fresh, 0, true, false)
	current, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, current, 1, 0, 5*time.Minute)
	decision := f.finish(t, ctx, slow, true, false)
	require.Nil(t, decision.Migration)
	require.False(t, decision.TemporaryFailover)
	require.Zero(t, decision.ConsecutiveFailovers)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, owner, 1, 0, time.Hour)
	require.True(t, owner.ExpiresAt.After(current.ExpiresAt))
	require.Equal(t, current.Generation, owner.Generation, "renewal must preserve the established binding generation")
}

func TestHealthGateSession_LateFormerOwnerRetryCannotMigrateFreshOwner(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	f.candidates[0].Channel.ID = 10
	f.candidates[1].Channel.ID = 20
	third := healthGateSelectorCandidate(15, 2, "third")
	third.Channel.Name = "fresh owner"
	f.candidates = append(f.candidates, third)
	f.selector.wrapped.(*staticChannelSelector).candidates = f.candidates
	f.policy.MaxChannelRetries = 2
	f.policy.HealthGate.OwnerFailoverThreshold = 1
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	expired := setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
		ChannelID: 10, ExpiresAt: time.Now().Add(time.Minute),
	})
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 10, selected[0].Channel.ID)
	require.Equal(t, 20, selected[1].Channel.ID)
	slow := f.begin(t, ctx, selected, 1, nil)
	require.True(t, slow.active)
	require.Equal(t, 10, slow.decision.owner.ChannelID)
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	setHealthGateSessionOwner(f.request, ctx, 20, 10, expired)
	third.Priority = -1
	fresh := f.selectCandidates(t, ctx)
	require.Equal(t, 15, fresh[0].Channel.ID)
	require.False(t, fresh[0].TraceSticky)
	f.complete(t, ctx, fresh, 0, true, false)
	current, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	requireHealthGateSessionOwner(t, current, 15, 0, 5*time.Minute)
	decision := f.finish(t, ctx, slow, true, false)
	require.Equal(t, 10, decision.Owner.ChannelID)
	require.Nil(t, decision.Migration, "the former owner's retry must not migrate fresh owner 15, even at threshold 1")
	require.False(t, decision.TemporaryFailover)
	require.Zero(t, decision.ConsecutiveFailovers)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, current, owner, "late channel 20 success must preserve channel 15's deadline and count")
	require.Equal(t, 15, f.selectCandidates(t, ctx)[0].Channel.ID)
}

func TestHealthGateSession_LateFallbackCannotAlterReestablishedSameChannel(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	f.policy.HealthGate.OwnerFailoverThreshold = 1
	require.NoError(t, f.selector.policy.(*biz.SystemService).SetRetryPolicy(ctx, f.policy))
	f.complete(t, ctx, f.selectCandidates(t, ctx), 0, true, false)
	// Simulate a clock correction so the assertion does not depend on the
	// platform clock ticking between two quick bindings of the same channel.
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{
		ChannelID: 1, Generation: time.Now().Add(time.Minute),
	})
	selected := f.selectCandidates(t, ctx)
	slow := f.begin(t, ctx, selected, 1, nil)
	old := slow.decision.owner
	old.ExpiresAt = time.Now().Add(-time.Minute)
	setHealthGateSessionOwner(f.request, ctx, 20, 10, old)
	fresh := f.selectCandidates(t, ctx)
	require.False(t, fresh[0].TraceSticky)
	require.Equal(t, 1, fresh[0].Channel.ID)
	f.complete(t, ctx, fresh, 0, true, false)
	current, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, old.Generation, current.Generation)
	require.True(t, current.Generation.After(old.Generation), "new bindings must advance past the retained identity even after a clock correction")
	decision := f.finish(t, ctx, slow, true, false)
	require.Nil(t, decision.Migration)
	require.False(t, decision.TemporaryFailover)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, current, owner)
}
