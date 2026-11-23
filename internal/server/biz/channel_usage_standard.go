package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
)

// Pricing evidence is parsed once per bill revision. These derived numeric
// fields are private to statistics; the original bill remains authoritative.
// The caller owns the same transaction used for the following aggregate read.
func prepareChannelStandardCosts(ctx context.Context, client *ent.Client, ids []int, start, end time.Time) error {
	versions, loaded := map[string]objects.ModelPrice{}, map[string]bool{}
	lastID := 0
	for {
		var rows []channelUsageRow
		err := client.UsageLog.Query().Where(usagelog.ChannelIDIn(ids...), usagelog.StatisticsStandardReadyEQ(false), usagelog.IDGT(lastID),
			usagelog.Or(usagelog.StatisticsStandardCostNotNil(), usagelog.And(usagelog.TotalCostNotNil(),
				usagelog.Or(usagelog.CostItemsNotNil(), usagelog.CostSnapshotNotNil(), usagelog.CostPriceReferenceIDNEQ("")))),
		).Unique(false).
			Order(ent.Asc(usagelog.FieldID)).Limit(channelUsageBatchSize).
			Modify(func(q *sql.Selector) {
				r := requestHistory(q.Dialect()).As("price_request")
				q.Join(r).OnP(sql.And(sql.ColumnsEQ(q.C("request_id"), r.C("id")), sql.ColumnsEQ(q.C("project_id"), r.C("project_id")))).
					Where(sql.And(sql.GTE(r.C("created_at"), start.UTC()), sql.LT(r.C("created_at"), end.UTC())))
				q.Select(q.Columns(channelUsageFields...)...).AppendSelect(q.C(usagelog.FieldStatisticsVersion))
			}).Scan(ctx, &rows)
		if err != nil || len(rows) == 0 {
			return err
		}
		logs := make([]*ent.UsageLog, len(rows))
		for i := range rows {
			rows[i].UsageLog.CostItems = rows[i].CostItems
			logs[i] = &rows[i].UsageLog
		}
		if err := loadStandardCostVersionsInto(ctx, client, logs, versions, loaded); err != nil {
			return err
		}
		values := make([]any, len(rows))
		for i, row := range logs {
			if cost, known := usageStandardCost(row, versions); known {
				values[i] = cost.InexactFloat64()
			}
		}
		lastID = logs[len(logs)-1].ID
		if client.Driver().Dialect() == dialect.Postgres {
			if err := savePostgresStandardCosts(ctx, client, logs, values); err != nil {
				return err
			}
			continue
		}
		keys := make([]int, len(rows))
		conditions := make([]*sql.Predicate, len(rows))
		for i, row := range logs {
			keys[i] = row.ID
			conditions[i] = sql.And(sql.EQ(usagelog.FieldID, row.ID), sql.EQ(usagelog.FieldStatisticsVersion, row.StatisticsVersion))
		}
		update := sql.Dialect(client.Driver().Dialect()).Update(usagelog.Table).
			Set(usagelog.FieldStatisticsStandardReady, true).
			Set(usagelog.FieldStatisticsStandardCost, sql.ExprFunc(func(b *sql.Builder) {
				b.WriteString("CASE ").Ident(usagelog.FieldID)
				for i, id := range keys {
					b.WriteString(" WHEN ").Arg(id).WriteString(" THEN ").Arg(values[i])
				}
				b.WriteString(" ELSE NULLIF(0.0, 0.0) END")
			})).Where(sql.Or(conditions...))
		if err := saveStatisticsProjection(ctx, client, update, len(rows)); err != nil {
			return err
		}
	}
}

// Join each bill to its own price and source revision in one set update.
func savePostgresStandardCosts(ctx context.Context, client *ent.Client, logs []*ent.UsageLog, values []any) error {
	b := sql.Builder{}
	b.SetDialect(dialect.Postgres)
	b.WriteString("UPDATE usage_logs AS u SET statistics_standard_ready = TRUE, statistics_standard_cost = v.cost FROM (VALUES ")
	for i, row := range logs {
		if i > 0 {
			b.Comma()
		}
		b.WriteByte('(').Arg(row.ID).WriteString("::bigint, ").Arg(row.StatisticsVersion).WriteString("::bigint, ").Arg(values[i]).WriteString("::double precision)")
	}
	b.WriteString(") AS v(id, version, cost) WHERE u.id = v.id AND u.statistics_version = v.version")
	stmt, args := b.Query()
	return execStatisticsProjection(ctx, client, stmt, args, len(logs))
}
