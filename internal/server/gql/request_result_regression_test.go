package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestDeliveryCancellationContinuation(t *testing.T) {
	for _, state := range []string{"canceled", "completed", "failed"} {
		for _, code := range []string{"stream_delivery_canceled", "stream_delivery_timeout", "stream_delivery_failed", "stream_delivery_unconfirmed"} {
			t.Run(state+"/"+code, func(t *testing.T) {
				f := newContinuationFixture(t)
				previous := f.add(f.at, "processing", "same-turn")
				execution := previous.QueryExecutions().OnlyX(f.ctx)
				f.db.RequestExecution.UpdateOne(execution).SetStatus(requestexecution.Status(state)).ExecX(f.ctx)
				service := biz.NewRequestService(f.db, xcache.Config{Mode: xcache.ModeMemory}, nil, nil, nil, nil, nil)
				failure := &objects.RequestFailure{Stage: "delivery", Type: "gateway_error", Code: code}
				require.NoError(t, service.UpdateRequestStatusFromError(f.ctx, previous, context.Canceled, failure))
				next := f.add(f.at.Add(2*time.Second), "completed", "same-turn")
				folded := code == "stream_delivery_canceled" && state != "failed"
				want := 0
				if folded {
					want = 1
				}
				counts, err := queryRequestContinuationCounts(f.ctx, f.db, []int{next.ID})
				require.NoError(t, err)
				require.Equal(t, want, counts[next.ID])
				history, err := requestContinuationHistoryQuery(f.db, next.ID).All(f.ctx)
				require.NoError(t, err)
				require.Len(t, history, want)
				resolver := &queryResolver{&Resolver{client: f.db}}
				filter := &RequestLogFilterInput{CollapseContinuations: lo.ToPtr(true)}
				page, err := resolver.RequestLogs(f.ctx, nil, lo.ToPtr(10), nil, nil, nil, nil, filter)
				require.NoError(t, err)
				require.Equal(t, 2-want, page.TotalCount)
				statuses, err := resolver.RequestLogStatusCounts(f.ctx, nil, filter)
				require.NoError(t, err)
				for _, status := range statuses {
					if status.Status == request.StatusCanceled {
						require.Equal(t, 1-want, status.Count)
					}
				}
				require.Equal(t, failure, f.db.Request.GetX(f.ctx, previous.ID).Failure)
				require.Equal(t, state, string(f.db.RequestExecution.GetX(f.ctx, execution.ID).Status))
			})
		}
	}
}

func TestRequestSessionExecutionUsesAdoptedResult(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, loserStatus := range []string{"canceled", "failed", "completed"} {
			t.Run(loserStatus+"/legacy="+lo.Ternary(legacy, "true", "false"), func(t *testing.T) {
				f := newContinuationFixture(t)
				record := f.db.Request.Create().SetProjectID(f.project.ID).SetTraceID(f.traceID).
					SetModelID("model").SetStatus("completed").SetChannelID(1).SetRequestBody(objects.JSONRawMessage(`{}`)).SaveX(f.ctx)
				add := func(channelID int, status string) *ent.RequestExecution {
					code := 200
					if status != "completed" {
						code = 500
					}
					return f.db.RequestExecution.Create().SetProjectID(f.project.ID).SetRequestID(record.ID).
						SetChannelID(channelID).SetModelID("model").SetStatus(requestexecution.Status(status)).
						SetResponseStatusCode(code).
						SetRequestBody(objects.JSONRawMessage(`{}`)).
						SetSelection(&objects.RequestExecutionSelection{Dispatch: objects.RequestExecutionDispatchStarted}).SaveX(f.ctx)
				}
				winner := add(1, "completed")
				loser := add(2, loserStatus)
				if !legacy {
					service := biz.NewRequestService(f.db, xcache.Config{Mode: xcache.ModeMemory}, nil, nil, nil, nil, nil)
					require.NoError(t, service.AdoptRequestExecution(f.ctx, winner, false))
				}
				selected := loadRequestSessionExecutions(f.ctx, f.db, []int{record.ID})[record.ID]
				require.NotNil(t, selected)
				require.Equal(t, winner.ID, selected.ID)
				require.Equal(t, 200, *selected.ResponseStatusCode)
				states := requestChannelSessions(f.ctx, f.db, []int{winner.ID, loser.ID})
				require.Equal(t, "new", states[winner.ID])
				require.Empty(t, states[loser.ID])
				require.Equal(t, loserStatus, string(f.db.RequestExecution.GetX(f.ctx, loser.ID).Status))
			})
		}
	}
}

func TestRequestResultStatusWithoutChannelPermission(t *testing.T) {
	f := newContinuationFixture(t)
	record := f.add(f.at, "completed", "result-status")
	winner := record.QueryExecutions().OnlyX(f.ctx)
	f.db.RequestExecution.UpdateOneID(winner.ID).SetResponseStatusCode(200).ExecX(f.ctx)
	f.db.Request.UpdateOneID(record.ID).SetResultExecutionID(winner.ID).ExecX(f.ctx)
	f.db.RequestExecution.Create().SetProjectID(f.project.ID).SetRequestID(record.ID).SetChannelID(2).
		SetModelID("model").SetStatus("failed").SetResponseStatusCode(500).SetRequestBody([]byte(`{}`)).SaveX(f.ctx)
	viewer := &ent.User{ID: 1, Scopes: []string{"read_requests"}}
	ctx := contexts.WithUser(t.Context(), viewer)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: f.db}}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ requestLogs(first: 1) { edges { node { clientStatusCode resultExecution { id responseStatusCode } sessionExecution { id channel { name } } } } } }"}`)).
		WithContext(ent.NewContext(withRequestChannelSessionCache(ctx), f.db))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	var payload struct {
		Errors []any
		Data   struct {
			RequestLogs struct {
				Edges []struct {
					Node struct {
						ClientStatusCode *int
						ResultExecution  *struct {
							ID                 string
							ResponseStatusCode *int
						}
						SessionExecution *struct{ ID string }
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Empty(t, payload.Errors, response.Body.String())
	require.Len(t, payload.Data.RequestLogs.Edges, 1)
	row := payload.Data.RequestLogs.Edges[0].Node
	require.Nil(t, row.ClientStatusCode, "reading must not invent a client response code")
	require.Nil(t, row.SessionExecution, "channel metadata remains restricted")
	require.NotNil(t, row.ResultExecution)
	require.Equal(t, fmt.Sprintf("gid://axonhub/RequestExecution/%d", winner.ID), row.ResultExecution.ID)
	require.Equal(t, 200, *row.ResultExecution.ResponseStatusCode)
}
