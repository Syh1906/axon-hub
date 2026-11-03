package gql

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

// 列表与状态统计共用日志筛选，分页参数由列表单独处理。
func (r *queryResolver) requestLogQuery(ctx context.Context, filter *RequestLogFilterInput) (*ent.RequestQuery, error) {
	query := r.client.Request.Query()
	if filter == nil {
		return query, nil
	}
	if len(filter.ChannelTagsIn) > 0 {
		query.Where(biz.RequestLogTagPredicate(filter.ChannelTagsIn))
	}
	if len(filter.APIKeyGroupRefsIn) > 0 {
		p, err := biz.RequestLogGroupPredicate(filter.APIKeyGroupRefsIn)
		if err != nil {
			return nil, err
		}
		query.Where(p)
	}
	if period := filter.TimeRange; period != nil {
		location, err := r.systemService.RequestLogLocation(ctx)
		if err != nil {
			return nil, err
		}
		predicates, err := biz.RequestLogDatePredicates(period.Period, period.Start, period.End, time.Now(), location)
		if err != nil {
			return nil, err
		}
		query.Where(predicates...)
	}
	return query, nil
}
