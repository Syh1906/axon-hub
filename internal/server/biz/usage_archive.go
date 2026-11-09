package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

// ArchivedUsageFields deliberately excludes payloads and pricing JSON. Detail
// deletion and these inserts must share the caller's transaction.
var ArchivedUsageFields = []string{
	usagelog.FieldID, usagelog.FieldRequestID, usagelog.FieldProjectID,
	usagelog.FieldAPIKeyID, usagelog.FieldChannelID, usagelog.FieldModelID, usagelog.FieldCreatedAt,
	usagelog.FieldPromptTokens, usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens,
	usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens,
	usagelog.FieldCompletionReasoningTokens, usagelog.FieldTotalCost,
}

func ArchiveUsageStatistics(ctx context.Context, client *ent.Client, rows []*ent.UsageLog) error {
	for _, row := range rows {
		err := client.UsageArchive.Create().SetKind(usagearchive.KindUsage).SetSourceID(row.ID).
			SetRequestID(row.RequestID).SetProjectID(row.ProjectID).SetAPIKeyID(row.APIKeyID).
			SetChannelID(row.ChannelID).SetModelID(row.ModelID).SetOccurredAt(row.CreatedAt).
			SetPromptTokens(row.PromptTokens).SetCompletionTokens(row.CompletionTokens).SetTotalTokens(row.TotalTokens).
			SetPromptCachedTokens(row.PromptCachedTokens).SetPromptWriteCachedTokens(row.PromptWriteCachedTokens).
			SetCompletionReasoningTokens(row.CompletionReasoningTokens).SetNillableTotalCost(row.TotalCost).
			OnConflict(sql.ConflictColumns(usagearchive.FieldKind, usagearchive.FieldSourceID)).DoNothing().Exec(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

type archivedRequestFacts struct {
	ID                int       `json:"id"`
	ProjectID         int       `json:"project_id"`
	APIKeyID          int       `json:"api_key_id"`
	ChannelID         int       `json:"channel_id"`
	CreatedAt         time.Time `json:"created_at"`
	Model             string    `json:"model"`
	RequestModel      string    `json:"request_model"`
	RequestStatus     string    `json:"request_status"`
	RequestSource     string    `json:"request_source"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	CacheRead         int64     `json:"cache_read"`
	CacheWrite        int64     `json:"cache_write"`
	Cost              float64   `json:"cost"`
	UnpricedRequests  int       `json:"unpriced_requests"`
	CacheCreationCost float64   `json:"cache_creation_cost"`
	CacheRequests     int64     `json:"cache_requests"`
	SuccessCount      int64     `json:"success_count"`
	OutcomeCount      int64     `json:"outcome_count"`
	FirstTokenSum     float64   `json:"first_token_sum"`
	FirstTokenCount   int64     `json:"first_token_count"`
	SpeedSum          float64   `json:"speed_sum"`
	SpeedCount        int64     `json:"speed_count"`
}

func excludeArchivedRequests(s *sql.Selector) {
	a := sql.Table(usagearchive.Table).As("archived_request")
	s.Where(sql.Not(sql.Exists(sql.Dialect(s.Dialect()).Select(a.C("id")).From(a).
		Where(sql.And(sql.EQ(a.C("kind"), string(usagearchive.KindRequest)), sql.ColumnsEQ(a.C("source_id"), s.C("id")), sql.ColumnsEQ(a.C("project_id"), s.C("project_id")))))))
}

func ArchiveRequestStatistics(ctx context.Context, client *ent.Client, requestIDs []int) error {
	if len(requestIDs) == 0 {
		return nil
	}
	if err := archiveExecutionStatistics(ctx, client, requestIDs); err != nil {
		return err
	}
	var rows []archivedRequestFacts
	err := client.Request.Query().Where(request.IDIn(requestIDs...)).Modify(func(q *sql.Selector) {
		excludeArchivedRequests(q)
		u, execution := joinConsumption(q, 0, nil, time.Now())
		// Restrict the usage aggregation itself, not only its outer join.
		u.Where(sql.InInts(sql.Table(usagelog.Table).As("ul").C("request_id"), requestIDs...))
		outcome, speed := consumptionOutcomeAndSpeed(u, execution)
		firstToken := usageanalytics.FirstTokenSQL(execution.C("metrics_first_token_latency_ms"), execution.C("metrics_latency_ms"), execution.C("stream"), execution.C("status"))
		cacheRead, cacheWrite := "COALESCE("+u.C("prompt_cached_tokens")+", 0)", "COALESCE("+u.C("prompt_write_cached_tokens")+", 0)"
		q.Select(q.C("id"), q.C("project_id"), q.C("created_at"),
			sql.As(q.C("model_id"), "request_model"), sql.As(q.C("status"), "request_status"), sql.As(q.C("source"), "request_source"),
			sql.As("COALESCE("+q.C("api_key_id")+", 0)", "api_key_id"), sql.As("COALESCE("+q.C("channel_id")+", 0)", "channel_id"),
			sql.As("COALESCE(NULLIF(TRIM("+execution.C("model_id")+"), ''), "+q.C("model_id")+")", "model"))
		for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
			q.AppendSelect(sql.As("COALESCE("+u.C(field)+", 0)", field))
		}
		q.AppendSelect(sql.As(cacheRead, "cache_read"), sql.As(cacheWrite, "cache_write"),
			sql.As(consumptionUnpriced(u), "unpriced_requests"),
			sql.As("COALESCE("+u.C("total_cost")+", 0)", "cost"),
			sql.As("CASE WHEN "+cacheWrite+" > 0 THEN COALESCE("+u.C("total_cost")+",0) ELSE 0 END", "cache_creation_cost"),
			sql.As("CASE WHEN "+cacheRead+" > 0 OR "+cacheWrite+" > 0 THEN 1 ELSE 0 END", "cache_requests"),
			sql.As("COALESCE(("+outcome+"),0)", "success_count"), sql.As("CASE WHEN ("+outcome+") IS NULL THEN 0 ELSE 1 END", "outcome_count"),
			sql.As("COALESCE("+firstToken+",0)", "first_token_sum"), sql.As("CASE WHEN "+firstToken+" IS NULL THEN 0 ELSE 1 END", "first_token_count"),
			sql.As("COALESCE(("+speed+"),0)", "speed_sum"), sql.As("CASE WHEN ("+speed+") IS NULL THEN 0 ELSE 1 END", "speed_count"))
	}).Scan(ctx, &rows)
	if err != nil {
		return err
	}
	for _, row := range rows {
		err := client.UsageArchive.Create().SetKind(usagearchive.KindRequest).SetSourceID(row.ID).SetRequestID(row.ID).
			SetRequestModelID(row.RequestModel).SetRequestStatus(row.RequestStatus).SetRequestSource(row.RequestSource).
			SetProjectID(row.ProjectID).SetAPIKeyID(row.APIKeyID).SetChannelID(row.ChannelID).SetModelID(row.Model).SetOccurredAt(row.CreatedAt).
			SetPromptTokens(row.PromptTokens).SetCompletionTokens(row.CompletionTokens).SetTotalTokens(row.TotalTokens).
			SetPromptCachedTokens(row.CacheRead).SetPromptWriteCachedTokens(row.CacheWrite).SetCacheInput(row.PromptTokens).
			SetTotalCost(row.Cost).SetCacheCreationCost(row.CacheCreationCost).SetCacheRequests(row.CacheRequests).
			SetPricingComplete(row.UnpricedRequests == 0).SetBillingVersion(requestBillingVersion).SetSpeedVersion(usageanalytics.OutputSpeedVersion).
			SetSuccessCount(row.SuccessCount).SetOutcomeCount(row.OutcomeCount).
			SetFirstTokenSum(row.FirstTokenSum).SetFirstTokenCount(row.FirstTokenCount).SetSpeedSum(row.SpeedSum).SetSpeedCount(row.SpeedCount).
			OnConflict(sql.ConflictColumns(usagearchive.FieldKind, usagearchive.FieldSourceID)).DoNothing().Exec(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}
