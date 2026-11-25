package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

func (s *APIKeyService) GroupCostComparison(ctx context.Context, system *SystemService, groupID int, start, end string) (*objects.GroupCostComparison, error) {
	project, err := s.costReportProject(ctx, groupID)
	if err != nil {
		return nil, err
	}
	first, last, err := selfUsageDateRange(start, end, system.TimeLocation(ctx))
	if err != nil {
		return nil, err
	}
	db := s.entFromContext(ctx)
	return groupCostReport(ctx, db, project, groupID, first.UTC(), last.AddDate(0, 0, 1).UTC())
}

func groupCostReport(ctx context.Context, db *ent.Client, project, group int, start, end time.Time) (*objects.GroupCostComparison, error) {
	d := sql.Dialect(db.Driver().Dialect())
	ledger := sql.Table("ledger")
	ledger.SetDialect(db.Driver().Dialect())
	// Aggregate all three levels in one statement so rows and totals share one database snapshot.
	aggregate := func(key, channel bool) *sql.Selector {
		q := d.Select().From(ledger)
		level := "0"
		if key {
			level = "1"
		}
		if channel {
			level = "2"
		}
		q.AppendSelectExprAs(sql.Expr(level), "report_level")
		for _, col := range []struct {
			name string
			use  bool
		}{{"api_key_id", key}, {"channel_id", channel}} {
			if col.use {
				q.AppendSelectAs("COALESCE("+ledger.C(col.name)+",0)", col.name)
				q.GroupBy(ledger.C(col.name))
			} else {
				q.AppendSelectExprAs(sql.Expr("0"), col.name)
			}
		}
		q.AppendSelectAs("COUNT(DISTINCT "+ledger.C("request_id")+")", "requests")
		for _, col := range []string{"total_cost", "billed_cost"} {
			q.AppendSelectAs("SUM("+ledger.C(col)+")", col)
			q.AppendSelectAs("COUNT(*) - COUNT("+ledger.C(col)+")", "missing_"+col)
		}
		q.AppendSelectAs("COUNT(CASE WHEN "+ledger.C("billing_estimated")+" THEN 1 END)", "estimated")
		return q
	}
	q, args := aggregate(false, false).Prefix(d.With("ledger").As(groupCostLedger(db.Driver().Dialect(), project, group, start, end))).
		UnionAll(aggregate(true, false)).UnionAll(aggregate(true, true)).Query()
	var cursor sql.Rows
	if err := db.Driver().Query(ctx, q, args, &cursor); err != nil {
		return nil, err
	}
	result := &objects.GroupCostComparison{Keys: []*objects.GroupCostRow{}, Channels: []*objects.GroupCostRow{}}
	defer cursor.Close()
	keyIDs, channelIDs := map[int]bool{}, map[int]bool{}
	for cursor.Next() {
		r := &objects.GroupCostRow{}
		var level, channelID int
		if err := cursor.Scan(&level, &r.KeyID, &channelID, &r.Requests, &r.ChannelCost, &r.MissingChannel, &r.BilledCost, &r.MissingBilled, &r.Estimated); err != nil {
			return nil, err
		}
		if level == 0 {
			result.Total = r
			continue
		}
		keyIDs[r.KeyID] = true
		if level == 1 {
			result.Keys = append(result.Keys, r)
		} else {
			r.ChannelID = &channelID
			channelIDs[channelID] = true
			result.Channels = append(result.Channels, r)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	if err := cursor.Close(); err != nil {
		return nil, err
	}
	if len(result.Keys) == 0 && result.Total != nil {
		result.Total.ChannelCost = lo.ToPtr(0.0)
		result.Total.BilledCost = lo.ToPtr(0.0)
		return result, nil
	}
	ctx = schematype.SkipSoftDelete(ctx)
	keys, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(project), apikey.IDIn(lo.Keys(keyIDs)...)).Select(apikey.FieldName).All(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := db.Channel.Query().Where(channel.IDIn(lo.Keys(channelIDs)...)).Select(channel.FieldName).All(ctx)
	if err != nil {
		return nil, err
	}
	keyNames := lo.SliceToMap(keys, func(k *ent.APIKey) (int, string) { return k.ID, k.Name })
	chNames := lo.SliceToMap(channels, func(c *ent.Channel) (int, string) { return c.ID, c.Name })
	for _, r := range append(append([]*objects.GroupCostRow{}, result.Keys...), result.Channels...) {
		r.KeyName = keyNames[r.KeyID]
		if r.KeyName == "" {
			r.KeyName = fmt.Sprintf("#%d", r.KeyID)
		}
		if r.ChannelID != nil {
			r.ChannelName = lo.EmptyableToPtr(chNames[*r.ChannelID])
		}
	}
	return result, nil
}

func (s *APIKeyService) costReportProject(ctx context.Context, groupID int) (int, error) {
	if err := requireRateAdmin(ctx, scopes.ScopeReadChannels); err != nil {
		return 0, err
	}
	if err := authz.RequireScope(ctx, scopes.ScopeReadRequests); err != nil {
		return 0, err
	}
	project, _, err := s.managedUsageGroup(ctx, groupID)
	if err != nil {
		return 0, err
	}
	user, _ := contexts.GetUser(ctx)
	owner, err := NewPermissionValidator().isProjectOwner(ctx, user, &project)
	if err != nil {
		return 0, err
	}
	if !owner && !scopes.UserHasScope(ctx, scopes.ScopeWriteChannels) {
		return 0, fmt.Errorf("channel administration required")
	}
	if groupID <= 0 {
		return 0, fmt.Errorf("billing group required")
	}
	return project, nil
}

// A settled budget charge links an older bill to its historical owner. Without
// that link, present membership alone cannot establish a legacy bill's group.
func groupCostLedger(dialect string, project, group int, start, end time.Time) *sql.Selector {
	d := sql.Dialect(dialect)
	window := func(t *sql.SelectTable, ts, usageID string) *sql.Selector {
		t.SetDialect(dialect)
		id := t.C(usageID)
		charge := sql.Table(budgetcharge.Table)
		charge.SetDialect(dialect)
		legacy := d.Select(charge.C("id")).From(charge).Where(sql.And(sql.EQ(charge.C("project_id"), project), sql.EQ(charge.C("group_id"), group), sql.ColumnsEQ(charge.C("request_id"), t.C("request_id")), sql.ColumnsEQ(charge.C("usage_log_id"), id), sql.EQ(charge.C("status"), "settled")))
		// 归档请求优先；只有用量恢复而没有请求时才按账单日期归属。
		liveRequest := sql.Table(request.Table).As("live_request")
		oldRequest := sql.Table(usagearchive.Table).As("archived_request")
		liveRequest.SetDialect(dialect)
		oldRequest.SetDialect(dialect)
		q := d.Select().From(t).
			LeftJoin(liveRequest).OnP(sql.And(sql.ColumnsEQ(liveRequest.C("id"), t.C("request_id")), sql.ColumnsEQ(liveRequest.C("project_id"), t.C("project_id")))).
			LeftJoin(oldRequest).OnP(sql.And(sql.EQ(oldRequest.C("kind"), string(usagearchive.KindRequest)), sql.ColumnsEQ(oldRequest.C("source_id"), t.C("request_id")), sql.ColumnsEQ(oldRequest.C("project_id"), t.C("project_id"))))
		billTime := "COALESCE(" + oldRequest.C("occurred_at") + "," + liveRequest.C("created_at") + "," + t.C(ts) + ")"
		q.Select(t.C("api_key_id"), t.C("channel_id"), t.C("request_id"), t.C("total_cost"),
			sql.As(billedCostExpression(t.C("billing_group_id"), t.C("total_cost"), t.C("billed_cost")), "billed_cost"), t.C("billing_estimated"), sql.As(id, "usage_id"))
		return q.Where(sql.And(sql.EQ(t.C("project_id"), project), sql.Or(sql.EQ(t.C("billing_group_id"), group), sql.And(sql.IsNull(t.C("billing_group_id")), sql.Exists(legacy))), sql.P(func(b *sql.Builder) { b.WriteString(billTime + " >= ").Arg(start) }), sql.P(func(b *sql.Builder) { b.WriteString(billTime + " < ").Arg(end) })))
	}
	live := sql.Table(usagelog.Table)
	archive := sql.Table(usagearchive.Table).As("archived_bill")
	existing := sql.Table(usagelog.Table).As("live_bill")
	existing.SetDialect(dialect)
	archived := window(archive, "occurred_at", "source_id").Where(sql.And(sql.EQ(archive.C("kind"), "usage"), sql.Not(sql.Exists(d.Select(existing.C("id")).From(existing).Where(sql.ColumnsEQ(existing.C("id"), archive.C("source_id")))))))
	return window(live, "created_at", "id").UnionAll(archived)
}

// Limit detail access to the same authorized historical ledger as the totals.
func (s *APIKeyService) GroupCostEntries(ctx context.Context, system *SystemService, groupID, keyID, channelID int, start, end string, before *int) ([]*objects.GroupCostEntry, error) {
	project, err := s.costReportProject(ctx, groupID)
	if err != nil {
		return nil, err
	}
	first, last, err := selfUsageDateRange(start, end, system.TimeLocation(ctx))
	if err != nil {
		return nil, err
	}
	db := s.entFromContext(ctx)
	ledger := groupCostLedger(db.Driver().Dialect(), project, groupID, first.UTC(), last.AddDate(0, 0, 1).UTC()).As("bills")
	query := sql.Dialect(db.Driver().Dialect()).Select(ledger.C("usage_id"), ledger.C("request_id"), ledger.C("total_cost"), ledger.C("billed_cost")).From(ledger).Where(sql.And(sql.EQ(ledger.C("api_key_id"), keyID), sql.EQ(ledger.C("channel_id"), channelID))).OrderBy(sql.Desc(ledger.C("usage_id"))).Limit(20)
	if before != nil {
		query.Where(sql.LT(ledger.C("usage_id"), *before))
	}
	stmt, args := query.Query()
	var cursor sql.Rows
	if err := db.Driver().Query(ctx, stmt, args, &cursor); err != nil {
		return nil, err
	}
	defer cursor.Close()
	entries := []*objects.GroupCostEntry{}
	byID := map[int]*objects.GroupCostEntry{}
	for cursor.Next() {
		e := &objects.GroupCostEntry{Archived: true}
		if err := cursor.Scan(&e.UsageID, &e.RequestID, &e.ChannelCost, &e.BilledCost); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		byID[e.UsageID] = e
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	if err := cursor.Close(); err != nil {
		return nil, err
	}
	rows, err := db.UsageLog.Query().Where(usagelog.ProjectIDEQ(project), usagelog.IDIn(lo.Keys(byID)...)).Select(usagelog.FieldCostSnapshot, usagelog.FieldCostItems, usagelog.FieldBilledItems).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		e := byID[r.ID]
		e.Archived = false
		if evidence := UsageBillingEvidence(r); evidence != nil {
			e.GroupBillingEvidence = *evidence
		}
		delete(byID, r.ID)
	}
	if len(byID) > 0 {
		archived, err := db.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage), usagearchive.ProjectIDEQ(project), usagearchive.SourceIDIn(lo.Keys(byID)...)).Select(usagearchive.FieldSourceID, usagearchive.FieldBillingEvidence).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range archived {
			if r.BillingEvidence != nil {
				byID[r.SourceID].GroupBillingEvidence = *r.BillingEvidence
			}
		}
	}
	return entries, nil
}
