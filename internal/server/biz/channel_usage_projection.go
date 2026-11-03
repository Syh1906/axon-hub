package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/shopspring/decimal"
)

// Decode only the historical evidence needed for 1x pricing. Detailed tier
// breakdowns, service-tier decisions and reference prices can be very large;
// they are never displayed or used by this aggregation.
type channelUsageCostItems []objects.CostItem

func (items *channelUsageCostItems) Scan(value any) error {
	switch data := value.(type) {
	case nil:
		*items = nil
		return nil
	case []byte:
		return items.UnmarshalJSON(data)
	case string:
		return items.UnmarshalJSON([]byte(data))
	default:
		return fmt.Errorf("unexpected cost items database type %T", value)
	}
}

func (items *channelUsageCostItems) UnmarshalJSON(data []byte) error {
	var values []struct {
		Subtotal        decimal.Decimal      `json:"subtotal"`
		Multiplier      *decimal.Decimal     `json:"multiplier"`
		Unpriced        bool                 `json:"unpriced"`
		Image           *objects.ImageOutput `json:"image"`
		PricingEvidence *struct {
			ModeSubtotal   *decimal.Decimal `json:"modeSubtotal"`
			BaseSubtotal   decimal.Decimal  `json:"baseSubtotal"`
			ModeMultiplier *decimal.Decimal `json:"modeMultiplier"`
			CostMultiplier decimal.Decimal  `json:"costMultiplier"`
		} `json:"pricingEvidence"`
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*items = make(channelUsageCostItems, len(values))
	for i, value := range values {
		item := objects.CostItem{Subtotal: value.Subtotal, Multiplier: value.Multiplier, Unpriced: value.Unpriced, Image: value.Image}
		if evidence := value.PricingEvidence; evidence != nil {
			item.PricingEvidence = &objects.CostPricingEvidence{ModeSubtotal: evidence.ModeSubtotal, BaseSubtotal: evidence.BaseSubtotal,
				ModeMultiplier: evidence.ModeMultiplier, CostMultiplier: evidence.CostMultiplier}
		}
		(*items)[i] = item
	}
	return nil
}

type channelUsageRow struct {
	ent.UsageLog
	CostItems        channelUsageCostItems `json:"cost_items"`
	UsageRequestID   int                   `json:"usage_request_id"`
	Model            string                `json:"model"`
	RequestCreatedAt time.Time             `json:"request_created_at"`
	Terminal         bool                  `json:"terminal"`
	FirstToken       *float64              `json:"first_token"`
	Outcome          *float64              `json:"outcome"`
	Speed            *float64              `json:"speed"`
}

// Use the channel/day index and a stable (created_at, id) cursor. Read the
// request page, final attempt and its usage in one database round trip.
func channelUsageBatchQuery(db string, id int, start, end, afterTime time.Time, afterID int) *sql.Selector {
	r := sql.Table(request.Table)
	page := sql.Dialect(db).Select(r.C("id"), r.C("project_id"), r.C("channel_id"), r.C("created_at"), r.C("model_id"), r.C("status")).From(r).
		Where(sql.And(sql.EQ(r.C("channel_id"), id), sql.GTE(r.C("created_at"), start.UTC()), sql.LT(r.C("created_at"), end.UTC()))).
		OrderBy(r.C("created_at"), r.C("id")).Limit(channelUsageBatchSize)
	if afterID > 0 {
		page.Where(sql.And(sql.GTE(r.C("created_at"), afterTime), sql.Or(sql.GT(r.C("created_at"), afterTime), sql.GT(r.C("id"), afterID))))
	}
	q := sql.Dialect(db).Select().From(page.As("usage_request")).As("usage_request")
	execution := joinLatestConsumptionExecution(q)
	u := sql.Table(usagelog.Table).As("usage_log")
	q.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(q.C("id"), u.C("request_id")), sql.ColumnsEQ(q.C("channel_id"), u.C("channel_id")), sql.ColumnsEQ(q.C("project_id"), u.C("project_id"))))
	outcome := consumptionOutcome(execution)
	speed := usageanalytics.OutputSpeedSQL(usageanalytics.TextTokensSQL(u.C("completion_tokens"), u.C("cost_snapshot"), u.C("completion_audio_tokens"), db),
		execution.C("metrics_latency_ms"), execution.C("metrics_first_byte_latency_ms"), execution.C("stream"), execution.C("format"))
	q.Select(sql.As(q.C("id"), "usage_request_id"), sql.As(q.C("created_at"), "request_created_at"),
		sql.As("COALESCE(NULLIF(TRIM("+execution.C("model_id")+"), ''), "+q.C("model_id")+")", "model"),
		sql.As(q.C("status")+" IN ('completed', 'failed', 'canceled')", "terminal"),
		sql.As(execution.C("metrics_first_token_latency_ms"), "first_token"), sql.As(outcome, "outcome"), sql.As(speed, "speed"))
	for _, field := range []string{usagelog.FieldID, usagelog.FieldRequestID, usagelog.FieldProjectID, usagelog.FieldCreatedAt,
		usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID, usagelog.FieldPromptTokens,
		usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens, usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens,
		usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h} {
		q.AppendSelect(u.C(field))
	}
	return q.OrderBy(q.C("created_at"), q.C("id"), u.C("id"))
}

// The caller has checked channel visibility and runs this read inside a single
// snapshot. Limit requests before joining: multiple usage rows stay together.
func queryChannelUsageBatch(ctx context.Context, client *ent.Client, id int, start, end, afterTime time.Time, afterID int) ([]channelUsageRow, error) {
	query, args := channelUsageBatchQuery(client.Driver().Dialect(), id, start, end, afterTime, afterID).Query()
	var cursor sql.Rows
	if err := client.Driver().Query(ctx, query, args, &cursor); err != nil {
		return nil, err
	}
	defer cursor.Close()
	var rows []channelUsageRow
	if err := sql.ScanSlice(&cursor, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
