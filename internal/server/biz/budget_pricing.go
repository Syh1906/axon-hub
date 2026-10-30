package biz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/shopspring/decimal"
)

// Estimate uses the selected attempt's immutable price and multiplier snapshot.
// It is an estimate, not a provider-token upper bound. Actual costs are never capped.
func (s *BudgetService) Estimate(ctx context.Context, execution *ent.RequestExecution, req *llm.Request, raw *httpclient.Request, promptTokens int64) (budget.Amount, error) {
	snap := execution.CostSnapshot
	if snap == nil || snap.Price == nil || snap.PriceReferenceID == "" {
		return 0, fmt.Errorf("the selected model has no price")
	}
	if req == nil {
		return 0, fmt.Errorf("missing request")
	}
	output := int64(0)
	imageCount := int64(1)
	if req.Image != nil && req.Image.N != nil {
		imageCount = *req.Image.N
	}
	if req.MaxCompletionTokens != nil {
		output = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		output = *req.MaxTokens
	}
	// Overrides and pass-through may change the output cap after normalization.
	if raw != nil {
		body := raw.Body
		if len(raw.JSONBody) > 0 {
			body = raw.JSONBody
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil {
			if value, ok := fields["n"]; ok && req.Image != nil {
				if json.Unmarshal(value, &imageCount) != nil || imageCount <= 0 {
					return 0, fmt.Errorf("invalid image output count")
				}
			}
			for _, name := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
				if value, ok := fields[name]; ok {
					var limit int64
					if json.Unmarshal(value, &limit) != nil || limit <= 0 {
						return 0, fmt.Errorf("invalid output limit")
					}
					output = limit
				}
			}
			promptTokens = max(promptTokens, int64((len(body)+2)/3))
		}
	}
	if output < 0 {
		return 0, fmt.Errorf("invalid output limit")
	}
	price := *snap.Price
	estimatedUsage := &llm.Usage{PromptTokens: promptTokens, CompletionTokens: output, TotalTokens: promptTokens + output}
	effective := effectiveModelPriceItems(estimatedUsage, price, snap.PricedAt)
	if len(effective) == 0 {
		return 0, fmt.Errorf("the selected model has no price items")
	}
	needsOutput := false
	for _, item := range effective {
		if item.ItemCode == objects.PriceItemCodeCompletion && item.Pricing.Mode != objects.PricingModeFlatFee && item.Pricing.Mode != objects.PricingModeImageFixed {
			needsOutput = needsOutput || budgetPricingPositive(item.Pricing)
		}
	}
	if needsOutput && output == 0 {
		var card *ent.Model
		_, err := authz.RunWithSystemBypass(ctx, "budget-output-limit", func(ctx context.Context) (bool, error) {
			var err error
			card, err = s.db.Model.Query().Where(model.ModelIDEQ(execution.ModelID)).Only(ctx)
			return true, err
		})
		if err != nil && !ent.IsNotFound(err) {
			return 0, err
		}
		if card == nil || card.ModelCard == nil || card.ModelCard.Limit.Output <= 0 {
			return 0, fmt.Errorf("an output limit is required for budget estimation")
		}
		output = int64(card.ModelCard.Limit.Output)
		estimatedUsage.CompletionTokens = output
		estimatedUsage.TotalTokens = promptTokens + output
		effective = effectiveModelPriceItems(estimatedUsage, price, snap.PricedAt)
	}
	total := decimal.Zero
	for _, item := range effective {
		multiplier := snap.Applied.Value
		if item.Pricing.Mode == objects.PricingModeFlatFee || item.Pricing.Mode == objects.PricingModeImageFixed {
			multiplier = snap.ManualMultiplier
		}
		if req.Video != nil && item.Pricing.Mode != objects.PricingModeFlatFee {
			return 0, fmt.Errorf("video pricing cannot be estimated")
		}
		if item.Pricing.Mode == objects.PricingModeImageFixed {
			if req.Image == nil || item.Pricing.Image == nil {
				return 0, fmt.Errorf("image output count is unknown")
			}
			n := imageCount
			if n <= 0 {
				return 0, fmt.Errorf("invalid image output count")
			}
			highest := decimal.Zero
			for _, rule := range item.Pricing.Image.Rules {
				if rule.Fee.GreaterThan(highest) {
					highest = rule.Fee
				}
			}
			total = total.Add(highest.Mul(decimal.NewFromInt(n)).Mul(multiplier))
			continue
		}
		quantity := promptTokens
		if item.ItemCode == objects.PriceItemCodeCompletion {
			quantity = output
		}
		// Reserve input/cache-write alternatives conservatively; actual settlement
		// uses the provider's mutually exclusive token counts.
		_, subtotal := computeItemSubtotal(quantity, item.Pricing)
		if item.ItemCode == objects.PriceItemCodeWriteCachedTokens {
			for _, variant := range []objects.PromptWriteCacheVariantCode{objects.PromptWriteCacheVariantCode5Min, objects.PromptWriteCacheVariantCode1Hour} {
				_, value := computeItemSubtotal(quantity, item.FindPromptWriteCacheVariantPricing(variant))
				if value.GreaterThan(subtotal) {
					subtotal = value
				}
			}
		}
		total = total.Add(subtotal.Mul(multiplier))
	}
	return budget.ReserveAmount(total)
}

func (s *UsageLogService) actualBudgetCost(ctx context.Context, usage *llm.Usage, snapshot *objects.RequestCostSnapshot) (*budget.Amount, error) {
	if snapshot == nil || snapshot.PriceReferenceID == "" {
		return nil, nil
	}
	price, err := s.budgetSnapshotPrice(ctx, snapshot)
	if err != nil || price == nil {
		return nil, err
	}
	if len(effectiveModelPriceItems(usage, *price, snapshot.PricedAt)) == 0 {
		return nil, nil
	}
	items, total := computeCostFromSnapshot(usage, *price, snapshot)
	for _, item := range items {
		if item.Unpriced {
			return nil, nil
		}
	}
	value, err := budget.ParseAmount(total.Round(8).StringFixed(8))
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func budgetPricingPositive(pricing objects.Pricing) bool {
	if pricing.UsagePerUnit != nil {
		return pricing.UsagePerUnit.IsPositive()
	}
	if pricing.UsageTiered != nil {
		for _, tier := range pricing.UsageTiered.Tiers {
			if tier.PricePerUnit.IsPositive() {
				return true
			}
		}
	}
	return false
}

func (s *UsageLogService) budgetSnapshotPrice(ctx context.Context, snapshot *objects.RequestCostSnapshot) (*objects.ModelPrice, error) {
	if snapshot == nil || snapshot.PriceReferenceID == "" {
		return nil, nil
	}
	price := snapshot.Price
	if price == nil {
		version, err := authz.RunWithSystemBypass(ctx, "budget-settlement-price", func(ctx context.Context) (*ent.ChannelModelPriceVersion, error) {
			return s.db.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ReferenceID(snapshot.PriceReferenceID)).Only(ctx)
		})
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		price = &version.Price
	}

	return price, nil
}
