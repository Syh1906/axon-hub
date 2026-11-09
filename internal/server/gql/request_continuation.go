package gql

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

const (
	continuationGapMilliseconds = 10_000
	continuationChainTable      = "request_continuation_chain"
)

// Adjacent means the next/previous request in this explicit client turn, not
// the next canceled row. Completed tool calls and overlapping requests break
// a chain. Tenant and caller constraints remain even though trace IDs are global.
func adjacentContinuationRequest(db string, current *sql.SelectTable, forward bool) *sql.Selector {
	neighbor := sql.Table(request.Table).As("continuation_neighbor")
	query := sql.Dialect(db).Select(neighbor.C(request.FieldID)).From(neighbor).Where(sql.And(
		sql.ColumnsEQ(neighbor.C(request.FieldProjectID), current.C(request.FieldProjectID)),
		sql.ColumnsEQ(neighbor.C(request.FieldAPIKeyID), current.C(request.FieldAPIKeyID)),
		sql.ColumnsEQ(neighbor.C(request.FieldTraceID), current.C(request.FieldTraceID)),
		sql.ColumnsEQ(neighbor.C(request.FieldContinuationKey), current.C(request.FieldContinuationKey)),
	))
	created, id := neighbor.C(request.FieldCreatedAt), neighbor.C(request.FieldID)
	currentCreated, currentID := current.C(request.FieldCreatedAt), current.C(request.FieldID)
	if forward {
		query.Where(sql.Or(sql.ColumnsGT(created, currentCreated), sql.And(sql.ColumnsEQ(created, currentCreated), sql.ColumnsGT(id, currentID)))).OrderBy(created, id)
	} else {
		query.Where(sql.Or(sql.ColumnsLT(created, currentCreated), sql.And(sql.ColumnsEQ(created, currentCreated), sql.ColumnsLT(id, currentID)))).OrderBy(sql.Desc(created), sql.Desc(id))
	}
	return query.Limit(1)
}

func joinContinuationFinalExecution(q *sql.Selector, previous *sql.SelectTable) *sql.SelectTable {
	execution := sql.Table(requestexecution.Table).As("continuation_execution")
	last := sql.Table(requestexecution.Table).As("continuation_last_execution")
	finalID := sql.Dialect(q.Dialect()).Select(sql.Max(last.C(requestexecution.FieldID))).From(last).
		Where(sql.And(sql.ColumnsEQ(last.C(requestexecution.FieldRequestID), previous.C(request.FieldID)),
			sql.ExprP(usageanalytics.RequestResultMatchSQL(previous.C, last.C, q.Dialect()))))
	q.Join(execution).OnP(sql.EQ(execution.C(requestexecution.FieldID), finalID))
	return execution
}

// A canceled request is folded only after a measured execution and a closely
// following request. Explicit client delivery cancellation may follow an already
// completed upstream execution; other failures must remain independent.
// Execution creation is slightly later than
// the performance clock's start; ambiguous overlap stays visible independently.
func validRequestContinuation(db string, previous, next, execution *sql.SelectTable) *sql.Predicate {
	start, finish := execution.C(requestexecution.FieldCreatedAt), next.C(request.FieldCreatedAt)
	var elapsed string
	switch db {
	case dialect.Postgres:
		elapsed = fmt.Sprintf("EXTRACT(EPOCH FROM (%s - %s)) * 1000.0", finish, start)
	case dialect.MySQL:
		elapsed = fmt.Sprintf("TIMESTAMPDIFF(MICROSECOND, %s, %s) / 1000.0", start, finish)
	default:
		// The SQLite driver stores UTC Go timestamps with a trailing zone name.
		// Remove that suffix while retaining fractional seconds for short gaps.
		julian := func(column string) string {
			return fmt.Sprintf("julianday(substr(%s, 1, 19) || substr(%s, 20, instr(substr(%s, 20) || ' ', ' ') - 1))", column, column, column)
		}
		elapsed = fmt.Sprintf("ROUND((%s - %s) * 86400000.0)", julian(finish), julian(start))
	}
	latency := execution.C(requestexecution.FieldMetricsLatencyMs)
	return sql.And(
		sql.EQ(previous.C(request.FieldStatus), request.StatusCanceled),
		sql.Or(
			sql.And(sql.IsNull(previous.C(request.FieldFailure)), sql.EQ(execution.C(requestexecution.FieldStatus), requestexecution.StatusCanceled)),
			sql.And(
				sqljson.ValueEQ(previous.C(request.FieldFailure), "delivery", sqljson.Path("stage")),
				sqljson.ValueEQ(previous.C(request.FieldFailure), "gateway_error", sqljson.Path("type")),
				sqljson.ValueEQ(previous.C(request.FieldFailure), "stream_delivery_canceled", sqljson.Path("code")),
				sql.In(execution.C(requestexecution.FieldStatus), requestexecution.StatusCanceled, requestexecution.StatusCompleted),
			),
		),
		sql.GT(previous.C(request.FieldTraceID), 0), sql.GT(previous.C(request.FieldAPIKeyID), 0),
		sql.NEQ(previous.C(request.FieldContinuationKey), ""),
		sql.EQ(previous.C(request.FieldSource), request.SourceAPI), sql.EQ(previous.C(request.FieldStream), true),
		sql.EQ(next.C(request.FieldSource), request.SourceAPI), sql.EQ(next.C(request.FieldStream), true),
		sql.ColumnsEQ(execution.C(requestexecution.FieldProjectID), previous.C(request.FieldProjectID)),
		sql.GT(latency, 0),
		sql.ExprP("("+elapsed+") >= "+latency),
		sql.P(func(b *sql.Builder) {
			b.WriteString("(" + elapsed + ") <= " + latency + " + ").Arg(continuationGapMilliseconds)
		}),
	)
}

func excludeContinuedRequests(q *sql.Selector) {
	previous := q.Table()
	next := sql.Table(request.Table).As("continuation_next")
	query := sql.Dialect(q.Dialect()).Select(next.C(request.FieldID)).From(next).
		Where(sql.EQ(next.C(request.FieldID), adjacentContinuationRequest(q.Dialect(), previous, true)))
	execution := joinContinuationFinalExecution(query, previous)
	query.Where(validRequestContinuation(q.Dialect(), previous, next, execution))
	q.Where(sql.Not(sql.Exists(query)))
}

// Roots have already passed the caller's Ent authorization. Every recursive
// step stays within their project, API key, trace and client turn. The outer
// Ent query applies authorization again when returning originals or counts.
func requestContinuationChain(db string, ids []int) *sql.WithBuilder {
	root := sql.Table(request.Table).As("continuation_root")
	anchor := sql.Dialect(db).Select(sql.As(root.C(request.FieldID), "root_id"), sql.As(root.C(request.FieldID), "member_id")).
		From(root).Where(sql.InInts(root.C(request.FieldID), ids...))
	chain := sql.Table(continuationChainTable)
	next := sql.Table(request.Table).As("continuation_current")
	previous := sql.Table(request.Table).As("continuation_previous")
	step := sql.Dialect(db).Select(chain.C("root_id"), previous.C(request.FieldID)).From(chain).
		Join(next).On(chain.C("member_id"), next.C(request.FieldID)).
		Join(previous).OnP(sql.EQ(previous.C(request.FieldID), adjacentContinuationRequest(db, next, false)))
	execution := joinContinuationFinalExecution(step, previous)
	step.Where(validRequestContinuation(db, previous, next, execution))
	w := sql.WithRecursive(continuationChainTable, "root_id", "member_id").As(anchor.UnionAll(step))
	w.SetDialect(db)
	return w
}

func queryRequestContinuationCounts(ctx context.Context, client *ent.Client, ids []int) (map[int]int, error) {
	counts := make(map[int]int, len(ids))
	for _, id := range ids {
		counts[id] = 0
	}
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		ID    int `json:"id"`
		Count int `json:"count"`
	}
	err := client.Request.Query().Where(request.IDIn(ids...)).Modify(func(q *sql.Selector) {
		chain := sql.Table(continuationChainTable)
		q.Prefix(requestContinuationChain(q.Dialect(), ids)).
			Join(chain).On(q.C(request.FieldID), chain.C("root_id")).
			Where(sql.ColumnsNEQ(chain.C("member_id"), chain.C("root_id"))).
			Select(q.C(request.FieldID), sql.As(sql.Count("*"), "count")).GroupBy(q.C(request.FieldID))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ID] = row.Count
	}
	return counts, nil
}

func requestContinuationHistoryQuery(client *ent.Client, id int) *ent.RequestQuery {
	return client.Request.Query().Where(func(q *sql.Selector) {
		chain := sql.Table(continuationChainTable)
		q.Prefix(requestContinuationChain(q.Dialect(), []int{id})).Where(sql.In(q.C(request.FieldID),
			sql.Dialect(q.Dialect()).Select(chain.C("member_id")).From(chain).
				Where(sql.ColumnsNEQ(chain.C("member_id"), chain.C("root_id")))))
	})
}

type requestContinuationCacheKey struct{}
type requestContinuationCache struct {
	sync.RWMutex

	counts map[int]int
}

func withRequestContinuationCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestContinuationCacheKey{}, &requestContinuationCache{counts: make(map[int]int)})
}

func cacheRequestContinuationCounts(ctx context.Context, client *ent.Client, records []*ent.Request) error {
	cache, ok := ctx.Value(requestContinuationCacheKey{}).(*requestContinuationCache)
	if !ok || len(records) == 0 {
		return nil
	}
	op := graphql.GetOperationContext(ctx)
	fields := graphql.CollectFieldsCtx(ctx, []string{"RequestConnection"})
	fields = requestLogFields(op, requestLogFields(op, fields, "edges"), "node")
	if !slices.ContainsFunc(fields, func(field graphql.CollectedField) bool { return field.Name == "continuationCount" }) {
		return nil
	}
	ids := make([]int, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	counts, err := queryRequestContinuationCounts(ctx, client, ids)
	if err != nil {
		return err
	}
	cache.Lock()
	defer cache.Unlock()
	maps.Copy(cache.counts, counts)
	return nil
}

func requestContinuationCount(ctx context.Context, client *ent.Client, id int) (int, error) {
	if cache, ok := ctx.Value(requestContinuationCacheKey{}).(*requestContinuationCache); ok {
		cache.RLock()
		count, found := cache.counts[id]
		cache.RUnlock()
		if found {
			return count, nil
		}
	}
	counts, err := queryRequestContinuationCounts(ctx, client, []int{id})
	return counts[id], err
}
