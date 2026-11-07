package biz

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/metrics"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/llm"
)

// UsageLogService handles usage log operations.
type UsageLogService struct {
	*AbstractService

	SystemService  *SystemService
	ChannelService *ChannelService
	BudgetService  *BudgetService

	// OnUsageLogCreated is called after a usage log is successfully created.
	// Used to invalidate caches that depend on usage log data.
	OnUsageLogCreated func()
}

func (s *UsageLogService) computeUsageCost(ctx context.Context, channelID int, modelID string, usage *llm.Usage, snapshots ...*objects.RequestCostSnapshot) ([]objects.CostItem, *float64, string) {
	if len(snapshots) > 0 && snapshots[0] != nil {
		return s.computeSnapshotUsageCost(ctx, usage, snapshots[0])
	}
	if usage == nil {
		return nil, nil, ""
	}

	ch := s.ChannelService.GetEnabledChannel(channelID)
	if ch == nil {
		log.Warn(ctx, "channel not enabled for cost calculation",
			log.Int("channel_id", channelID),
			log.String("model_id", modelID),
		)

		return nil, nil, ""
	}
	if ch.Settings != nil && ch.Settings.CacheWriteBilling != nil {
		snapshot := captureRequestCostSnapshot(ctx, s.entFromContext(ctx), ch, modelID, nil)
		return s.computeSnapshotUsageCost(ctx, usage, snapshot)
	}

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "checking cached model price",
			log.Int("channel_id", channelID),
			log.String("model_id", modelID),
			log.Int("cached_price_count", len(ch.cachedModelPrices)),
		)
	}

	if modelPrice, ok := ch.cachedModelPrices[modelID]; ok {
		items, total := ComputeUsageCost(usage, modelPrice.Price, time.Now())
		if len(items) == 0 {
			return nil, nil, modelPrice.ReferenceID
		}
		for _, item := range items {
			if item.Unpriced {
				return items, nil, modelPrice.ReferenceID
			}
		}

		totalCost := total.InexactFloat64()
		if log.DebugEnabled(ctx) {
			log.Debug(ctx, "computed usage cost from cache",
				log.Int("channel_id", channelID),
				log.String("model_id", modelID),
				log.Float64("total_cost", totalCost),
				log.Int64("total_tokens", usage.TotalTokens),
				log.String("price_reference_id", modelPrice.ReferenceID),
			)
		}

		return items, lo.ToPtr(totalCost), modelPrice.ReferenceID
	}


	return nil, nil, ""
}

// InjectUsageCost writes AxonHub-calculated cost onto usage when a matching
// channel model price is cached, falling back to the model library price when
// the channel has no explicit price. usage is left unchanged when it is nil.
// When no matching price is available, usage.Cost is set to nil.
func (s *UsageLogService) InjectUsageCost(ctx context.Context, channelID int, modelID string, usage *llm.Usage, snapshots ...*objects.RequestCostSnapshot) {
	if usage == nil {
		return
	}

	_, totalCost, _ := s.computeUsageCost(ctx, channelID, modelID, usage, snapshots...)
	usage.Cost = totalCost
}

// NewUsageLogService creates a new UsageLogService.
func NewUsageLogService(ent *ent.Client, systemService *SystemService, channelService *ChannelService) *UsageLogService {
	return &UsageLogService{
		AbstractService: &AbstractService{
			db: ent,
		},
		SystemService:  systemService,
		ChannelService: channelService,
		BudgetService:  NewBudgetService(ent),
	}
}

// CreateUsageLogParams represents the parameters for creating a usage log.
type CreateUsageLogParams struct {
	SettledCost   *budget.Amount
	CostSnapshot  *objects.RequestCostSnapshot
	RequestID     int
	ProjectID     int
	ChannelID     int
	ActualModelID string // The channel actual model ID, not the request model ID.
	Usage         *llm.Usage
	Source        usagelog.Source
	Format        string
	APIKeyID      *int
}

// CreateUsageLog creates a new usage log record from LLM response usage data.
func (s *UsageLogService) CreateUsageLog(ctx context.Context, params CreateUsageLogParams) (*ent.UsageLog, error) {
	return s.saveUsageLog(ctx, params, nil)
}

// Refresh an unpriced record in the same transaction as its budget settlement.
// Missing late fields do not erase evidence from the earlier response.
func (s *UsageLogService) saveUsageLog(ctx context.Context, params CreateUsageLogParams, existing *ent.UsageLog) (*ent.UsageLog, error) {
	if params.Usage == nil && params.SettledCost == nil && !NeedsUsageLogWithoutTokens(params.CostSnapshot) {
		return existing, nil
	}
	// Keep missing provider usage distinct during billing; zero token columns are
	// only the storage representation for a non-token fee log.
	providerUsage := params.Usage
	if params.Usage == nil {
		params.Usage = &llm.Usage{}
	}

	client := s.entFromContext(ctx)

	create := client.UsageLog.Create()
	mut, save := create.Mutation(), create.Save
	if existing != nil {
		update := client.UsageLog.UpdateOneID(existing.ID)
		mut, save = update.Mutation(), update.Save
	} else {
		mut.SetRequestID(params.RequestID)
		mut.SetProjectID(params.ProjectID)
		mut.SetModelID(params.ActualModelID)
		mut.SetChannelID(params.ChannelID)
		mut.SetSource(params.Source)
		mut.SetFormat(params.Format)
	}
	if providerUsage != nil || existing == nil {
		mut.SetPromptTokens(params.Usage.PromptTokens)
		mut.SetCompletionTokens(params.Usage.CompletionTokens)
		mut.SetTotalTokens(params.Usage.TotalTokens)
	}

	if params.APIKeyID != nil {
		mut.SetAPIKeyID(*params.APIKeyID)
	} else if ctxAPIKey, ok := contexts.GetAPIKey(ctx); ok && ctxAPIKey != nil {
		mut.SetAPIKeyID(ctxAPIKey.ID)
	}

	// Set prompt tokens details if available
	if params.Usage.PromptTokensDetails != nil {
		details := params.Usage.PromptTokensDetails
		if details.TextTokens != nil {
			mut.SetPromptTextTokens(*details.TextTokens)
		}
		if details.ImageTokens != nil {
			mut.SetPromptImageTokens(*details.ImageTokens)
		}
		if cached := details.CachedTokensDetails; cached != nil {
			if cached.TextTokens != nil {
				mut.SetPromptTextCachedTokens(*cached.TextTokens)
			}
			if cached.ImageTokens != nil {
				mut.SetPromptImageCachedTokens(*cached.ImageTokens)
			}
		}
		mut.SetPromptAudioTokens(params.Usage.PromptTokensDetails.AudioTokens)
		mut.SetPromptCachedTokens(params.Usage.PromptTokensDetails.CachedTokens)
		mut.SetPromptWriteCachedTokens(params.Usage.PromptTokensDetails.WriteCachedTokens)
		mut.SetPromptWriteCachedTokens5m(params.Usage.PromptTokensDetails.WriteCached5MinTokens)
		mut.SetPromptWriteCachedTokens1h(params.Usage.PromptTokensDetails.WriteCached1HourTokens)
	}

	// Set completion tokens details if available
	if params.Usage.CompletionTokensDetails != nil {
		mut.SetCompletionAudioTokens(params.Usage.CompletionTokensDetails.AudioTokens)
		mut.SetCompletionReasoningTokens(params.Usage.CompletionTokensDetails.ReasoningTokens)
		mut.SetCompletionAcceptedPredictionTokens(params.Usage.CompletionTokensDetails.AcceptedPredictionTokens)
		mut.SetCompletionRejectedPredictionTokens(params.Usage.CompletionTokensDetails.RejectedPredictionTokens)
	}

	// Calculate cost if price is configured
	var (
		totalCost        *float64
		costItems        []objects.CostItem
		priceReferenceID string
	)

	if providerUsage != nil || existing == nil || existing.TotalTokens == 0 && NeedsUsageLogWithoutTokens(params.CostSnapshot) {
		costItems, totalCost, priceReferenceID = s.computeUsageCost(ctx, params.ChannelID, params.ActualModelID, providerUsage, params.CostSnapshot)
		mut.SetCostItems(costItems)
	}
	if params.SettledCost != nil {
		totalCost = lo.ToPtr(params.SettledCost.Decimal().InexactFloat64())
	}
	if params.CostSnapshot != nil {
		mut.SetCostSnapshot(params.CostSnapshot)
	}

	if totalCost != nil {
		mut.SetTotalCost(*totalCost)
	}

	if priceReferenceID != "" {
		mut.SetCostPriceReferenceID(priceReferenceID)
	}

	usageLog, err := save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to save usage log: %w", err)
	}

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "Saved usage log",
			log.Int("usage_log_id", usageLog.ID),
			log.Int("request_id", params.RequestID),
			log.String("model_id", params.ActualModelID),
			log.Int64("total_tokens", params.Usage.TotalTokens),
		)
	}

	if s.OnUsageLogCreated != nil {
		s.OnUsageLogCreated()
	}

	return usageLog, nil
}

// CreateUsageLogFromRequest creates a usage log from request and response data.
func (s *UsageLogService) CreateUsageLogFromRequest(
	ctx context.Context,
	request *ent.Request,
	requestExec *ent.RequestExecution,
	usage *llm.Usage,
) (*ent.UsageLog, error) {
	return s.createUsageLogFromRequest(ctx, request, requestExec, usage, nil)
}

func (s *UsageLogService) createUsageLogFromRequest(ctx context.Context, request *ent.Request, requestExec *ent.RequestExecution, usage *llm.Usage, recordedCost *budget.Amount) (*ent.UsageLog, error) {
	if request == nil || requestExec == nil {
		return nil, nil
	}

	s.recordChatTokens(ctx, requestExec, usage)
	params := CreateUsageLogParams{
		CostSnapshot:  requestExec.CostSnapshot,
		RequestID:     request.ID,
		ProjectID:     request.ProjectID,
		ChannelID:     requestExec.ChannelID,
		ActualModelID: requestExec.ModelID,
		Usage:         usage,
		Source:        usagelog.Source(request.Source),
		Format:        request.Format,
		APIKeyID:      lo.ToPtr(request.APIKeyID),
	}
	cost := recordedCost
	var err error
	if cost == nil {
		cost, err = s.actualBudgetCost(ctx, usage, requestExec.CostSnapshot)
	}
	params.SettledCost = cost
	if evidenceErr := s.BudgetService.RecordUsage(ctx, requestExec.ID, usage, requestExec.CostSnapshot, cost); evidenceErr != nil {
		return nil, evidenceErr
	}
	if err != nil {
		return nil, err
	}
	result, handled, err := s.BudgetService.Settle(ctx, requestExec.ID, cost, func(ctx context.Context, client *ent.Client, existing *ent.UsageLog) (*ent.UsageLog, error) {
		writer := *s
		writer.OnUsageLogCreated = nil
		return writer.saveUsageLog(ent.NewContext(ctx, client), params, existing)
	})
	if err != nil {
		return nil, err
	}
	if !handled {
		return s.CreateUsageLog(ctx, params)
	}
	if result != nil && s.OnUsageLogCreated != nil {
		s.OnUsageLogCreated()
	}
	return result, nil
}

// recordChatTokens reports the token usage of a chat request to the metrics
// registry. Channel name resolution is best-effort: it must never block usage
// log creation.
func (s *UsageLogService) recordChatTokens(ctx context.Context, requestExec *ent.RequestExecution, usage *llm.Usage) {
	if !metrics.Enabled() || requestExec == nil || usage == nil || usage.TotalTokens <= 0 {
		return
	}

	channelID := ""
	channelName := ""

	if requestExec.ChannelID != 0 {
		channelID = strconv.Itoa(requestExec.ChannelID)

		// The enabled-channel cache covers the common case without a database
		// round trip; a missing entry (e.g. disabled or deleted channel) only
		// degrades the label.
		if channel := s.ChannelService.GetEnabledChannel(requestExec.ChannelID); channel != nil {
			channelName = channel.Name
		}
	}

	metrics.Metrics.RecordChatTokens(ctx, channelID, channelName, usage.TotalTokens)
}
