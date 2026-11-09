package gql

import (
	"context"
	"maps"
	"slices"
	"sync"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/looplj/axonhub/internal/scopes"
)

type requestChannelSessionCacheKey struct{}

type requestChannelSessionCache struct {
	sync.RWMutex
	states map[int]string
	final  map[int]*ent.RequestExecution
}

func withRequestChannelSessionCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestChannelSessionCacheKey{}, &requestChannelSessionCache{
		states: make(map[int]string),
		final:  make(map[int]*ent.RequestExecution),
	})
}

func cacheRequestChannelSessions(ctx context.Context, client *ent.Client, requests []*ent.Request, connection bool) {
	cache, ok := ctx.Value(requestChannelSessionCacheKey{}).(*requestChannelSessionCache)
	if !ok || len(requests) == 0 {
		return
	}
	canReadChannels := authz.HasScope(ctx, scopes.ScopeReadChannels)
	op := graphql.GetOperationContext(ctx)
	fields := graphql.CollectFieldsCtx(ctx, []string{"Node", "Request", "RequestConnection"})
	if connection {
		fields = requestLogFields(op, requestLogFields(op, fields, "edges"), "node")
	}
	var aliases []string
	var loadFinal bool
	var loadFinalState bool
	for _, field := range fields {
		if field.Name == "resultExecution" {
			loadFinal = true
		}
		if field.Name == "sessionExecution" && canReadChannels {
			loadFinal = true
			loadFinalState = loadFinalState || slices.ContainsFunc(graphql.CollectFields(op, field.Selections, nil),
				func(child graphql.CollectedField) bool { return child.Name == "selection" })
		}
		if field.Name != "executions" || !canReadChannels {
			continue
		}
		executions := graphql.CollectFields(op, field.Selections, nil)
		attempts := requestLogFields(op, requestLogFields(op, executions, "edges"), "node")
		if slices.ContainsFunc(attempts, func(field graphql.CollectedField) bool { return field.Name == "selection" }) {
			aliases = append(aliases, field.Alias)
		}
	}
	var ids []int
	var finals map[int]*ent.RequestExecution
	if loadFinal {
		requestIDs := make([]int, 0, len(requests))
		for _, request := range requests {
			requestIDs = append(requestIDs, request.ID)
		}
		finals = loadRequestSessionExecutions(ctx, client, requestIDs)
		if loadFinalState {
			for _, execution := range finals {
				if execution != nil {
					ids = append(ids, execution.ID)
				}
			}
		}
	}
	for _, request := range requests {
		for _, alias := range aliases {
			executions, _ := request.NamedExecutions(alias)
			for _, execution := range executions {
				ids = append(ids, execution.ID)
			}
		}
	}
	states := requestChannelSessions(ctx, client, ids)
	cache.Lock()
	defer cache.Unlock()
	maps.Copy(cache.states, states)
	maps.Copy(cache.final, finals)
}

func requestLogFields(op *graphql.OperationContext, fields []graphql.CollectedField, name string) []graphql.CollectedField {
	var children []graphql.CollectedField
	for _, field := range fields {
		if field.Name == name {
			children = append(children, graphql.CollectFields(op, field.Selections, nil)...)
		}
	}
	return children
}

func requestChannelSession(ctx context.Context, client *ent.Client, id int) string {
	if cache, ok := ctx.Value(requestChannelSessionCacheKey{}).(*requestChannelSessionCache); ok {
		cache.RLock()
		state, found := cache.states[id]
		cache.RUnlock()
		if found {
			return state
		}
	}
	return requestChannelSessions(ctx, client, []int{id})[id]
}

func requestSessionExecution(ctx context.Context, client *ent.Client, id int) *ent.RequestExecution {
	if cache, ok := ctx.Value(requestChannelSessionCacheKey{}).(*requestChannelSessionCache); ok {
		cache.RLock()
		execution, found := cache.final[id]
		cache.RUnlock()
		if found {
			return execution
		}
	}
	return loadRequestSessionExecutions(ctx, client, []int{id})[id]
}

// Legacy requests without dispatch evidence retain the previous rule: only
// their last recorded attempt can qualify, and it must match the request channel.
func sessionDispatchEligible(dialect *sql.DialectBuilder, req *sql.SelectTable, executionID, channelID, selection string) *sql.Predicate {
	dispatch := sqljson.Path("dispatch")
	newer := sql.Table(requestexecution.Table).As("newer_attempt")
	return sql.And(sql.GT(channelID, 0), sql.Or(
		sqljson.ValueEQ(selection, objects.RequestExecutionDispatchStarted, dispatch),
		sql.And(sql.Not(sqljson.HasKey(selection, dispatch)),
			sql.ColumnsEQ(channelID, req.C(request.FieldChannelID)),
			sql.Not(sql.Exists(dialect.Select(newer.C(requestexecution.FieldID)).From(newer).Where(sql.And(
				sql.ColumnsEQ(newer.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
				sql.ColumnsGT(newer.C(requestexecution.FieldID), executionID),
			)))),
		),
	))
}

func finalSessionAttempt(db string, req *sql.SelectTable, field string) *sql.Selector {
	dialect := sql.Dialect(db)
	attempt := sql.Table(requestexecution.Table).As("session_attempt")
	req.SetDialect(db)
	attempt.SetDialect(db)
	return dialect.Select(attempt.C(field)).From(attempt).Where(sql.And(
		sql.ColumnsEQ(attempt.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
		sql.ExprP(usageanalytics.RequestResultMatchSQL(req.C, attempt.C, db)),
		sql.Or(sql.ColumnsEQ(attempt.C(requestexecution.FieldID), req.C(request.FieldResultExecutionID)),
			sessionDispatchEligible(dialect, req, attempt.C(requestexecution.FieldID),
				attempt.C(requestexecution.FieldChannelID), attempt.C(requestexecution.FieldSelection))),
	)).OrderBy(sql.Desc(attempt.C(requestexecution.FieldID))).Limit(1)
}

func sessionAttemptMatches(column string, subquery *sql.Selector) *sql.Predicate {
	return sql.P(func(b *sql.Builder) {
		b.Ident(column).WriteOp(sql.OpEQ).Wrap(func(b *sql.Builder) { b.Join(subquery) })
	})
}

func sessionRequestFinished(req *sql.SelectTable) *sql.Predicate {
	return sql.In(req.C(request.FieldStatus), request.StatusCompleted, request.StatusFailed, request.StatusCanceled)
}

func loadRequestSessionExecutions(ctx context.Context, client *ent.Client, ids []int) map[int]*ent.RequestExecution {
	finals := make(map[int]*ent.RequestExecution, len(ids))
	for _, id := range ids {
		finals[id] = nil
	}
	query := client.RequestExecution.Query().Where(requestexecution.RequestIDIn(ids...), func(q *sql.Selector) {
		req := sql.Table(request.Table).As("session_request")
		q.Join(req).On(q.C(requestexecution.FieldRequestID), req.C(request.FieldID)).
			Where(sql.And(sessionRequestFinished(req),
				sessionAttemptMatches(q.C(requestexecution.FieldID), finalSessionAttempt(q.Dialect(), req, requestexecution.FieldID))))
	}).Select(requestexecution.FieldRequestID, requestexecution.FieldChannelID, requestexecution.FieldSelection,
		requestexecution.FieldCostSnapshot, requestexecution.FieldResponseStatusCode)
	if authz.HasScope(ctx, scopes.ScopeReadChannels) {
		query.WithChannel()
	}
	executions, err := query.All(ctx)
	if err != nil {
		log.Warn(ctx, "Failed to read final session attempts", log.Cause(err))
		return finals
	}
	for _, execution := range executions {
		finals[execution.RequestID] = execution
	}
	return finals
}

// Each finished request contributes its last dispatched attempt, even if it failed.
func requestChannelSessions(ctx context.Context, client *ent.Client, ids []int) map[int]string {
	states := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return states
	}
	var rows []struct {
		ID              int  `json:"id"`
		Channel         int  `json:"channel"`
		PreviousChannel *int `json:"previous_channel"`
		UsedBefore      bool `json:"used_before"`
	}
	err := client.RequestExecution.Query().Where(requestexecution.IDIn(ids...)).Modify(func(q *sql.Selector) {
		requestSessionHistoryQuery(q, ids)
	}).Scan(ctx, &rows)
	for _, id := range ids {
		states[id] = ""
	}
	if err != nil {
		log.Warn(ctx, "Failed to read request channel session history", log.Cause(err))
		return states
	}
	for _, row := range rows {
		switch {
		case row.PreviousChannel != nil && *row.PreviousChannel == row.Channel:
			states[row.ID] = "reused"
		case row.UsedBefore:
			states[row.ID] = "returned"
		default:
			states[row.ID] = "new"
		}
	}
	return states
}
