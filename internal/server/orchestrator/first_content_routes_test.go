package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	entchannel "github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestFirstContentComparesDistinctComparableSessions(t *testing.T) {
	now := time.Now()
	keys := []firstContentRoute{{channel: 1}, {channel: 2}}
	for _, tc := range []struct {
		name                           string
		repeat, stale, different, fail bool
		want                           int
	}{
		{name: "comparable", want: 1}, {name: "one session", repeat: true}, {name: "expired", stale: true},
		{name: "different actual tier", different: true}, {name: "peer failure", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := new(firstContentRoutes)
			for i := 0; i < 25; i++ {
				for j, key := range keys {
					row := firstContentSample{session: uint64(i), at: now.Add(-time.Duration(i) * 30 * time.Second), tokens: 9000, cachePercent: 80, reportedTier: "default", delay: time.Duration(10-j*8) * time.Second}
					if tc.repeat {
						row.session = 0
					}
					if tc.stale {
						row.at = row.at.Add(-time.Hour)
					}
					if j == 1 && tc.different {
						row.reportedTier = "priority"
					}
					if j == 1 && tc.fail && i == 0 {
						row.failed = true
					}
					h.record(key, row)
				}
			}
			d := h.choose(keys, 1, 0, time.Second, now)
			require.Equal(t, 0, d.chosen)
			require.Equal(t, tc.want, d.suggested)
			if tc.want == 1 {
				require.Equal(t, 1, h.choose(keys, 1, 10, time.Second, now).chosen)
				require.Equal(t, 0, h.choose(keys, 81, 10, time.Second, now).chosen)
			}
		})
	}
}

func TestFirstContentRejectsProviderModelMismatch(t *testing.T) {
	now := time.Now()
	h := new(firstContentRoutes)
	keys := []firstContentRoute{{channel: 1}, {channel: 2}}
	for j, k := range keys {
		for i := 0; i < 25; i++ {
			h.record(k, firstContentSample{session: uint64(i), at: now.Add(-time.Duration(i) * 25 * time.Second), delay: time.Duration(10-j*8) * time.Second, tokens: 500, cachePercent: 10, reportedTier: "default"})
		}
	}
	require.Equal(t, 1, h.choose(keys, 101, 10, time.Second, now).chosen)
	h.record(keys[1], firstContentSample{session: 999, at: now, failed: true, mismatched: true})
	d := h.choose(keys, 101, 10, time.Second, now)
	require.Zero(t, d.chosen, "a mismatching upstream model must also suppress exploratory traffic")
	require.Zero(t, d.suggested)
}

func TestFirstContentChannelTrialUsesExistingSelectorAndPricing(t *testing.T) {
	ctx, db := setupTest(t)
	service, rs, _, _ := setupTestServices(t, db)
	rs.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), FirstContentTrialPercent: 10})
	session := ""
	for i := 0; ; i++ {
		session = fmt.Sprintf("route-%d", i)
		if xxhash.Sum64String(session)%100 < 10 {
			break
		}
	}
	ctx, _ = contexts.WithRequestClock(shared.WithSessionID(shared.WithResponsesAPI(ctx), session), time.Now())
	ctx = contexts.WithTrace(ctx, &ent.Trace{ID: 11, ProjectID: 3, TraceID: session})
	card := &objects.ModelCard{Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage}}}}
	group := make([]*ChannelModelsCandidate, 0, 2)
	for _, name := range []string{"source-one", "source-two"} {
		row := db.Channel.Create().SetName(name).SetType(entchannel.TypeOpenaiResponses).SetBaseURL("https://" + name + ".example").
			SetCredentials(objects.ChannelCredentials{APIKey: "synthetic-" + name}).SetSupportedModels([]string{"model"}).
			SetDefaultTestModel("model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: string(llm.APIFormatOpenAIResponse)}}).SaveX(ctx)
		ch, err := service.GetChannel(ctx, row.ID)
		require.NoError(t, err)
		group = append(group, &ChannelModelsCandidate{Channel: ch, APIFormat: string(llm.APIFormatOpenAIResponse), Models: []biz.ChannelModelEntry{{ActualModel: "model"}}, SessionState: "new", ModelCard: card})
	}
	raw := &httpclient.Request{Headers: make(http.Header), Body: []byte("{}")}
	req := &llm.Request{Stream: new(true), ReasoningEffort: "high", RawRequest: raw}
	project := db.Project.Create().SetName("price-trial").SaveX(ctx)
	key := db.APIKey.Create().SetProjectID(project.ID).SetName("trial").SetKey("fixture-price-trial").SaveX(ctx)
	ctx = contexts.WithTrace(ctx, &ent.Trace{ID: 11, ProjectID: project.ID, TraceID: session})
	state := &PersistenceState{routeHistory: new(firstContentRoutes), APIKey: key, Request: &ent.Request{ProjectID: project.ID, TraceID: 11, APIKeyID: key.ID},
		RequestService: rs, RawRequest: raw, LlmRequest: req, RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyAdaptive}}
	ctx = context.WithValue(ctx, firstContentStateKey{}, state)
	s := newFirstContentSort(ctx, state, req, group)
	require.NotNil(t, s)
	now := time.Now()
	for j, c := range group {
		key, ok := s.channelKey(c)
		require.True(t, ok)
		for i := 0; i < 25; i++ {
			state.routeHistory.record(key, firstContentSample{session: uint64(i), at: now.Add(-time.Duration(i) * 20 * time.Second), delay: time.Duration(10-j*8) * time.Second, tokens: 500, cachePercent: 20, reportedTier: "default"})
		}
	}
	selector := new(LoadBalancedSelector)
	lb := NewLoadBalancer(nil, nil).WithStableTieOrder()
	for _, debug := range []bool{false, true} {
		lb.debug = debug
		got := selector.sortCandidates(ctx, lb, group, req, 1, 1, true)
		require.Equal(t, group[1], got[0])
	}
	group[1].Channel.Settings = &objects.ChannelSettings{TransformOptions: objects.TransformOptions{
		ReasoningEffortMapping: []llm.ReasoningEffortMapping{{From: "high", To: "low"}},
	}}
	require.Equal(t, group[0], selector.sortCandidates(ctx, lb, group, req, 1, 1, true)[0])
	group[1].Channel.Settings = &objects.ChannelSettings{RateLimit: &objects.ChannelRateLimit{MaxConcurrent: new(int64(1))}}
	state.routeLimiter = NewChannelLimiterManager()
	limiter := state.routeLimiter.GetOrCreate(group[1].Channel)
	require.NoError(t, limiter.Acquire(ctx))
	require.Equal(t, group[0], selector.sortCandidates(ctx, lb, group, req, 1, 1, true)[0])
	limiter.Release()
	group[1].ModelCard = nil
	want := lb.SortWithoutTracking(t.Context(), group, "model", true, 1)
	got := selector.sortCandidates(ctx, lb, group, req, 1, 1, true)
	require.Equal(t, want, got, "unknown price must preserve the original order")
}

func TestFirstContentBusyRoutesRetainBothWindows(t *testing.T) {
	now := time.Now()
	h := new(firstContentRoutes)
	keys := []firstContentRoute{{channel: 1}, {channel: 2}}
	for j, k := range keys {
		for i := 0; i < 800; i++ {
			h.record(k, firstContentSample{session: uint64(i), at: now.Add(time.Duration(i-800) * time.Second),
				delay: time.Duration(10-j*8) * time.Second, reportedTier: "standard", tokens: 200})
		}
		require.Len(t, h.groups[k].samples, routeMaxSamples)
	}
	require.Equal(t, 1, h.choose(keys, 1, 10, time.Second, now).chosen)
}

func TestReportedCacheTokensDistinguishMissingAndZero(t *testing.T) {
	for _, tc := range []struct {
		format llm.APIFormat
		zero   string
	}{
		{llm.APIFormatOpenAIChatCompletion, `{"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`},
		{llm.APIFormatOpenAIResponse, `{"type":"response.completed","response":{"usage":{"input_tokens_details":{"cached_tokens":0}}}}`},
		{llm.APIFormatAnthropicMessage, `{"type":"message_start","message":{"usage":{"cache_read_input_tokens":0}}}`},
		{llm.APIFormatGeminiContents, `{"usageMetadata":{"cachedContentTokenCount":0}}`},
	} {
		n, ok := reportedCacheTokens(tc.format, []*httpclient.StreamEvent{{Data: []byte(tc.zero)}})
		require.True(t, ok, tc.format)
		require.Zero(t, n)
		_, ok = reportedCacheTokens(tc.format, []*httpclient.StreamEvent{{Data: []byte("{}")}})
		require.False(t, ok)
	}
}

func TestFirstContentAddressTrialPreservesPriorityContinuationAndFallback(t *testing.T) {
	session := ""
	for i := 0; ; i++ {
		session = fmt.Sprintf("session-%d", i)
		h := xxhash.Sum64String(session)
		if h%100 < 10 && h/100%2 == 1 {
			break
		}
	}
	ctx, db := setupTest(t)
	service, rs, _, _ := setupTestServices(t, db)
	ctx, clock := contexts.WithRequestClock(shared.WithResponsesAPI(ctx), time.Now().Add(-time.Second))
	ctx = contexts.WithTrace(ctx, &ent.Trace{ID: 11, ProjectID: 3, TraceID: session})
	ctx = contexts.EnsureContainer(ctx)
	ctx = contexts.WithChannelAPIKey(ctx, "fixture")
	addresses := []objects.ChannelUpstreamAddress{{ID: "a", URL: "https://a.test", Priority: 1, Enabled: true}, {ID: "b", URL: "https://b.test", Priority: 1, Enabled: true}, {ID: "c", URL: "https://c.test", Priority: 2, Enabled: true}}
	row := db.Channel.Create().SetName("address-fixture").SetType(entchannel.TypeOpenaiResponses).SetBaseURL("https://a.test").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").
		SetSettings(&objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: addresses}).SaveX(ctx)
	ch, err := service.GetChannel(ctx, row.ID)
	require.NoError(t, err)
	c := &ChannelModelsCandidate{Channel: ch, SessionState: "new", APIFormat: string(llm.APIFormatOpenAIResponse), Models: []biz.ChannelModelEntry{{ActualModel: "model"}}}
	rs.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), FirstContentTrialPercent: 10})
	s := &PersistenceState{routeHistory: new(firstContentRoutes), APIKey: &ent.APIKey{ID: 4}, Request: &ent.Request{ProjectID: 3, APIKeyID: 4, TraceID: 11}, CurrentCandidate: c,
		LlmRequest: &llm.Request{Stream: new(true), ReasoningEffort: "high"}, RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyAdaptive},
		RequestService: rs}
	p := &PersistentOutboundTransformer{state: s}
	raw := &httpclient.Request{URL: "https://a.test/v1/responses", Headers: make(http.Header), Body: []byte("{}")}
	m := applyUpstreamAddress(p)
	out, err := m.OnOutboundRawRequest(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, "https://b.test/v1/responses", out.URL)
	require.Equal(t, "a", addresses[0].ID)
	require.Equal(t, "explore", s.routeDecision.mode)
	p.addressIndex = 1
	out, err = m.OnOutboundRawRequest(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, raw.URL, out.URL)
	// This unit check drives the clock directly. The HTTP integration test
	// exercises the actual writer, provider accounting and stream finalization.
	exec := &ent.RequestExecution{ID: 1, ChannelID: row.ID, ModelID: "model", ReasoningEffort: new("high"), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{}}}
	clock.AddAttempt(1, row.ID, nil)
	s.observeFirstContentChoice(ctx, exec)
	s.StreamCompleted = true
	s.routeCompletion = firstContentCompletion{model: "model", tier: "default", tokens: 100, valid: true}
	clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.output_text.delta","delta":"x"}`)})
	clock.Wrote(true)
	clock.Finish(false)
	samples := s.routeHistory.groups[s.routeDecision.keys[1]].samples
	require.Len(t, samples, 1)
	require.False(t, samples[0].failed)
	require.EqualValues(t, 100, samples[0].tokens)
	p.addressIndex = 0
	p.continuationAddresses = nil
	s.routeDecision = nil
	c.continuationBound = true
	out, err = m.OnOutboundRawRequest(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, raw.URL, out.URL)
	require.Nil(t, s.routeDecision)
	c.continuationBound = false
	c.SessionState = "reused"
	_, err = m.OnOutboundRawRequest(ctx, raw)
	require.NoError(t, err)
	require.Nil(t, s.routeDecision)
	c.SessionState = "new"
	ch.Settings.UpstreamAddresses = addresses[:1]
	_, err = m.OnOutboundRawRequest(context.Background(), raw)
	require.NoError(t, err)
	require.Nil(t, s.routeDecision)
}

func TestFirstContentObservePreservesTiedRetryOrder(t *testing.T) {
	ctx := t.Context()
	lb := NewLoadBalancer(nil, nil)
	var group []*ChannelModelsCandidate
	for id := 1; id <= 7; id++ {
		ch := &biz.Channel{Channel: &ent.Channel{ID: id}}
		group = append(group, &ChannelModelsCandidate{Channel: ch, SessionState: "new"})
	}
	// Unknown prices and zero rollout both leave the legacy top-k result intact.
	state := &PersistenceState{RawRequest: &httpclient.Request{}}
	state.routeSort = &firstContentSort{ctx: ctx, state: state}
	observed := context.WithValue(ctx, firstContentStateKey{}, state)
	for _, debug := range []bool{false, true} {
		lb.debug = debug
		for _, k := range []int{1, 2, 3} {
			want := lb.SortWithoutTracking(ctx, group, "model", true, k)
			got := lb.SortWithoutTracking(observed, group, "model", true, k)
			require.Equal(t, want, got, "debug=%t, topK=%d", debug, k)
		}
	}
}
