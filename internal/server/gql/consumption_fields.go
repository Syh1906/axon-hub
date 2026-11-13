package gql

import (
	"context"
	"slices"

	"github.com/99designs/gqlgen/graphql"
	"github.com/looplj/axonhub/internal/server/biz"
)

// 检查展开后的字段（包含 fragment 和 alias），而不是按页面维度猜测调用方需要什么。
func consumptionFields(ctx context.Context, input biz.ConsumptionFilter) biz.ConsumptionFilter {
	op := graphql.GetOperationContext(ctx)
	var needsPerformance func([]graphql.CollectedField) bool
	needsPerformance = func(fields []graphql.CollectedField) bool {
		for _, field := range fields {
			if slices.Contains([]string{"successRate", "avgFirstTokenMs", "avgTokensPerSecond", "speedSampleCount", "successCount", "outcomeCount", "firstTokenSum", "firstTokenCount", "speedSum"}, field.Name) {
				return true
			}
			if needsPerformance(graphql.CollectFields(op, field.Selections, nil)) {
				return true
			}
		}
		return false
	}
	input.OmitPerformance = !needsPerformance(graphql.CollectFieldsCtx(ctx, nil))
	return input
}
