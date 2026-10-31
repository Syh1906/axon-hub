package gql

import (
	"context"
	"fmt"

	"entgo.io/contrib/entgql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
)

// 页码跳转只定位目标页前一条记录的游标，不取回途经页面的完整请求。
// 后续仍走 Ent 的分页与权限过滤，totalCount 不受 offset 影响。
func paginateRequestLogOffset(ctx context.Context, query *ent.RequestQuery, offset int, first *int, orderBy *ent.RequestOrder, where *ent.RequestWhereInput) (*ent.RequestConnection, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset must not be negative")
	}
	query, err := where.Filter(query)
	if err != nil {
		return nil, err
	}
	if offset == 0 {
		return query.Paginate(ctx, nil, first, nil, nil, ent.WithRequestOrder(orderBy))
	}
	order := ent.DefaultRequestOrder
	if orderBy != nil {
		order = orderBy
	}
	fields := []string{request.FieldID}
	byUpdatedAt := order.Field != nil && order.Field.String() == "UPDATED_AT"
	if byUpdatedAt {
		fields = []string{request.FieldUpdatedAt, request.FieldID}
	}
	boundaryQuery := query.Clone().Select(fields...).Offset(offset - 1)
	if order.Direction == entgql.OrderDirectionDesc {
		boundaryQuery.Order(ent.Desc(fields...))
	} else {
		boundaryQuery.Order(ent.Asc(fields...))
	}
	boundary, err := boundaryQuery.First(ctx)
	if ent.IsNotFound(err) {
		total, countErr := query.Count(ctx)
		if countErr != nil {
			return nil, countErr
		}
		return &ent.RequestConnection{Edges: []*ent.RequestEdge{}, TotalCount: total, PageInfo: ent.PageInfo{HasPreviousPage: total > 0}}, nil
	}
	if err != nil {
		return nil, err
	}
	cursor := &entgql.Cursor[int]{ID: boundary.ID}
	if byUpdatedAt {
		cursor.Value = boundary.UpdatedAt
	}
	return query.Paginate(ctx, cursor, first, nil, nil, ent.WithRequestOrder(orderBy))
}
