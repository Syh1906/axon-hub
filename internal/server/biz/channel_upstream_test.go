package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestUpstreamRefreshManualPriorityAndInvalidation(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/usage":
			_, _ = w.Write([]byte(`{"isValid":true,"balance":128.6}`))
		case "/v1/sub2api/billing":
			rate := 0.5
			if r.Header.Get("Authorization") == "Bearer fixture-b" {
				rate = 0.8
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "sub2api.key_billing", "schema_version": 1, "billing_scope": "token", "effective_rate_multiplier": rate, "observed_at": time.Now().UTC().Format(time.RFC3339)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.httpClient = httpclient.NewHttpClient()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "upstream", []string{"priced"})
	ch = ch.Update().SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-a", "fixture-b"}}).SaveX(ctx)
	_, err := svc.RefreshChannelUpstreams(context.Background(), []int{ch.ID}, true, true, nil)
	require.Error(t, err, "authorization is checked before contacting upstream")
	result, err := refreshTestUpstreamChannel(svc, ctx, ch.ID, false, false)
	require.NoError(t, err)
	require.Equal(t, 1, result.Skipped)
	require.Zero(t, calls.Load())
	result, err = refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	require.Len(t, result.Channels[0].Keys, 2)
	require.Equal(t, "0.5", result.Channels[0].Keys[0].UpstreamMultiplier.String())
	require.Equal(t, objects.CostMultiplierManual, result.Channels[0].Keys[0].Applied.Source)
	state, err := svc.UpdateChannelUpstream(ctx, ch.ID, objects.UpdateChannelUpstreamInput{Enabled: true, RefreshIntervalMinutes: 10, CostMode: objects.ChannelCostModeUpstream, ManualMultiplier: decimal.NewFromInt(2)})
	require.NoError(t, err)
	require.Equal(t, "0.8", state.Keys[1].Applied.Value.String())
	before := calls.Load()
	_, err = refreshTestUpstreamChannel(svc, ctx, ch.ID, false, false)
	require.NoError(t, err)
	require.Equal(t, before, calls.Load(), "fresh snapshots issue no requests")
	_, err = svc.UpdateChannelCostMultiplier(ctx, ch.ID, decimal.RequireFromString("0.25"))
	require.NoError(t, err)
	result, err = refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	require.Equal(t, objects.ChannelCostModeManual, result.Channels[0].Settings.CostMode)
	require.Equal(t, "0.25", result.Channels[0].Keys[0].Applied.Value.String())
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ExtraModelPrefix: "test-"}})
	require.NoError(t, err)
	require.True(t, db.Channel.GetX(ctx, ch.ID).Settings.Upstream.Enabled)
	ch = db.Channel.GetX(ctx, ch.ID).Update().SetBaseURL(server.URL + "/changed").SaveX(ctx)
	states, err := svc.ChannelUpstreamStates(ctx, []int{ch.ID})
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQueryNever, states[0].Keys[0].Snapshot.RateState)
	_, err = svc.UpdateChannelUpstream(ctx, ch.ID, objects.UpdateChannelUpstreamInput{Enabled: false, RefreshIntervalMinutes: 10, CostMode: objects.ChannelCostModeUpstream, ManualMultiplier: decimal.NewFromInt(2)})
	require.NoError(t, err)
	require.Equal(t, objects.ChannelCostModeManual, db.Channel.GetX(ctx, ch.ID).Settings.Upstream.CostMode)
}

func TestAppliedUpstreamRateExpiryAndZero(t *testing.T) {
	now := time.Now()
	settings := objects.ChannelUpstreamSettings{Enabled: true, RefreshIntervalMinutes: 10, CostMode: objects.ChannelCostModeUpstream}
	snapshot := upstream.EmptySnapshot()
	snapshot.RateState = objects.UpstreamQuerySuccess
	snapshot.Rate = &objects.UpstreamRateSnapshot{Multiplier: decimal.Zero, ObservedAt: now, SyncedAt: now}
	require.Equal(t, "0", appliedUpstreamMultiplier(settings, decimal.NewFromInt(2), snapshot, now).Value.String())
	snapshot.RateState = objects.UpstreamQueryError
	require.Equal(t, objects.CostMultiplierCached, appliedUpstreamMultiplier(settings, decimal.NewFromInt(2), snapshot, now.Add(20*time.Minute)).Source)
	require.Equal(t, objects.CostMultiplierEstimated, appliedUpstreamMultiplier(settings, decimal.NewFromInt(2), snapshot, now.Add(31*time.Minute)).Source)
	snapshot.Rate.ValidUntil = lo.ToPtr(now.Add(time.Minute))
	require.Equal(t, objects.CostMultiplierEstimated, appliedUpstreamMultiplier(settings, decimal.NewFromInt(2), snapshot, now.Add(2*time.Minute)).Source)
	settings.Enabled = false
	require.Equal(t, objects.CostMultiplierManual, appliedUpstreamMultiplier(settings, decimal.NewFromInt(2), snapshot, now).Source)
}

func TestUpstreamQueryBudgetAndCanceledLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	queryCtx, queryCancel := upstreamQueryContext(ctx)
	defer queryCancel()
	parentDeadline, _ := ctx.Deadline()
	queryDeadline, _ := queryCtx.Deadline()
	require.Equal(t, 2*time.Second, parentDeadline.Sub(queryDeadline))
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.upstreamRefreshOnce.Do(func() {
		for i := range svc.upstreamRefreshLocks {
			svc.upstreamRefreshLocks[i] = make(chan struct{}, 1)
		}
	})
	lock := svc.upstreamRefreshLocks[1]
	lock <- struct{}{}
	defer func() { <-lock }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer waitCancel()
	_, _, err := svc.refreshChannelUpstream(waitCtx, 1, true, true, false, "key")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRequestCostSnapshotUsesSelectedKeyAndSurvivesLaterChanges(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "snapshot", []string{"priced"})
	ch = ch.Update().SetStatus(channel.StatusEnabled).SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-a", "fixture-b"}}).SetSettings(&objects.ChannelSettings{Upstream: &objects.ChannelUpstreamSettings{Enabled: true, RefreshIntervalMinutes: 10, CostMode: objects.ChannelCostModeUpstream}}).SaveX(ctx)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeUsagePerUnit, 2)}}
	versions, err := svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "priced", Price: price}}, nil)
	require.NoError(t, err)
	ch = db.Channel.GetX(ctx, ch.ID)
	loaded := &Channel{Channel: ch, cachedModelPrices: map[string]*ent.ChannelModelPrice{"priced": versions[0]}}
	for i, key := range []string{"fixture-a", "fixture-b"} {
		now := time.Now().UTC()
		snapshot := upstream.EmptySnapshot()
		snapshot.RateState = objects.UpstreamQuerySuccess
		snapshot.Rate = &objects.UpstreamRateSnapshot{Multiplier: decimal.NewFromFloat([]float64{0.5, 0.8}[i]), ObservedAt: now, SyncedAt: now}
		db.ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(upstream.KeyRef(key)).SetConfigFingerprint(upstreamConfigFingerprint(ch, upstream.KeyRef(key))).SetSnapshot(snapshot).SaveX(ctx)
	}
	ctx = contexts.WithChannelAPIKey(ctx, "fixture-a")
	a := captureRequestCostSnapshot(ctx, db, loaded, "priced", nil)
	ctx = contexts.WithChannelAPIKey(ctx, "fixture-b")
	b := captureRequestCostSnapshot(ctx, db, loaded, "priced", nil)
	require.Equal(t, "0.5", a.Applied.Value.String())
	require.Equal(t, "0.8", b.Applied.Value.String())
	_, err = svc.UpdateChannelCostMultiplier(ctx, ch.ID, decimal.NewFromInt(9))
	require.NoError(t, err)
	usageSvc := NewUsageLogService(db, svc.SystemService, svc)
	usage := &llm.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000}
	usageSvc.InjectUsageCost(ctx, ch.ID, "priced", usage, a)
	require.Equal(t, 1.0, *usage.Cost)
	// Simulate a record restored from JSON without its transient price pointer.
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	var restored objects.RequestCostSnapshot
	require.NoError(t, json.Unmarshal(raw, &restored))
	_, cost, ref := usageSvc.computeSnapshotUsageCost(ctx, usage, &restored)
	require.Equal(t, 1.6, *cost)
	require.Equal(t, versions[0].ReferenceID, ref)
	flat := objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 2)}}
	flatItems, flatCost := computeCostFromSnapshot(usage, flat, b)
	require.Equal(t, "2", flatCost.String(), "independent flat charges use the captured manual multiplier")
	require.Equal(t, "1", b.DisplayMultiplier(flatItems).String())
	flat.Items[0].ItemCode = objects.PriceItemCodeCompletion
	mixed := objects.ModelPrice{Items: append(price.Items, flat.Items...)}
	mixedItems, mixedCost := computeCostFromSnapshot(usage, mixed, b)
	require.Equal(t, "3.6", mixedCost.String())
	require.Nil(t, b.DisplayMultiplier(mixedItems), "mixed token and flat charges must not display one multiplier")
	cache := createPriceItem(objects.PricingModeUsagePerUnit, 2)
	cache.ItemCode = objects.PriceItemCodeWriteCachedTokens
	cache.PromptWriteCacheVariants = []objects.PromptWriteCacheVariant{{
		VariantCode: objects.PromptWriteCacheVariantCode5Min,
		Pricing:     createPriceItem(objects.PricingModeFlatFee, 2).Pricing,
	}}
	cacheUsage := &llm.Usage{PromptTokensDetails: &llm.PromptTokensDetails{WriteCached5MinTokens: 100}}
	cacheItems, cacheCost := computeCostFromSnapshot(cacheUsage, objects.ModelPrice{Items: []objects.ModelPriceItem{cache}}, b)
	require.Equal(t, "2", cacheCost.String(), "the selected cache variant determines whether the charge is flat")
	require.Equal(t, "1", b.DisplayMultiplier(cacheItems).String())
}

// 页面按 Key 分别刷新；这里沿同样顺序复用现有业务断言。
func refreshTestUpstreamChannel(svc *ChannelService, ctx context.Context, id int, force, includeDisabled bool) (*objects.RefreshChannelUpstreamsResult, error) {
	states, err := svc.ChannelUpstreamStates(ctx, []int{id})
	if err != nil {
		return nil, err
	}
	var result *objects.RefreshChannelUpstreamsResult
	for _, key := range states[0].Keys {
		result, err = svc.RefreshChannelUpstreams(ctx, []int{id}, force, includeDisabled, &key.KeyRef)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
