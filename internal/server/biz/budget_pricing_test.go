package biz

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBudgetQuoteUsesFinalImageCount(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "5")
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeImageFixed, Image: &objects.ImagePricing{Automatic: true, Rules: []objects.ImagePriceRule{{Name: "1K", Fee: decimal.NewFromInt(1)}}}}}}}
	snap := &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "fixture", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1)}
	req := &llm.Request{Image: &llm.ImageRequest{}}
	exec := &ent.RequestExecution{CostSnapshot: snap}
	quote, err := f.service.Estimate(f.ctx, exec, req, &httpclient.Request{Body: []byte(`{"n":10}`)}, 1)
	require.NoError(t, err)
	require.Equal(t, "10.00000000", quote.String())
	err = f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, quote, nil)
	require.True(t, IsBudgetDenied(err))
	require.Zero(t, f.db.BudgetCharge.Query().CountX(f.ctx))
}
func TestBudgetZeroOutputPriceDoesNotRequireOutputLimit(t *testing.T) {
	f := newBudgetFixture(t)
	for _, input := range []int64{0, 1} {
		price := objects.ModelPrice{Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: new(decimal.NewFromInt(input))}},
			{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: new(decimal.Zero)}},
		}}
		snap := &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "fixture", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
		quote, err := f.service.Estimate(f.ctx, &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{}, nil, 1000000)
		require.NoError(t, err)
		require.True(t, decimal.NewFromInt(input).Equal(quote.Decimal()))
	}
}
func TestBudgetExpiryAppliesToAuthenticationWithoutBlockingExhaustedReads(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "1")
	service := &AuthService{APIKeyService: f.admin}
	account := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx)
	state := account.State
	future := time.Now().Add(time.Hour)
	state.Policy.ExpiresAt = &future
	account.Update().SetState(state).SaveX(f.ctx)
	_, err := service.AuthenticateAPIKey(f.ctx, f.key.Key)
	require.NoError(t, err)
	past := time.Now().Add(-time.Second)
	state.Policy.ExpiresAt = &past
	account.Update().SetState(state).SaveX(f.ctx)
	_, err = service.AuthenticateAPIKey(f.ctx, f.key.Key)
	require.ErrorIs(t, err, ErrInvalidAPIKey)
}
