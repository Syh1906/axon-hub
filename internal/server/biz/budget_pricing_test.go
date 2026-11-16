package biz

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
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

func screenshotBudgetSnapshot() *objects.RequestCostSnapshot {
	price := serviceTierTestPrice()
	price.VolumeTiers = nil
	price.Items = append(price.Items, objects.ModelPriceItem{ItemCode: objects.PriceItemCodeWriteCachedTokens,
		Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString("2.5"))}})
	return &objects.RequestCostSnapshot{ModelPrice: &price, ModelOutputLimit: 128000,
		Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.18")}, ManualMultiplier: decimal.NewFromInt(1)}
}

func screenshotBudgetBody() []byte {
	image := `{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", 3100000) + `"}`
	return []byte(`{"input":[{"type":"function_call_output","output":[` + strings.TrimSuffix(strings.Repeat(image+",", 10), ",") + `]}]}`)
}

func TestBudgetQuoteIgnoresImageEncodingSize(t *testing.T) {
	for name, template := range map[string]string{
		"responses tool output": `{"input":[{"type":"function_call_output","output":[{"type":"input_image","image_url":"data:image/png;base64,%s"}]}]}`,
		"chat completion":       `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		"anthropic tool result": `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`,
		"gemini":                `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"%s"}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			service := &BudgetService{}
			execution := &ent.RequestExecution{CostSnapshot: screenshotBudgetSnapshot()}
			execution.CostSnapshot.ModelPrice.VolumeTiers = serviceTierTestPrice().VolumeTiers
			quote := func(data string) string {
				amount, err := service.Estimate(t.Context(), execution, &llm.Request{}, &httpclient.Request{Body: fmt.Appendf(nil, template, data)}, 0)
				require.NoError(t, err)
				return amount.String()
			}
			require.Equal(t, quote("AAAA"), quote(strings.Repeat("A", 1000000)), "image transfer encoding must not count as text tokens or select long-context prices")
		})
	}
}

func TestBudgetQuoteScreenshotHistoryFitsRemainingDailyBudget(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "group", f.group.ID, "35")
	spent := budgetAmount(t, "26.38029424")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, spent, nil))
	_, _, err := f.service.Settle(f.ctx, 1, &spent, nil)
	require.NoError(t, err)

	quote, err := f.service.Estimate(f.ctx, &ent.RequestExecution{CostSnapshot: screenshotBudgetSnapshot()}, &llm.Request{}, &httpclient.Request{Body: screenshotBudgetBody()}, 2000)
	require.NoError(t, err)
	require.Less(t, quote, budgetAmount(t, "1"), "ten encoded screenshots must not reserve millions of text tokens")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 2, 2, quote, nil))
	actual := budgetAmount(t, "0.005247")
	_, _, err = f.service.Settle(f.ctx, 2, &actual, nil)
	require.NoError(t, err)
	account := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindGroup)).OnlyX(f.ctx)
	require.Zero(t, account.Reserved)
	view, err := f.service.View(f.ctx, f.key.ProjectID, "group", f.group.ID, "Asia/Shanghai")
	require.NoError(t, err)
	for _, window := range view.Windows {
		require.Equal(t, "26.38554124", window.Settled)
	}
}

func TestBudgetQuoteKeepsFinalTextAndOutputLimits(t *testing.T) {
	service := &BudgetService{}
	snapshot := screenshotBudgetSnapshot()
	snapshot.ModelPrice.Items = snapshot.ModelPrice.Items[:2]
	snapshot.ModelPrice.VolumeTiers = nil
	execution := &ent.RequestExecution{CostSnapshot: snapshot}
	request := &llm.Request{MaxTokens: lo.ToPtr(int64(1))}
	// The final raw body wins over the earlier normalized text and output cap.
	body := []byte(`{"input":"` + strings.Repeat("x", 30000) + `","max_output_tokens":1000}`)
	quote, err := service.Estimate(t.Context(), execution, request, &httpclient.Request{Body: []byte(`{"max_output_tokens":1}`), JSONBody: body}, 0)
	require.NoError(t, err)
	require.Greater(t, quote, budgetAmount(t, "0.0054"))
	_, err = service.Estimate(t.Context(), execution, request, &httpclient.Request{Body: []byte(`{"max_output_tokens":0}`)}, 0)
	require.ErrorContains(t, err, "invalid output limit")
	// JSON-looking text remains billable text, not a protocol image part.
	body = []byte(`{"input":"data:image/png;base64,` + strings.Repeat("A", 30000) + `"}`)
	quote, err = service.Estimate(t.Context(), execution, request, &httpclient.Request{Body: body}, 0)
	require.NoError(t, err)
	require.Greater(t, quote, budgetAmount(t, "0.0036"))
}

func TestBudgetQuoteInputCacheAlternativesAreExclusive(t *testing.T) {
	snapshot := screenshotBudgetSnapshot()
	quote, err := (&BudgetService{}).Estimate(t.Context(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{MaxTokens: lo.ToPtr(int64(1000))}, nil, 100000)
	require.NoError(t, err)
	// 100k input at max(2, 0.1, 2.5), plus 1k output at 10; apply 0.18 once.
	require.Equal(t, "0.04680000", quote.String())
	// A separate fixed charge is additive, not another token-rate alternative.
	snapshot.ModelPrice.Items[2].Pricing = objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(1))}
	quote, err = (&BudgetService{}).Estimate(t.Context(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{MaxTokens: lo.ToPtr(int64(1000))}, nil, 100000)
	require.NoError(t, err)
	require.Equal(t, "1.04680000", quote.String())
	// Fixed cache-write variants remain additive to ordinary token input.
	snapshot.ModelPrice.Items = append(snapshot.ModelPrice.Items[:2], snapshot.ModelPrice.Items[3])
	snapshot.ModelPrice.Items[2].PromptWriteCacheVariants = []objects.PromptWriteCacheVariant{{VariantCode: objects.PromptWriteCacheVariantCode1Hour,
		Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(1))}}}
	quote, err = (&BudgetService{}).Estimate(t.Context(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{MaxTokens: lo.ToPtr(int64(1000))}, nil, 100000)
	require.NoError(t, err)
	require.Equal(t, "1.03780000", quote.String())
}

func TestBudgetQuoteSingleInputKeepsTieredPrice(t *testing.T) {
	for _, mode := range []objects.PricingMode{objects.PricingModeTiered, objects.PricingModeVolume} {
		t.Run(string(mode), func(t *testing.T) {
			price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{Mode: mode, UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
					{UpTo: lo.ToPtr(int64(500)), PricePerUnit: decimal.NewFromInt(10)},
					{PricePerUnit: decimal.Zero},
				}}}}}}
			snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
			quote, err := (&BudgetService{}).Estimate(t.Context(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{}, nil, 1000)
			require.NoError(t, err)
			want := "0.00500000"
			if mode == objects.PricingModeVolume {
				want = "0.00000000"
			}
			require.Equal(t, want, quote.String(), "without an input split, preserve the configured tier calculation")
			// Cached and uncached subsets may both hit the first, dearer tier.
			cache := price.Items[0]
			cache.ItemCode = objects.PriceItemCodePromptCachedToken
			price.Items = append(price.Items, cache)
			quote, err = (&BudgetService{}).Estimate(t.Context(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{}, nil, 1000)
			require.NoError(t, err)
			require.Equal(t, "0.01000000", quote.String(), "split input reserves the maximum reachable rate once")
		})
	}
}

func BenchmarkBudgetQuoteScreenshotHistory(b *testing.B) {
	execution := &ent.RequestExecution{CostSnapshot: screenshotBudgetSnapshot()}
	raw := &httpclient.Request{Body: screenshotBudgetBody()}
	service := &BudgetService{}
	for _, inspect := range []bool{false, true} {
		b.Run(fmt.Sprintf("inspect=%t", inspect), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw.Body)))
			for b.Loop() {
				// Execution persistence already inspects this same final body.
				if inspect {
					requestpayload.Inspect(raw.Body)
				}
				if _, err := service.Estimate(context.Background(), execution, &llm.Request{}, raw, 2000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
