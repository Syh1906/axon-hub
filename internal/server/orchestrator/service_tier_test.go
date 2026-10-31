package orchestrator

import (
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
	ctx, db, ch, _, _, usage, key := setupBudgetRequest(t)
	req := db.Request.Create().SetRequestBody([]byte(`{}`)).SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).SetModelID("gpt-4").SetFormat(string(llm.APIFormatOpenAIResponse)).SetStatus(request.StatusCompleted).SaveX(ctx)
	price := &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}}}, ServiceTiers: &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))}}}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ManualMultiplier: decimal.NewFromInt(1), ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "priority"}}
	exec := db.RequestExecution.Create().SetRequestBody([]byte(`{}`)).SetProjectID(key.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("gpt-4").SetFormat(string(llm.APIFormatOpenAIResponse)).SetCostSnapshot(snapshot).SetStatus(requestexecution.StatusCompleted).SaveX(ctx)
	require.NoError(t, usage.BudgetService.Reserve(ctx, key.ProjectID, key.ID, req.ID, exec.ID, 10000000, nil))
	require.NoError(t, usage.BudgetService.MarkSent(ctx, exec.ID))
	snapshot.ServiceTier.Observe([]byte(`{"service_tier":"default"}`))
	require.NoError(t, usage.BudgetService.RecordUsage(ctx, exec.ID, &llm.Usage{PromptTokens: 10000, TotalTokens: 10000}, snapshot, nil))
	// Simulate a process boundary: discard in-memory objects and re-read durable evidence.
	snapshot.ServiceTier.Upstream = "priority"
	price.ServiceTiers.Fast.Multiplier = lo.ToPtr(decimal.NewFromInt(99))
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.NotNil(t, charge.Usage)
	require.NotNil(t, charge.CostSnapshot)
	require.NotNil(t, charge.CostSnapshot.ModelPrice)
	require.Equal(t, "default", charge.CostSnapshot.ServiceTier.Upstream)
	require.NotNil(t, db.Request.GetX(ctx, req.ID))
	require.NotNil(t, db.RequestExecution.GetX(ctx, exec.ID))
	db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	require.NoError(t, usage.RecoverBudgets(ctx, nil))
	require.NoError(t, usage.RecoverBudgets(ctx, nil))
	charge = db.BudgetCharge.GetX(ctx, charge.ID)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.Equal(t, int64(2000000), *charge.Actual)
	log := db.UsageLog.Query().OnlyX(ctx)
	require.Equal(t, 0.02, *log.TotalCost)
	require.Equal(t, "standard", log.CostItems[0].PricingEvidence.ServiceTier.Mode)
	require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
	require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
}
