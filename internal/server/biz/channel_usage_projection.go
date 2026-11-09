package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
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
	LogicalRequest   bool                  `json:"logical_request"`
	Archived         bool                  `json:"archived"`
}

var channelUsageFields = []string{usagelog.FieldID, usagelog.FieldRequestID, usagelog.FieldProjectID, usagelog.FieldChannelID, usagelog.FieldModelID, usagelog.FieldCreatedAt,
	usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID, usagelog.FieldPromptTokens,
	usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens, usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens,
	usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h,
	usagelog.FieldPromptTextTokens, usagelog.FieldPromptImageTokens, usagelog.FieldPromptTextCachedTokens, usagelog.FieldPromptImageCachedTokens}

func channelUsageHistory(db string) *sql.Selector {
	live := sql.Table(usagelog.Table)
	archive := sql.Table("usage_archives")
	live.SetDialect(db)
	archive.SetDialect(db)
	liveColumns, historyColumns := make([]string, 0, len(channelUsageFields)), make([]string, 0, len(channelUsageFields))
	for _, field := range channelUsageFields {
		liveColumns = append(liveColumns, live.C(field))
		column := field
		switch field {
		case "id":
			column = "source_id"
		case "created_at":
			column = "occurred_at"
		case usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID, usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h,
			usagelog.FieldPromptTextTokens, usagelog.FieldPromptImageTokens, usagelog.FieldPromptTextCachedTokens, usagelog.FieldPromptImageCachedTokens:
			historyColumns = append(historyColumns, "NULL AS "+field)
			continue
		}
		historyColumns = append(historyColumns, sql.As(archive.C(column), field))
	}
	liveColumns = append(liveColumns, "FALSE AS archived")
	historyColumns = append(historyColumns, "TRUE AS archived")
	return sql.Dialect(db).Select(liveColumns...).From(live).UnionAll(
		sql.Dialect(db).Select(historyColumns...).From(archive).Where(sql.EQ(archive.C("kind"), "usage")))
}

// Bound the channel/day page with a stable (created_at, id) cursor, including
// requests finalized elsewhere that still have billing on this channel.
func channelUsageBatchQuery(db string, id int, start, end, afterTime time.Time, afterID int) *sql.Selector {
	r := requestHistory(db)
	bill := usageHistory(db).As("channel_bill")
	charged := sql.Dialect(db).Select(bill.C("id")).From(bill).Where(sql.And(sql.EQ(bill.C("channel_id"), id), sql.ColumnsEQ(bill.C("request_id"), r.C("id")), sql.ColumnsEQ(bill.C("project_id"), r.C("project_id"))))
	columns := make([]string, 0, 9)
	for _, field := range []string{"id", "project_id", "channel_id", "created_at", "model_id", "status", "failure", "result_execution_id", "attribution_model_id"} {
		columns = append(columns, r.C(field))
	}
	page := sql.Dialect(db).Select(columns...).From(r).
		Where(sql.And(sql.Or(sql.EQ(r.C("channel_id"), id), sql.Exists(charged)), sql.GTE(r.C("created_at"), start.UTC()), sql.LT(r.C("created_at"), end.UTC()))).
		OrderBy(r.C("created_at"), r.C("id")).Limit(channelUsageBatchSize)
	if afterID > 0 {
		page.Where(sql.And(sql.GTE(r.C("created_at"), afterTime), sql.Or(sql.GT(r.C("created_at"), afterTime), sql.GT(r.C("id"), afterID))))
	}
	q := sql.Dialect(db).Select().From(page.As("usage_request")).As("usage_request")
	execution := joinLatestConsumptionExecution(q)
	u := channelUsageHistory(db).As("usage_log")
	q.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(q.C("id"), u.C("request_id")), sql.EQ(u.C("channel_id"), id), sql.ColumnsEQ(q.C("project_id"), u.C("project_id"))))
	finalModel := "COALESCE(NULLIF(TRIM(" + execution.C("model_id") + "), ''), " + q.C("attribution_model_id") + ")"
	logical := q.C("channel_id") + " = " + strconv.Itoa(id)
	q.Select(sql.As(q.C("id"), "usage_request_id"), sql.As(q.C("created_at"), "request_created_at"),
		u.C("archived"),
		sql.As(logical, "logical_request"),
		sql.As("CASE WHEN "+logical+" THEN "+finalModel+" ELSE COALESCE(NULLIF(TRIM("+u.C("model_id")+"), ''), "+finalModel+") END", "model"))
	for _, field := range channelUsageFields {
		q.AppendSelect(u.C(field))
	}
	return q.OrderBy(q.C("created_at"), q.C("id"), "model", u.C("id"))
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
