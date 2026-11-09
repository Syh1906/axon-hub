package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
)

type ConsumptionGroupOption struct {
	ID   objects.GUID `json:"id"`
	Name string       `json:"name"`
}

func (s *ConsumptionService) LeaderboardGroupOptions(ctx context.Context, input ConsumptionFilter) ([]*ConsumptionGroupOption, error) {
	projectID, err := leaderboardProjectID(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.groupOptions(ctx, input, projectID)
}

func (s *ConsumptionService) UsageGroupOptions(ctx context.Context, input ConsumptionFilter) ([]*ConsumptionGroupOption, error) {
	projectID, err := usageStatisticsProjectID(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.groupOptions(ctx, input, projectID)
}

// 下拉框只查询所选时间内有数据的分组；不读取执行、费用或性能统计。
// 单条 SQL 同时判断在线与归档记录，清理过程不会使分组选项短暂消失。
func (s *ConsumptionService) groupOptions(ctx context.Context, input ConsumptionFilter, projectID int) ([]*ConsumptionGroupOption, error) {
	if _, err := consumptionEnum(string(input.Metric), "COST", "CACHE"); err != nil {
		return nil, err
	}
	return authz.RunWithSystemBypass(ctx, "consumption-group-options", func(ctx context.Context) ([]*ConsumptionGroupOption, error) {
		start, end, err := consumptionRange(input, time.Now(), s.system.TimeLocation(ctx))
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			ProjectName string `json:"project_name"`
		}
		err = s.client.APIKeyGroup.Query().Modify(func(q *sql.Selector) {
			d := sql.Dialect(q.Dialect())
			r := sql.Table(request.Table).As("option_request")
			a := sql.Table(usagearchive.Table).As("option_archive")
			k := sql.Table(apikey.Table).As("option_key")
			live := d.Select(k.C("group_ref")).From(r).
				Join(k).OnP(sql.And(sql.ColumnsEQ(r.C("api_key_id"), k.C("id")), sql.ColumnsEQ(r.C("project_id"), k.C("project_id")))).
				Where(sql.And(sql.In(r.C("status"), "completed", "failed", "canceled"), sql.LT(r.C("created_at"), end)))
			archived := d.Select(k.C("group_ref")).From(a).
				Join(k).OnP(sql.And(sql.ColumnsEQ(a.C("api_key_id"), k.C("id")), sql.ColumnsEQ(a.C("project_id"), k.C("project_id")))).
				Where(sql.And(sql.EQ(a.C("kind"), string(usagearchive.KindRequest)), sql.LT(a.C("occurred_at"), end)))
			if start != nil {
				live.Where(sql.GTE(r.C("created_at"), *start))
				archived.Where(sql.GTE(a.C("occurred_at"), *start))
			}
			if projectID > 0 {
				q.Where(sql.EQ(q.C(apikeygroup.FieldProjectID), projectID))
				live.Where(sql.EQ(r.C("project_id"), projectID))
				archived.Where(sql.EQ(a.C("project_id"), projectID))
			}
			if input.Metric == ConsumptionMetricCache {
				u := sql.Table(usagelog.Table).As("option_usage")
				live.Where(sql.Exists(d.Select(u.C("id")).From(u).Where(sql.And(
					sql.ColumnsEQ(u.C("request_id"), r.C("id")), sql.ColumnsEQ(u.C("project_id"), r.C("project_id")),
					sql.ColumnsEQ(u.C("channel_id"), r.C("channel_id")),
					sql.Or(sql.GT(u.C("prompt_cached_tokens"), 0), sql.GT(u.C("prompt_write_cached_tokens"), 0)),
				))))
				archived.Where(sql.GT(a.C("cache_requests"), 0))
			}
			q.Where(sql.Or(sql.In(q.C(apikeygroup.FieldID), live), sql.In(q.C(apikeygroup.FieldID), archived))).
				Select(q.C(apikeygroup.FieldID), q.C(apikeygroup.FieldName)).OrderBy(q.C(apikeygroup.FieldName), q.C(apikeygroup.FieldID))
			if projectID == 0 {
				p := sql.Table(project.Table).As("option_project")
				q.Join(p).On(q.C(apikeygroup.FieldProjectID), p.C(project.FieldID)).AppendSelectAs(p.C(project.FieldName), "project_name")
			}
		}).Scan(ctx, &rows)
		if err != nil {
			return nil, err
		}
		result := make([]*ConsumptionGroupOption, 0, len(rows))
		for _, row := range rows {
			name := row.Name
			if projectID == 0 {
				name += " · " + row.ProjectName
			}
			result = append(result, &ConsumptionGroupOption{ID: objects.GUID{Type: ent.TypeAPIKeyGroup, ID: row.ID}, Name: name})
		}
		return result, nil
	})
}
