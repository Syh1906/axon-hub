package ent

import (
	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/ent/request"
)

// Ent 遇到扩展字段时会读取整行。列表计算字段需要请求元数据，
// 只有查询相应详情时才需要头部、正文、分块或选路记录。
func (q *RequestQuery) selectRequestListFields(op *graphql.OperationContext, node graphql.CollectedField) {
	detailFields := map[string]string{
		request.FieldRequestBody:     "requestBody",
		request.FieldResponseBody:    "responseBody",
		request.FieldResponseChunks:  "responseChunks",
		request.FieldRequestHeaders:  "requestHeaders",
		request.FieldResponseHeaders: "responseHeaders",
		request.FieldRoutingDecision: "routingDecision",
	}
	selected := make(map[string]bool)
	for _, field := range graphql.CollectFields(op, node.Selections, requestImplementors) {
		selected[field.Name] = true
	}
	for _, column := range request.Columns {
		if name, detail := detailFields[column]; !detail || selected[name] {
			q.ctx.Fields = append(q.ctx.Fields, column)
		}
	}
}
