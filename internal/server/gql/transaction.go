package gql

import (
	"context"

	"entgo.io/contrib/entgql"
	"github.com/99designs/gqlgen/graphql"
)

// 请求读取或解码失败时尚未创建 operation context，此时不能进入事务处理。
type transactioner struct {
	entgql.Transactioner
}

func (t transactioner) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	if !graphql.HasOperationContext(ctx) {
		return next(ctx)
	}
	return t.Transactioner.InterceptResponse(ctx, next)
}
