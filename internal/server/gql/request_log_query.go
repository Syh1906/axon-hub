package gql

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
)

// 列表与状态统计共用日志筛选，分页参数由列表单独处理。
func (r *queryResolver) requestLogQuery(ctx context.Context, filter *RequestLogFilterInput) (*ent.RequestQuery, error) {
	query := r.client.Request.Query()
	if filter == nil {
		return query, nil
	}
	if len(filter.ChannelTagsIn) > 0 {
		if authz.HasScope(ctx, scopes.ScopeReadChannels) {
			query.Where(func(q *sql.Selector) {
				ch := sql.Table(channel.Table).As("log_channel")
				dialect := sql.Dialect(q.Dialect())
				tags := make([]*sql.Predicate, 0, len(filter.ChannelTagsIn))
				for _, tag := range filter.ChannelTagsIn {
					tags = append(tags, sqljson.ValueContains(ch.C(channel.FieldTags), tag))
				}
				q.Where(sql.Exists(dialect.Select(ch.C(channel.FieldID)).From(ch).Where(sql.And(
					sql.Or(tags...), sql.P(func(b *sql.Builder) {
						b.Ident(ch.C(channel.FieldID)).WriteOp(sql.OpEQ)
						writeRequestLogChannel(b, q)
					}),
				))))
			})
		} else {
			query.Where(biz.RequestLogTagPredicate(filter.ChannelTagsIn))
		}
	}
	if len(filter.ChannelIDsIn) > 0 {
		if err := authz.RequireScope(ctx, scopes.ScopeReadChannels); err != nil {
			return nil, err
		}
		query.Where(func(q *sql.Selector) {
			q.Where(sql.P(func(b *sql.Builder) {
				writeRequestLogChannel(b, q)
				b.WriteOp(sql.OpIn).Wrap(func(b *sql.Builder) {
					for i, id := range filter.ChannelIDsIn {
						if i > 0 {
							b.Comma()
						}
						b.Arg(id.ID)
					}
				})
			}))
		})
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

func writeRequestLogChannel(b *sql.Builder, q *sql.Selector) {
	req := q.Table()
	attempt := finalSessionAttempt(sql.Dialect(q.Dialect()), req, requestexecution.FieldChannelID).
		Where(sessionRequestFinished(req))
	b.WriteString("COALESCE").Wrap(func(b *sql.Builder) {
		b.Wrap(func(b *sql.Builder) { b.Join(attempt) }).Comma().Ident(q.C(request.FieldChannelID))
	})
}
