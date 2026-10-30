package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

func (svc *ChannelService) TodayUsage(ctx context.Context, ids []int) ([]*objects.ChannelTodayUsage, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeReadChannels); err != nil {
		return nil, err
	}
	visible, err := authz.RunWithScopeDecision(ctx, scopes.ScopeReadChannels, func(ctx context.Context) ([]int, error) {
		return svc.entFromContext(ctx).Channel.Query().Where(channel.IDIn(ids...)).IDs(ctx)
	})
	if err != nil || len(visible) == 0 {
		return []*objects.ChannelTodayUsage{}, err
	}
	return authz.RunWithSystemBypass(ctx, "channel-today-usage", func(ctx context.Context) ([]*objects.ChannelTodayUsage, error) {
		settings, err := svc.SystemService.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		location := svc.SystemService.TimeLocation(ctx)
		now := time.Now().In(location)
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		end := start.AddDate(0, 0, 1)
		rows, err := queryChannelTodayUsage(ctx, svc.entFromContext(ctx), visible, start, end)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			row.CurrencyCode = settings.CurrencyCode
		}
		return rows, nil
	})
}

// Aggregate usage per request before joining so multiple usage rows do not
// multiply the request count. Missing prices stay distinct from explicit zero.
func queryChannelTodayUsage(ctx context.Context, client *ent.Client, ids []int, start, end time.Time) ([]*objects.ChannelTodayUsage, error) {
	timezone := start.Location().String()
	start, end = start.UTC(), end.UTC()
	var rows []struct {
		ChannelID     int     `json:"channel_id"`
		RequestCount  int     `json:"request_count"`
		UnpricedCount int     `json:"unpriced_count"`
		TotalCost     float64 `json:"total_cost"`
	}
	err := client.Request.Query().Where(request.ChannelIDIn(ids...), request.CreatedAtGTE(start), request.CreatedAtLT(end)).
		Modify(func(q *sql.Selector) {
			u := usageForConsumption(0, q.Dialect(), &start, end)
			q.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(q.C("id"), u.C("request_id")), sql.ColumnsEQ(q.C("channel_id"), u.C("channel_id")), sql.ColumnsEQ(q.C("project_id"), u.C("project_id"))))
			q.Select(q.C("channel_id"), sql.As(sql.Count("*"), "request_count"),
				sql.As("COALESCE(SUM("+u.C("total_cost")+"), 0)", "total_cost"),
				sql.As("SUM(CASE WHEN "+u.C("usage_count")+" IS NULL OR "+u.C("priced_count")+" < "+u.C("usage_count")+" THEN 1 ELSE 0 END)", "unpriced_count")).GroupBy(q.C("channel_id"))
		}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]*objects.ChannelTodayUsage, len(rows))
	for _, row := range rows {
		byID[row.ChannelID] = &objects.ChannelTodayUsage{RequestCount: row.RequestCount, UnpricedCount: row.UnpricedCount, TotalCost: row.TotalCost}
	}
	result := make([]*objects.ChannelTodayUsage, 0, len(ids))
	for _, id := range ids {
		entry := byID[id]
		if entry == nil {
			entry = &objects.ChannelTodayUsage{}
		}
		entry.ChannelID = objects.GUID{ID: id, Type: ent.TypeChannel}
		entry.Timezone = timezone
		result = append(result, entry)
	}
	return result, nil
}
