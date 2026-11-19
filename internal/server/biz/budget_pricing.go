package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

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
	currentPrice := snap.CurrentPrice()
	if currentPrice == nil {
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
		// Keep only the scalar controls; copying every field also copies large
		// inline images merely to read an output limit.
		var fields struct {
			N                   json.RawMessage `json:"n"`
			MaxTokens           json.RawMessage `json:"max_tokens"`
			MaxOutputTokens     json.RawMessage `json:"max_output_tokens"`
			MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
		}
		if json.Unmarshal(body, &fields) == nil {
			if len(fields.N) > 0 && req.Image != nil {
				if json.Unmarshal(fields.N, &imageCount) != nil || imageCount <= 0 {
					return 0, fmt.Errorf("invalid image output count")
				}
			}
			for _, value := range []json.RawMessage{fields.MaxTokens, fields.MaxOutputTokens, fields.MaxCompletionTokens} {
				if len(value) > 0 {
					var limit int64
					if json.Unmarshal(value, &limit) != nil || limit <= 0 {
						return 0, fmt.Errorf("invalid output limit")
					}
					output = limit
				}
			}
			textBytes := len(body)
			imageTokens := int64(0)
			if images, content := inspectRequestBody(ctx, body); images != nil {
				textBytes -= content.ImageBytes
				// Match the normalized request heuristic. Encoding size is not
				// image token usage; actual provider usage still settles in full.
				imageTokens = int64(images.Count) * 128
			}
			promptTokens = max(promptTokens, int64((textBytes+2)/3)+imageTokens)
		}
	}
	if output < 0 {
		return 0, fmt.Errorf("invalid output limit")
	}
	price := *currentPrice
	estimatedUsage := &llm.Usage{PromptTokens: promptTokens, CompletionTokens: output, TotalTokens: promptTokens + output}
	effective, overrides, contextPrice := selectedModelPricing(estimatedUsage, price, snap.PricedAt)
	decision := snap.ServiceTier.RequestDecision()
	resolve := func(item objects.ModelPriceItem, variant objects.PromptWriteCacheVariantCode) (objects.Pricing, bool) {
		variant = snap.CacheWriteBilling.PricingVariant(variant)
		base := item.Pricing
		if variant != "" {
			base = item.FindPromptWriteCacheVariantPricing(variant)
		}
		rule := objects.ResolveServiceTierItem(decision.Mode, contextPrice.InputTokens, price.ServiceTiers, overrides, item.ItemCode, variant)
		return rule.Apply(base)
	}
	if len(effective) == 0 {
		return 0, fmt.Errorf("the selected model has no price items")
	}
	needsOutput := false
	for _, item := range effective {
		if item.ItemCode == objects.PriceItemCodeCompletion && item.Pricing.Mode != objects.PricingModeFlatFee && item.Pricing.Mode != objects.PricingModeImageFixed {
			resolved, priced := resolve(item, "")
			if !priced {
				return 0, fmt.Errorf("the requested output service tier has no configured price")
			}
			needsOutput = needsOutput || budgetPricingPositive(resolved)
		}
	}
	if needsOutput && output == 0 {
		output = snap.ModelOutputLimit
	}
	if needsOutput && output == 0 {
		card, err := authz.RunAsHost(ctx, s.db, "budget-output-limit", func(ctx context.Context) (*ent.Model, error) {
			if execution.Selection != nil && execution.Selection.UseActualModelPrice {
				matches, err := matchModelStandardPrices(ctx, s.db, []string{execution.ModelID}, model.StatusEnabled)
				if err != nil || len(matches) == 0 {
					return nil, err
				}
				return matches[0].Model, nil
			}
			return s.db.Model.Query().Where(model.ModelIDEQ(execution.ModelID)).Only(ctx)
		})
		if err != nil && !ent.IsNotFound(err) {
			return 0, err
		}
		if card == nil || card.ModelCard == nil || card.ModelCard.Limit.Output <= 0 {
			return 0, fmt.Errorf("an output limit is required for budget estimation")
		}
		output = int64(card.ModelCard.Limit.Output)
	}
	estimatedUsage.CompletionTokens = output
	estimatedUsage.TotalTokens = promptTokens + output
	total := decimal.Zero
	splitInput := slices.ContainsFunc(effective, func(item objects.ModelPriceItem) bool {
		return item.ItemCode.IsModalityToken() || item.ItemCode == objects.PriceItemCodePromptCachedToken || item.ItemCode == objects.PriceItemCodeWriteCachedTokens
	})
	inputEstimate := decimal.Zero
	for _, item := range effective {
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
			total = total.Add(highest.Mul(decimal.NewFromInt(n)).Mul(snap.ManualMultiplier))
			continue
		}
		quantity := promptTokens
		if item.ItemCode == objects.PriceItemCodeCompletion {
			quantity = output
		}
		// Reserve input/cache-write alternatives conservatively; actual settlement
		// uses the provider's mutually exclusive token counts.
		resolved, priced := resolve(item, "")
		if !priced && quantity > 0 {
			return 0, fmt.Errorf("the requested service tier item has no configured price")
		}
		estimateItem := func(pricing objects.Pricing) decimal.Decimal {
			multiplier := snap.Applied.Value
			if pricing.Mode == objects.PricingModeFlatFee {
				multiplier = snap.ManualMultiplier
			}
			if splitInput && item.ItemCode != objects.PriceItemCodeCompletion &&
				(pricing.Mode == objects.PricingModeTiered || pricing.Mode == objects.PricingModeVolume) && pricing.UsageTiered != nil {
				// Until input/cache quantities are known, each subset can hit a different
				// (including cheaper or dearer) tier. Reserve once at the largest
				// reachable unit rate instead of applying the total to each tier.
				maximum := decimal.Zero
				var previous int64
				for _, tier := range pricing.UsageTiered.Tiers {
					if quantity <= previous {
						break
					}
					maximum = decimal.Max(maximum, tier.PricePerUnit)
					if tier.UpTo == nil {
						break
					}
					previous = *tier.UpTo
				}
				return maximum.Mul(unitsInMillionTokens(quantity)).Mul(multiplier)
			}
			_, subtotal := computeItemSubtotal(quantity, pricing)
			return subtotal.Mul(multiplier)
		}
		subtotal := estimateItem(resolved)
		inputAlternative := item.ItemCode != objects.PriceItemCodeCompletion && resolved.Mode != objects.PricingModeFlatFee
		if item.ItemCode == objects.PriceItemCodeWriteCachedTokens {
			for _, variant := range []objects.PromptWriteCacheVariantCode{objects.PromptWriteCacheVariantCode5Min, objects.PromptWriteCacheVariantCode1Hour} {
				variantPrice, priced := resolve(item, variant)
				if !priced && quantity > 0 {
					return 0, fmt.Errorf("the requested cache variant has no configured price")
				}
				// A fixed variant may be charged alongside uncached input.
				inputAlternative = inputAlternative && variantPrice.Mode != objects.PricingModeFlatFee
				value := estimateItem(variantPrice)
				if value.GreaterThan(subtotal) {
					subtotal = value
				}
			}
		}
		if inputAlternative {
			// Only the total input estimate is known before dispatch. Its modality
			// and cache alternatives are exclusive, so reserve the largest cost.
			inputEstimate = decimal.Max(inputEstimate, subtotal)
		} else {
			total = total.Add(subtotal)
		}
	}
	return budget.ReserveAmount(total.Add(inputEstimate))
}

func (s *UsageLogService) actualBudgetCost(ctx context.Context, usage *llm.Usage, snapshot *objects.RequestCostSnapshot) (*budget.Amount, error) {
	if snapshot == nil {
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
	if snapshot == nil {
		return nil, nil
	}
	price := snapshot.CurrentPrice()
	if price == nil {
		if snapshot.PriceReferenceID == "" {
			return nil, nil
		}
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
