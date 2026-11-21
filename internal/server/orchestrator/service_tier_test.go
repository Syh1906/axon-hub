package orchestrator

import (
	"net/http"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestServiceTierObservesOnlyTerminalResponsesAndIsolatesAttempts(t *testing.T) {
	require.True(t, IsTerminalStreamEvent(&httpclient.StreamEvent{Type: "response.done"}))
	require.True(t, IsTerminalStreamEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.done"}`)}))
	e := &ent.RequestExecution{Format: string(llm.APIFormatOpenAIResponse), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "fast"}}}
	observeServiceTier(e, []byte(`{"type":"response.created","response":{"service_tier":"default"}}`), true)
	require.False(t, e.CostSnapshot.ServiceTier.Observed)
	observeServiceTier(e, []byte(`{"type":"response.completed","response":{"service_tier":"priority"}}`), true)
	require.True(t, e.CostSnapshot.ServiceTier.Observed)
	require.False(t, e.CostSnapshot.ServiceTier.Conflict)
	require.Equal(t, "priority", e.CostSnapshot.ServiceTier.Upstream)
	other := &ent.RequestExecution{Format: e.Format, CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{}}}
	observeServiceTier(other, []byte(`{"type":"response.completed","response":{}}`), true)
	require.False(t, other.CostSnapshot.ServiceTier.Observed)
}

func TestServiceTierChatFinalUsageOverridesIntermediateAndDetectsFinalConflict(t *testing.T) {
	e := &ent.RequestExecution{Format: string(llm.APIFormatOpenAIChatCompletion), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{}}}
	observeServiceTier(e, []byte(`{"service_tier":"priority","choices":[{}]}`), true)
	observeServiceTier(e, []byte(`{"service_tier":"default","choices":[],"usage":{"prompt_tokens":10}}`), true)
	require.Equal(t, "default", e.CostSnapshot.ServiceTier.Upstream)
	require.False(t, e.CostSnapshot.ServiceTier.Conflict)
	observeServiceTier(e, []byte(`{"service_tier":"ultrafast","choices":[],"usage":{"prompt_tokens":10}}`), true)
	require.True(t, e.CostSnapshot.ServiceTier.Conflict)
}

func TestPeerBillingTierReadsFinalStreamEvidence(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponseWebSocket} {
		e := &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, UseOutboundTier: true, Outbound: "priority"}
		exec := &ent.RequestExecution{Format: string(format), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: e}}
		chunk := `{"choices":[{"delta":{}}],"axonhub_billing_tier":"fast"}`
		final := `{"choices":[],"usage":{"prompt_tokens":10},"service_tier":"priority","axonhub_billing_tier":"standard"}`
		if responsesTierFormat(format) {
			chunk = `{"type":"response.created","response":{"axonhub_billing_tier":"fast"}}`
			final = `{"type":"response.completed","response":{"usage":{"input_tokens":10},"service_tier":"priority","axonhub_billing_tier":"standard"}}`
		}
		observePeerBillingTier(exec, []byte(chunk), true)
		require.Empty(t, e.PeerBillingTier)
		observePeerBillingTier(exec, []byte(final), true)
		observeServiceTier(exec, []byte(final), true)
		require.Equal(t, "standard", e.Decision().Mode)
		require.Equal(t, "priority", e.Upstream)
		require.Equal(t, "fast", e.RequestDecision().Mode)
	}
}

func TestChatBillingTierAfterFinishUsesFinalUsage(t *testing.T) {
	settings := objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}
	inner := &objects.ServiceTierEvidence{Settings: settings, Client: "standard"}
	outer := &objects.ServiceTierEvidence{Settings: settings, Client: "fast"}
	execution := func(e *objects.ServiceTierEvidence) *ent.RequestExecution {
		return &ent.RequestExecution{Format: string(llm.APIFormatOpenAIChatCompletion), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: e}}
	}
	state := &PersistenceState{RequestExec: execution(inner), RawRequest: &httpclient.Request{Headers: http.Header{httpclient.AcceptBillingTierHeader: {"1"}}}, LlmRequest: &llm.Request{APIFormat: llm.APIFormatOpenAIChatCompletion}}
	chunks := []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"index":0,"delta":{}},{"index":1,"finish_reason":"stop"}],"service_tier":"priority"}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{}}],"service_tier":"default","usage":{"prompt_tokens":10,"completion_tokens":2}}`)},
	}
	source := streams.Map(streams.SliceStream(chunks), func(ev *httpclient.StreamEvent) *httpclient.StreamEvent {
		observeServiceTier(state.RequestExec, ev.Data, true)
		return ev
	})
	wire, err := (&peerBillingTierMiddleware{state: state}).OnInboundRawStream(t.Context(), source)
	require.NoError(t, err)
	defer wire.Close()
	for _, want := range []string{"fast", "standard"} {
		require.True(t, wire.Next())
		ev := wire.Current()
		exec := execution(outer)
		observePeerBillingTier(exec, ev.Data, true)
		observeServiceTier(exec, ev.Data, true)
		require.Equal(t, want, outer.PeerBillingTier)
		require.Equal(t, inner.Decision().Mode, outer.Decision().Mode)
	}
	require.False(t, wire.Next())
	require.NoError(t, wire.Err())
	require.Equal(t, "standard", outer.Decision().Mode)
}

func TestPeerBillingTierTailKeepsProviderEvidence(t *testing.T) {
	body := []byte(`{"choices":[],"usage":{"prompt_tokens":10},"service_tier":"default","axonhub_billing_tier":"fast"}`)
	for _, ack := range []bool{false, true} {
		e := &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}}
		exec := &ent.RequestExecution{Format: string(llm.APIFormatOpenAIChatCompletion), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: e}}
		e.ObserveValue("priority")
		stream := &OutboundPersistentStream{requestExec: exec, apiFormat: llm.APIFormatOpenAIChatCompletion, terminalState: streamTerminalCompleted, headers: &executionHeaderObserver{}}
		if ack {
			stream.headers.observe(t.Context(), http.Header{httpclient.BillingTierAckHeader: {"1"}})
		}
		require.NoError(t, stream.consumeTailEvent(&httpclient.StreamEvent{Data: body}))
		if ack {
			require.Equal(t, "fast", e.PeerBillingTier)
			require.Equal(t, "default", e.Upstream)
		} else {
			require.Empty(t, e.PeerBillingTier)
			require.Equal(t, "priority", e.Upstream)
		}
	}
}

func TestServiceTierCancelledResponseUsesUpstreamAndInvalidFallsBack(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket} {
		for _, raw := range []string{`"default"`, `42`, `{}`} {
			e := &ent.RequestExecution{Format: string(format), CostSnapshot: &objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "priority"}}}
			observeServiceTier(e, []byte(`{"type":"response.cancelled","response":{"service_tier":`+raw+`,"usage":{"input_tokens":10,"output_tokens":2}}}`), true)
			d, _ := e.CostSnapshot.ServiceTier.Resolve(&objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))}})
			if raw == `"default"` {
				require.Equal(t, "standard", d.Mode)
				require.True(t, d.Confirmed)
			} else {
				require.Equal(t, "fast", d.Mode)
				require.Equal(t, "invalid_upstream", d.Reason)
				require.False(t, d.Confirmed)
			}
		}
	}
}

func TestServiceTierRecoveryUsesFrozenFinalEvidenceAndRemainsIdempotent(t *testing.T) {
	t.Run("legacy-final", func(t *testing.T) { checkServiceTierRecovery(t, false, "") })
	t.Run("new-outbound", func(t *testing.T) { checkServiceTierRecovery(t, true, "") })
	t.Run("peer-fast", func(t *testing.T) { checkServiceTierRecovery(t, false, "fast") })
}

func checkServiceTierRecovery(t *testing.T, outbound bool, peerTier string) {
	t.Helper()
	ctx, db, ch, _, _, usage, key := setupBudgetRequest(t)
	req := db.Request.Create().SetRequestBody([]byte(`{}`)).SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).SetModelID("gpt-4").SetFormat(string(llm.APIFormatOpenAIResponse)).SetStatus(request.StatusCompleted).SaveX(ctx)
	price := &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}}}, ServiceTiers: &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))}}}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ManualMultiplier: decimal.NewFromInt(1), ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "priority"}}
	exec := db.RequestExecution.Create().SetRequestBody([]byte(`{}`)).SetProjectID(key.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("gpt-4").SetFormat(string(llm.APIFormatOpenAIResponse)).SetCostSnapshot(snapshot).SetStatus(requestexecution.StatusCompleted).SaveX(ctx)
	require.NoError(t, usage.BudgetService.Reserve(ctx, key.ProjectID, key.ID, req.ID, exec.ID, 10000000, nil))
	require.NoError(t, usage.BudgetService.MarkSent(ctx, exec.ID))
	if outbound {
		snapshot.ServiceTier.UseOutboundTier = true
		snapshot.ServiceTier.Outbound = "default"
	} else {
		snapshot.ServiceTier.Observe([]byte(`{"service_tier":"default"}`))
	}
	snapshot.ServiceTier.PeerBillingTier = peerTier
	require.NoError(t, usage.BudgetService.RecordUsage(ctx, exec.ID, &llm.Usage{PromptTokens: 10000, TotalTokens: 10000}, snapshot, nil))
	// Simulate a process boundary: discard in-memory objects and re-read durable evidence.
	snapshot.ServiceTier.Upstream = "priority"
	snapshot.ServiceTier.PeerBillingTier = ""
	price.ServiceTiers.Fast.Multiplier = lo.ToPtr(decimal.NewFromInt(99))
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.NotNil(t, charge.Usage)
	require.NotNil(t, charge.CostSnapshot)
	require.NotNil(t, charge.CostSnapshot.ModelPrice)
	if outbound {
		require.Equal(t, "outbound", charge.CostSnapshot.ServiceTier.Decision().Basis)
	} else {
		require.Equal(t, "default", charge.CostSnapshot.ServiceTier.Upstream)
	}
	require.NotNil(t, db.Request.GetX(ctx, req.ID))
	require.NotNil(t, db.RequestExecution.GetX(ctx, exec.ID))
	db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	require.NoError(t, usage.RecoverBudgets(ctx, nil))
	require.NoError(t, usage.RecoverBudgets(ctx, nil))
	charge = db.BudgetCharge.GetX(ctx, charge.ID)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	log := db.UsageLog.Query().OnlyX(ctx)
	if peerTier == "fast" {
		require.Equal(t, int64(4000000), *charge.Actual)
		require.Equal(t, 0.04, *log.TotalCost)
		require.Equal(t, "fast", log.CostItems[0].PricingEvidence.ServiceTier.Mode)
	} else {
		require.Equal(t, int64(2000000), *charge.Actual)
		require.Equal(t, 0.02, *log.TotalCost)
		require.Equal(t, "standard", log.CostItems[0].PricingEvidence.ServiceTier.Mode)
	}
	require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
	require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
}
