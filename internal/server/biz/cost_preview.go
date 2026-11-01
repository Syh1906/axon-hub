package biz

import (
	"context"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/imagebilling"
	"github.com/looplj/axonhub/llm"
)

// CostRecalculationPreview never writes accounting data or fetches image URLs.
// It reuses the authorized request's immutable price and retained output evidence.
type CostRecalculationPreview struct {
	RecordedCost *float64
	ProposedCost *float64
	PricedAt     *time.Time
	Reason       string
	Images       []objects.ImageOutput
}

func PreviewRequestCost(ctx context.Context, client *ent.Client, requestID int) (*CostRecalculationPreview, error) {
	result := &CostRecalculationPreview{Images: []objects.ImageOutput{}}
	req, err := client.Request.Query().Where(request.IDEQ(requestID)).Select(request.FieldID, request.FieldProjectID, request.FieldChannelID, request.FieldStatus).Only(ctx)
	if err != nil {
		return nil, err
	}
	if req.Status != request.StatusCompleted {
		result.Reason = "request_not_completed"
		return result, nil
	}
	execution, err := client.RequestExecution.Query().Where(requestexecution.RequestIDEQ(req.ID), requestexecution.ProjectIDEQ(req.ProjectID), requestexecution.ChannelIDEQ(req.ChannelID)).
		Select(requestexecution.FieldID, requestexecution.FieldStatus, requestexecution.FieldCostSnapshot, requestexecution.FieldRequestBody, requestexecution.FieldResponseBody, requestexecution.FieldModelID).
		Order(ent.Desc(requestexecution.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		result.Reason = "execution_missing"
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if execution.Status != requestexecution.StatusCompleted {
		result.Reason = "request_not_completed"
		return result, nil
	}
	logs, err := client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID), usagelog.ProjectIDEQ(req.ProjectID), usagelog.ChannelIDEQ(req.ChannelID)).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(logs) > 1 {
		result.Reason = "ambiguous_usage"
		return result, nil
	}
	snapshot := execution.CostSnapshot
	var usage *llm.Usage
	if len(logs) == 1 {
		row := logs[0]
		result.RecordedCost = row.TotalCost
		if row.CostSnapshot != nil {
			snapshot = row.CostSnapshot
		}
		usage = &llm.Usage{
			PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, TotalTokens: row.TotalTokens,
			PromptTokensDetails: &llm.PromptTokensDetails{
				CachedTokens: row.PromptCachedTokens, WriteCachedTokens: row.PromptWriteCachedTokens,
				WriteCached5MinTokens: row.PromptWriteCachedTokens5m, WriteCached1HourTokens: row.PromptWriteCachedTokens1h,
			},
		}
		for _, item := range row.CostItems {
			// Zero token columns also represent absent provider usage. Preserve
			// the original unknown item, including legacy rows without a reason.
			if item.Unpriced && item.Image == nil && item.Quantity == 0 && (item.UnpricedReason == "usage_missing" || item.UnpricedReason == "") {
				usage = nil
				break
			}
		}
	}
	if snapshot == nil {
		result.Reason = "historical_price_missing"
		return result, nil
	}
	copy := *snapshot
	price := copy.CurrentPrice()
	if price == nil && copy.PriceReferenceID != "" {
		// Request and usage authorization above bounds this read to the price
		// already referenced by this request, as with existing cost breakdowns.
		version, err := authz.RunWithSystemBypass(ctx, "request-cost-preview-price", func(ctx context.Context) (*ent.ChannelModelPriceVersion, error) {
			return client.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ChannelIDEQ(req.ChannelID), channelmodelpriceversion.ReferenceID(copy.PriceReferenceID)).Select(channelmodelpriceversion.FieldPrice).Only(ctx)
		})
		if err != nil && !ent.IsNotFound(err) {
			return nil, err
		}
		if version != nil {
			price = &version.Price
		}
	}
	if price == nil {
		result.Reason = "historical_price_missing"
		return result, nil
	}
	copy.Price = price
	if !NeedsUsageLogWithoutTokens(&copy) {
		result.Reason = "non_fixed_pricing"
		return result, nil
	}
	result.PricedAt = &copy.PricedAt
	images := imagebilling.Extract(ctx, execution.RequestBody, execution.ResponseBody, execution.ModelID, nil)
	if len(copy.Images) > 0 {
		retained := append([]objects.ImageOutput(nil), copy.Images...)
		for i := range retained {
			width, height := retained[i].BillingDimensions()
			if i < len(images) && retained[i].Tier == "" && (width == 0 || height == 0) {
				inspectionError := retained[i].InspectionError
				retained[i] = images[i]
				if retained[i].InspectionError == "" {
					retained[i].InspectionError = inspectionError
				}
			}
		}
		images = retained
	}
	copy.Images = images
	if images != nil {
		result.Images = images
	}
	items, total := computeCostFromSnapshot(usage, *price, &copy)
	for _, item := range items {
		if item.Unpriced {
			result.Reason = item.UnpricedReason
			return result, nil
		}
	}
	if len(items) == 0 {
		result.Reason = "historical_price_missing"
		return result, nil
	}
	result.ProposedCost = lo.ToPtr(total.InexactFloat64())
	result.Reason = "preview_only"
	return result, nil
}
