package gql

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/99designs/gqlgen/client"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

type continuationFixture struct {
	db      *ent.Client
	ctx     context.Context
	project *ent.Project
	traceID int
	at      time.Time
}

func newContinuationFixture(t *testing.T) *continuationFixture {
	db := enttest.NewEntClient(t, "sqlite3", fmt.Sprintf("file:continuation-%d?mode=memory&_fk=0", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := db.Project.Create().SetName("continuation").SaveX(ctx)
	trace := db.Trace.Create().SetProjectID(project.ID).SetTraceID("session").SaveX(ctx)
	return &continuationFixture{db: db, ctx: ctx, project: project, traceID: trace.ID,
		at: time.Date(2026, 11, 7, 23, 59, 40, 0, time.UTC)}
}

func (f *continuationFixture) add(at time.Time, status, identity string) *ent.Request {
	r := f.db.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(1).SetTraceID(f.traceID).
		SetModelID("model").SetSource("api").SetStream(true).SetStatus(request.Status(status)).
		SetContinuationKey(identity).SetRequestBody(objects.JSONRawMessage(`{}`)).SetCreatedAt(at).SaveX(f.ctx)
	f.db.RequestExecution.Create().SetProjectID(f.project.ID).SetRequestID(r.ID).SetChannelID(1).
		SetModelID("model").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus(requestexecution.Status(status)).
		SetCreatedAt(at).SetMetricsLatencyMs(1000).SaveX(f.ctx)
	return r
}

func TestRequestContinuationGroupsBeforePagination(t *testing.T) {
	f := newContinuationFixture(t)
	first := f.add(f.at, "canceled", "same-turn")
	second := f.add(f.at.Add(2*time.Second), "canceled", "same-turn")
	latest := f.add(f.at.Add(4*time.Second), "completed", "same-turn")
	standalone := f.add(f.at.Add(6*time.Second), "completed", "same-turn")
	resolver := &queryResolver{&Resolver{client: f.db}}
	filter := &RequestLogFilterInput{CollapseContinuations: lo.ToPtr(true)}
	page, err := resolver.RequestLogs(f.ctx, nil, lo.ToPtr(1), nil, nil,
		&ent.RequestOrder{Direction: entgql.OrderDirectionDesc, Field: ent.RequestOrderFieldCreatedAt}, nil, filter)
	require.NoError(t, err)
	require.Equal(t, 2, page.TotalCount)
	require.Equal(t, standalone.ID, page.Edges[0].Node.ID)
	next, err := resolver.RequestLogs(f.ctx, page.PageInfo.EndCursor, lo.ToPtr(1), nil, nil,
		&ent.RequestOrder{Direction: entgql.OrderDirectionDesc, Field: ent.RequestOrderFieldCreatedAt}, nil, filter)
	require.NoError(t, err)
	require.Equal(t, latest.ID, next.Edges[0].Node.ID)
	counts, err := queryRequestContinuationCounts(f.ctx, f.db, []int{latest.ID, standalone.ID})
	require.NoError(t, err)
	require.Equal(t, map[int]int{latest.ID: 2, standalone.ID: 0}, counts)
	history, err := requestContinuationHistoryQuery(f.db, latest.ID).Order(ent.Desc(request.FieldID)).All(f.ctx)
	require.NoError(t, err)
	require.Equal(t, []int{second.ID, first.ID}, []int{history[0].ID, history[1].ID})
	statuses, err := resolver.RequestLogStatusCounts(f.ctx, nil, filter)
	require.NoError(t, err)
	for _, status := range statuses {
		if status.Status == request.StatusCanceled {
			require.Zero(t, status.Count)
		}
		if status.Status == request.StatusCompleted {
			require.Equal(t, 2, status.Count)
		}
	}
	// Original requests remain independently readable and unmodified.
	require.Equal(t, request.StatusCanceled, f.db.Request.GetX(f.ctx, first.ID).Status)
}

func TestRequestContinuationRejectsAmbiguousRelations(t *testing.T) {
	for _, scenario := range []string{"missing-key", "other-turn", "other-project", "other-caller", "other-trace", "overlap", "late", "final-not-canceled", "failure", "missing-timing", "non-stream"} {
		t.Run(scenario, func(t *testing.T) {
			f := newContinuationFixture(t)
			previous := f.add(f.at, "canceled", "same-turn")
			at := f.at.Add(2 * time.Second)
			if scenario == "overlap" {
				at = f.at.Add(500 * time.Millisecond)
			}
			if scenario == "late" {
				at = f.at.Add(12 * time.Second)
			}
			next := f.add(at, "completed", "same-turn")
			switch scenario {
			case "missing-key":
				f.db.Request.UpdateOne(previous).ClearContinuationKey().SaveX(f.ctx)
			case "other-turn":
				f.db.Request.UpdateOne(next).SetContinuationKey("other").SaveX(f.ctx)
			case "other-project":
				// Immutable scope fields are set by the fixture through a new row.
				f.db.Request.DeleteOne(next).ExecX(f.ctx)
				f.project = f.db.Project.Create().SetName("other").SaveX(f.ctx)
				next = f.add(at, "completed", "same-turn")
			case "other-caller", "other-trace", "non-stream":
				f.db.Request.DeleteOne(next).ExecX(f.ctx)
				create := f.db.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(1).SetTraceID(f.traceID).
					SetModelID("model").SetSource("api").SetStream(true).SetStatus("completed").SetContinuationKey("same-turn").SetRequestBody(objects.JSONRawMessage(`{}`)).SetCreatedAt(at)
				if scenario == "other-caller" {
					create.SetAPIKeyID(2)
				}
				if scenario == "other-trace" {
					create.SetTraceID(f.traceID + 1)
				}
				if scenario == "non-stream" {
					create.SetStream(false)
				}
				next = create.SaveX(f.ctx)
			case "final-not-canceled":
				f.db.RequestExecution.Create().SetRequestID(previous.ID).SetProjectID(f.project.ID).SetChannelID(1).
					SetModelID("model").SetStatus("completed").SetRequestBody(objects.JSONRawMessage(`{}`)).SetCreatedAt(f.at).SetMetricsLatencyMs(1000).SaveX(f.ctx)
			case "failure":
				f.db.Request.UpdateOne(previous).SetFailure(&objects.RequestFailure{Code: "request_deadline_exceeded"}).SaveX(f.ctx)
			case "missing-timing":
				f.db.RequestExecution.Update().ClearMetricsLatencyMs().ExecX(f.ctx)
			}
			counts, err := queryRequestContinuationCounts(f.ctx, f.db, []int{next.ID})
			require.NoError(t, err)
			require.Zero(t, counts[next.ID])
			rows, err := f.db.Request.Query().Where(excludeContinuedRequests).All(f.ctx)
			require.NoError(t, err)
			require.Len(t, rows, 2)
		})
	}
}

func TestRequestContinuationGraphQLPreservesHistoryAccess(t *testing.T) {
	f := newContinuationFixture(t)
	previous := f.add(f.at, "canceled", "same-turn")
	latest := f.add(f.at.Add(2*time.Second), "failed", "same-turn")
	ctx := contexts.WithUser(f.ctx, &ent.User{ID: 1, IsOwner: true})
	h := NewGraphqlHandlers(Dependencies{Ent: f.db, SystemService: biz.NewSystemService(biz.SystemServiceParams{Ent: f.db})})
	c := client.New(h.Graphql, func(r *client.Request) { r.HTTP = r.HTTP.WithContext(ctx) })
	var result struct {
		RequestContinuations struct {
			Edges []struct {
				Node struct {
					ID     string
					Status string
				}
			}
		}
	}
	err := c.Post(`query($id:ID!) { requestContinuations(requestID:$id,first:20) { edges { node { id status } } } }`, &result,
		client.Var("id", fmt.Sprintf("gid://axonhub/Request/%d", latest.ID)))
	require.NoError(t, err)
	require.Len(t, result.RequestContinuations.Edges, 1)
	require.Equal(t, fmt.Sprintf("gid://axonhub/Request/%d", previous.ID), result.RequestContinuations.Edges[0].Node.ID)
	require.Equal(t, "canceled", result.RequestContinuations.Edges[0].Node.Status)
}

func TestRequestContinuationCrossDayFiltersAndHistoryPermissions(t *testing.T) {
	f := newContinuationFixture(t)
	f.at = time.Date(2026, 11, 7, 23, 59, 59, 123000000, time.UTC)
	previous := f.add(f.at, "canceled", "same-turn")
	latest := f.add(f.at.Add(2*time.Second), "failed", "same-turn")
	resolver := &queryResolver{&Resolver{client: f.db}}
	filter := &RequestLogFilterInput{CollapseContinuations: lo.ToPtr(true)}
	midnight := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	priorDay, err := resolver.RequestLogs(f.ctx, nil, lo.ToPtr(20), nil, nil, nil,
		&ent.RequestWhereInput{CreatedAtLT: &midnight}, filter)
	require.NoError(t, err)
	require.Empty(t, priorDay.Edges, "a chain belongs to the representative's date")
	filtered, err := resolver.RequestLogs(f.ctx, nil, lo.ToPtr(20), nil, nil, nil,
		&ent.RequestWhereInput{StatusIn: []request.Status{request.StatusFailed}, CreatedAtGTE: &midnight}, filter)
	require.NoError(t, err)
	require.Len(t, filtered.Edges, 1)
	require.Equal(t, latest.ID, filtered.Edges[0].Node.ID)

	viewer := &ent.User{ID: 1, Edges: ent.UserEdges{ProjectUsers: []*ent.UserProject{{ProjectID: f.project.ID, Scopes: []string{"read_requests"}}}}}
	ctx := contexts.WithProjectID(contexts.WithUser(context.Background(), viewer), f.project.ID)
	ctx, err = authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	history, err := resolver.RequestContinuations(ctx, objects.GUID{Type: ent.TypeRequest, ID: latest.ID}, lo.ToPtr(1), nil)
	require.NoError(t, err)
	require.Equal(t, previous.ID, history.Edges[0].Node.ID)
	original, err := f.db.Request.Get(ctx, previous.ID)
	require.NoError(t, err)
	require.Equal(t, request.StatusCanceled, original.Status)
	f.project = f.db.Project.Create().SetName("other-project").SaveX(f.ctx)
	other := f.add(f.at, "completed", "same-turn")
	_, err = resolver.RequestContinuations(ctx, objects.GUID{Type: ent.TypeRequest, ID: other.ID}, lo.ToPtr(20), nil)
	require.Error(t, err, "unauthorized representatives must not expose their history")
}

func TestRequestContinuationGraphQLCountsAreBatched(t *testing.T) {
	f := newContinuationFixture(t)
	f.add(f.at, "canceled", "same-turn")
	f.add(f.at.Add(2*time.Second), "completed", "same-turn")
	for i := 0; i < 24; i++ {
		f.add(f.at.Add(time.Duration(i+4)*time.Second), "completed", fmt.Sprintf("turn-%d", i))
	}
	var queries atomic.Int32
	db := ent.NewClient(ent.Driver(f.db.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		if strings.Contains(fmt.Sprint(args...), "WITH RECURSIVE") {
			queries.Add(1)
		}
	}))
	ctx := contexts.WithUser(f.ctx, &ent.User{ID: 1, IsOwner: true})
	h := NewGraphqlHandlers(Dependencies{Ent: db, SystemService: biz.NewSystemService(biz.SystemServiceParams{Ent: db})})
	c := client.New(h.Graphql, func(r *client.Request) { r.HTTP = r.HTTP.WithContext(ctx) })
	var result struct {
		RequestLogs struct {
			Edges []struct {
				Node struct {
					ID    string
					Count int
				}
			}
		}
	}
	err := c.Post(`{ requestLogs(first:50,filter:{collapseContinuations:true}) { edges { node { id count:continuationCount } } } }`, &result)
	require.NoError(t, err)
	require.Len(t, result.RequestLogs.Edges, 25)
	total := 0
	for _, edge := range result.RequestLogs.Edges {
		total += edge.Node.Count
	}
	require.Equal(t, 1, total)
	require.EqualValues(t, 1, queries.Load(), "one count query per page, including aliased fields")
	queries.Store(0)
	err = c.Post(`{ requestLogs(first:50) { edges { node { id } } } }`, &result)
	require.NoError(t, err)
	require.Zero(t, queries.Load(), "no continuation query when the field is not selected")
}

func TestRequestContinuationDialectQueries(t *testing.T) {
	for _, db := range []string{dialect.Postgres, dialect.MySQL, dialect.SQLite} {
		t.Run(db, func(t *testing.T) {
			q := sql.Dialect(db).Select().From(sql.Table(request.Table))
			excludeContinuedRequests(q)
			query, args := q.Query()
			require.NotEmpty(t, args)
			chain, _ := requestContinuationChain(db, []int{1, 2}).Query()
			for _, statement := range []string{query, chain} {
				switch db {
				case dialect.Postgres:
					require.Contains(t, statement, "EXTRACT(EPOCH FROM")
					require.NotContains(t, statement, "`")
				case dialect.MySQL:
					require.Contains(t, statement, "TIMESTAMPDIFF(MICROSECOND")
				case dialect.SQLite:
					require.Contains(t, statement, "julianday(substr(")
				}
			}
		})
	}
}

func TestRequestContinuationLookupUsesTurnIndex(t *testing.T) {
	f := newContinuationFixture(t)
	first := f.add(f.at, "canceled", "same-turn")
	f.add(f.at.Add(2*time.Second), "completed", "same-turn")
	q := sql.Dialect(dialect.SQLite).Select().From(sql.Table(request.Table)).Where(sql.EQ(request.FieldID, first.ID))
	excludeContinuedRequests(q)
	query, args := q.Query()
	var rows sql.Rows
	require.NoError(t, f.db.Driver().Query(f.ctx, "EXPLAIN QUERY PLAN "+query, args, &rows))
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		plan = append(plan, detail)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, strings.Join(plan, "\n"), "requests_by_trace_continuation_created_at")
	t.Log(strings.Join(plan, "\n"))
}
