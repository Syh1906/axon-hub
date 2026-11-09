package gql

import (
	"context"
	"maps"
	"slices"
	"sync"

	"entgo.io/ent/dialect/sql"
	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
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

func finalSessionAttempt(db string, req *sql.SelectTable, field string) *sql.Selector {
	return biz.FinalRequestChannelAttempt(db, req, field)
}

func sessionAttemptMatches(column string, subquery *sql.Selector) *sql.Predicate {
	return biz.RequestSessionAttemptMatches(column, subquery)
}

func sessionRequestFinished(req *sql.SelectTable) *sql.Predicate {
	return biz.RequestSessionFinished(req)
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

func requestChannelSessions(ctx context.Context, client *ent.Client, ids []int) map[int]string {
	return biz.RequestChannelSessions(ctx, client, ids)
}
