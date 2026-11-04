package gql

import (
	"context"
	"encoding/json"
	"entgo.io/contrib/entgql"
	"fmt"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRequestSelectionRequiresChannelPermission(t *testing.T) {
	resolver := &requestExecutionResolver{}
	record := &ent.RequestExecution{Selection: &objects.RequestExecutionSelection{ChannelName: "private-channel", KeyRemark: "private-remark"}}
	viewer := &ent.User{ID: 1, Scopes: []string{"read_requests"}}
	ctx := contexts.WithUser(context.Background(), viewer)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	value, err := resolver.Selection(ctx, record)
	require.NoError(t, err)
	require.Nil(t, value)
	viewer.Scopes = append(viewer.Scopes, "read_channels")
	value, err = resolver.Selection(ctx, record)
	require.NoError(t, err)
	require.Equal(t, record.Selection, value)
}

func TestRequestLogChannelSessionHistory(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_log_channel_session?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("会话标记").SaveX(ctx)
	thread := client.Thread.Create().SetProjectID(project.ID).SetThreadID("session").SaveX(ctx)
	firstTrace := client.Trace.Create().SetProjectID(project.ID).SetTraceID("initial").SetThreadID(thread.ID).SaveX(ctx)
	continuedTrace := client.Trace.Create().SetProjectID(project.ID).SetTraceID("continued").SetThreadID(thread.ID).SaveX(ctx)
	otherTrace := client.Trace.Create().SetProjectID(project.ID).SetTraceID("separate").SaveX(ctx)
	newRequest := func(traceID, channelID int) *ent.Request {
		return client.Request.Create().SetProjectID(project.ID).SetTraceID(traceID).SetChannelID(channelID).SetModelID("session-model").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("completed").SaveX(ctx)
	}
	addExecution := func(req *ent.Request, channelID int, state string) *ent.RequestExecution {
		return client.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(channelID).SetModelID(req.ModelID).
			SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("completed").
			SetSelection(&objects.RequestExecutionSelection{SessionState: state, ChannelName: "recorded"}).SaveX(ctx)
	}
	first := addExecution(newRequest(firstTrace.ID, 1), 1, "reused")
	switchTo := newRequest(continuedTrace.ID, 2)
	intermediate := addExecution(switchTo, 3, "unrecorded")
	client.RequestExecution.UpdateOne(intermediate).SetStatus("failed").SaveX(ctx)
	second := addExecution(switchTo, 2, "switched")
	third := addExecution(newRequest(continuedTrace.ID, 3), 3, "reused")
	backRequest := newRequest(continuedTrace.ID, 1)
	client.Request.UpdateOne(backRequest).SetStatus("failed").SaveX(ctx)
	back := client.RequestExecution.Create().SetRequestID(backRequest.ID).SetChannelID(1).SetModelID(backRequest.ModelID).
		SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("failed").SaveX(ctx)
	withoutChannel := newRequest(continuedTrace.ID, 0)
	client.Request.UpdateOne(withoutChannel).SetStatus("failed").SaveX(ctx)
	// Retention can remove executions before it removes their request row.
	newRequest(continuedTrace.ID, 2)
	inProgress := newRequest(continuedTrace.ID, 2)
	inProgressExecution := addExecution(inProgress, 2, "unrecorded")
	client.Request.UpdateOne(inProgress).SetStatus("processing").SaveX(ctx)
	client.RequestExecution.UpdateOne(inProgressExecution).SetStatus("processing").SaveX(ctx)
	same := addExecution(newRequest(continuedTrace.ID, 1), 1, "unrecorded")
	other := addExecution(newRequest(otherTrace.ID, 1), 1, "reused")
	otherAgain := addExecution(newRequest(otherTrace.ID, 1), 1, "disabled")
	standalone := addExecution(newRequest(0, 1), 1, "unrecorded")
	standaloneAgain := addExecution(newRequest(0, 1), 1, "reused")

	var mu sync.Mutex
	var queries int
	debug := ent.NewClient(ent.Driver(client.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		if strings.Contains(fmt.Sprint(args...), "previous_channel") {
			mu.Lock()
			queries++
			mu.Unlock()
		}
	}))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: debug}}))
	query := `{ requestLogs(first: 11, orderBy: {field: CREATED_AT, direction: DESC}) { edges { node { attempts: executions(first: 3) { edges { node { id selection { sessionState channelName } } } } } } } }`
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	httpRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body))).WithContext(ent.NewContext(withRequestChannelSessionCache(ctx), debug))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httpRequest)
	require.Equal(t, http.StatusOK, response.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.NotContains(t, payload, "errors", response.Body.String())
	states := map[string]string{}
	for _, edge := range payload["data"].(map[string]any)["requestLogs"].(map[string]any)["edges"].([]any) {
		for _, execution := range edge.(map[string]any)["node"].(map[string]any)["attempts"].(map[string]any)["edges"].([]any) {
			node := execution.(map[string]any)["node"].(map[string]any)
			if selection, ok := node["selection"].(map[string]any); ok {
				if node["id"] == fmt.Sprintf("gid://axonhub/RequestExecution/%d", back.ID) {
					require.Empty(t, selection["channelName"], "legacy selection must not invent a snapshot")
				} else {
					require.Equal(t, "recorded", selection["channelName"])
				}
				states[node["id"].(string)] = selection["sessionState"].(string)
			}
		}
	}
	require.NotContains(t, states, fmt.Sprintf("gid://axonhub/RequestExecution/%d", first.ID), "prior use is outside the returned page")
	for _, expected := range []struct {
		execution *ent.RequestExecution
		state     string
	}{
		{intermediate, ""}, {second, "new"}, {third, "new"}, {back, "returned"}, {inProgressExecution, ""}, {same, "reused"},
		{other, "new"}, {otherAgain, "reused"}, {standalone, "new"}, {standaloneAgain, "new"},
	} {
		id := fmt.Sprintf("gid://axonhub/RequestExecution/%d", expected.execution.ID)
		require.Contains(t, states, id)
		require.Equal(t, expected.state, states[id], id)
	}
	require.Equal(t, 1, queries, "the page must use one batch history query")
	query = fmt.Sprintf(`{ node(id: "gid://axonhub/Request/%d") { ... on Request { executions(first: 3) { edges { node { selection { sessionState } } } } } } }`, switchTo.ID)
	body, err = json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	httpRequest = httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body))).WithContext(ent.NewContext(withRequestChannelSessionCache(ctx), debug))
	httpRequest.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httpRequest)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"sessionState":"new"`)
	require.Contains(t, response.Body.String(), `"sessionState":""`)
	require.Equal(t, 2, queries, "the detail must also use one batch history query")
	resolver := &requestExecutionResolver{&Resolver{client: client}}
	selection, err := resolver.Selection(ctx, first)
	require.NoError(t, err)
	require.Equal(t, "new", selection.SessionState)
	require.Equal(t, "reused", first.Selection.SessionState, "reading the log must not rewrite the selection snapshot")
}

func TestRequestLogSessionExecutionSkipsUndispatchedRetries(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:session_execution_dispatch?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("dispatch").SaveX(ctx)
	trace := client.Trace.Create().SetProjectID(project.ID).SetTraceID("dispatch").SaveX(ctx)
	makeChannel := func(name string) int {
		return client.Channel.Create().SetName(name).SetType("openai").SetBaseURL("https://example.test").
			SetSupportedModels([]string{"test"}).SetDefaultTestModel("test").SetCredentials(objects.ChannelCredentials{}).
			SetTags([]string{name}).SaveX(ctx).ID
	}
	channelA, channelB := makeChannel("A"), makeChannel("B")
	create := func(channelID int) *ent.Request {
		return client.Request.Create().SetProjectID(project.ID).SetTraceID(trace.ID).SetChannelID(channelID).
			SetModelID("test").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("failed").SaveX(ctx)
	}
	attempt := func(req *ent.Request, channelID int, name, dispatch string) *ent.RequestExecution {
		return client.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(channelID).SetModelID(req.ModelID).
			SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("failed").
			SetSelection(&objects.RequestExecutionSelection{ChannelName: name, Dispatch: dispatch}).
			SetFailure(&objects.RequestExecutionFailure{Source: "upstream_http", Type: "rate_limit_error", Code: "channel_rpm_exhausted"}).
			SaveX(ctx)
	}
	retried := create(channelB)
	a := attempt(retried, channelA, "A", objects.RequestExecutionDispatchStarted)
	for range 11 {
		attempt(retried, channelB, "B", objects.RequestExecutionDispatchPending)
	}
	onlyRejected := create(channelB)
	attempt(onlyRejected, channelB, "B", objects.RequestExecutionDispatchPending)
	again := create(channelA)
	attempt(again, channelA, "A", objects.RequestExecutionDispatchStarted)
	firstB := create(channelB)
	attempt(firstB, channelB, "B", objects.RequestExecutionDispatchStarted)

	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client}}))
	query := `{ requestLogs(first: 4) { edges { node { id actual: sessionExecution { id selection { channelName sessionState } }
		executions(first: 1, orderBy: { field: CREATED_AT, direction: DESC }) { edges { node { selection { channelName sessionState } } } }
	} } } }`
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body))).
		WithContext(ent.NewContext(withRequestChannelSessionCache(ctx), client))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code)
	type execution struct {
		ID        string
		Selection struct{ ChannelName, SessionState string }
	}
	type result struct {
		ID         string
		Actual     *execution
		Executions struct{ Edges []struct{ Node execution } }
	}
	var payload struct {
		Errors []any
		Data   struct {
			RequestLogs struct{ Edges []struct{ Node result } }
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Empty(t, payload.Errors)
	rows := make(map[string]result)
	for _, edge := range payload.Data.RequestLogs.Edges {
		rows[edge.Node.ID] = edge.Node
	}
	row := func(req *ent.Request) result {
		id := fmt.Sprintf("gid://axonhub/Request/%d", req.ID)
		require.Contains(t, rows, id)
		return rows[id]
	}
	require.NotNil(t, row(retried).Actual)
	require.Equal(t, fmt.Sprintf("gid://axonhub/RequestExecution/%d", a.ID), row(retried).Actual.ID)
	require.Equal(t, "A", row(retried).Actual.Selection.ChannelName)
	require.Equal(t, "new", row(retried).Actual.Selection.SessionState)
	require.Equal(t, "B", row(retried).Executions.Edges[0].Node.Selection.ChannelName)
	require.Empty(t, row(retried).Executions.Edges[0].Node.Selection.SessionState)
	require.Nil(t, row(onlyRejected).Actual)
	require.NotNil(t, row(again).Actual)
	require.Equal(t, "reused", row(again).Actual.Selection.SessionState)
	require.NotNil(t, row(firstB).Actual)
	require.Equal(t, "new", row(firstB).Actual.Selection.SessionState)

	resolver := &queryResolver{&Resolver{client: client}}
	for _, filter := range []*RequestLogFilterInput{
		{ChannelIDsIn: []*objects.GUID{{Type: ent.TypeChannel, ID: channelA}}},
		{ChannelTagsIn: []string{"A"}},
	} {
		filtered, err := resolver.requestLogQuery(ctx, filter)
		require.NoError(t, err)
		require.ElementsMatch(t, []int{retried.ID, again.ID}, filtered.IDsX(ctx))
	}
}

func TestRequestLogTimezoneWithoutSettingsPermission(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_log_timezone?mode=memory&_fk=0")
	defer client.Close()
	setup := authz.WithTestBypass(context.Background())
	writer := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	require.NoError(t, writer.SetGeneralSettings(setup, biz.SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	// A cold service must read the stored timezone without relying on an owner's cache.
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	resolver := &queryResolver{&Resolver{systemService: system}}
	viewer := &ent.User{ID: 1, Scopes: []string{"read_requests"}}
	ctx := contexts.WithUser(context.Background(), viewer)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	timezone, err := resolver.RequestLogTimezone(ctx)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", timezone)
	viewer.Scopes = nil
	viewer.Edges.ProjectUsers = []*ent.UserProject{{ProjectID: 7, Scopes: []string{"read_requests"}}}
	projectCtx := contexts.WithProjectID(ctx, 7)
	timezone, err = resolver.RequestLogTimezone(projectCtx)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", timezone)
	_, err = resolver.RequestLogTimezone(contexts.WithProjectID(ctx, 8))
	require.Error(t, err)
	// contexts share a container: clear the project before testing absent scope.
	ctx = contexts.WithProjectID(ctx, 0)
	_, err = resolver.RequestLogTimezone(ctx)
	require.Error(t, err)
	_, err = resolver.RequestLogTimezone(context.Background())
	require.Error(t, err)
}

func TestRequestLogPageJump(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_log_page_jump?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("分页项目").SaveX(ctx)
	other := client.Project.Create().SetName("其他项目").SaveX(ctx)
	var ids []int
	for i := range 125 {
		record := client.Request.Create().SetProjectID(project.ID).SetModelID("page-model").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("completed").SaveX(ctx)
		ids = append(ids, record.ID)
		if i%5 == 0 {
			client.Request.Create().SetProjectID(other.ID).SetModelID("other-model").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("completed").SaveX(ctx)
		}
	}
	resolver := &queryResolver{&Resolver{client: client}}
	where := &ent.RequestWhereInput{ProjectID: &project.ID, ModelID: lo.ToPtr("page-model")}
	order := &ent.RequestOrder{Field: ent.RequestOrderFieldCreatedAt, Direction: entgql.OrderDirectionDesc}
	for _, offset := range []int{0, 20, 100, 120, 125, 200} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			conn, err := resolver.RequestLogs(ctx, nil, lo.ToPtr(20), nil, nil, order, where, &RequestLogFilterInput{Offset: &offset})
			require.NoError(t, err)
			require.Equal(t, 125, conn.TotalCount)
			count := max(0, min(20, 125-offset))
			require.Len(t, conn.Edges, count)
			for i, edge := range conn.Edges {
				require.Equal(t, ids[124-offset-i], edge.Node.ID)
			}
			require.Equal(t, offset+20 < 125, conn.PageInfo.HasNextPage)
			require.Equal(t, offset > 0, conn.PageInfo.HasPreviousPage)
			if count > 0 && conn.PageInfo.HasNextPage {
				adjacent, err := resolver.RequestLogs(ctx, conn.PageInfo.EndCursor, lo.ToPtr(20), nil, nil, order, where, nil)
				require.NoError(t, err)
				require.Equal(t, ids[124-offset-20], adjacent.Edges[0].Node.ID)
			}
		})
	}

	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client}}))
	updatedField := *ent.RequestOrderFieldUpdatedAt
	updatedPage, err := resolver.RequestLogs(ctx, nil, lo.ToPtr(20), nil, nil, &ent.RequestOrder{Field: &updatedField, Direction: entgql.OrderDirectionAsc}, where, &RequestLogFilterInput{Offset: lo.ToPtr(100)})
	require.NoError(t, err)
	require.Equal(t, ids[100], updatedPage.Edges[0].Node.ID)
	body := `{"query":"query { requestLogs(first: 20, filter: {offset: 100}, where: {modelID: \"page-model\"}, orderBy: {field: CREATED_AT, direction: DESC}) { totalCount edges { node { id modelID } } pageInfo { hasNextPage hasPreviousPage } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.NotContains(t, payload, "errors", response.Body.String())
	result := payload["data"].(map[string]any)["requestLogs"].(map[string]any)
	require.Equal(t, float64(125), result["totalCount"])
	require.Len(t, result["edges"], 20)
	_, err = resolver.RequestLogs(ctx, nil, lo.ToPtr(20), nil, nil, order, where, &RequestLogFilterInput{Offset: lo.ToPtr(-1)})
	require.Error(t, err)
	_, err = resolver.RequestLogs(ctx, &entgql.Cursor[int]{ID: ids[0]}, lo.ToPtr(20), nil, nil, order, where, &RequestLogFilterInput{Offset: lo.ToPtr(20)})
	require.Error(t, err)
	viewer := &ent.User{ID: 99, Scopes: []string{"read_requests"}, Edges: ent.UserEdges{ProjectUsers: []*ent.UserProject{{ProjectID: project.ID, Scopes: []string{"read_requests"}}}}}
	viewerCtx := contexts.WithUser(context.Background(), viewer)
	viewerCtx, err = authz.WithPrincipal(viewerCtx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	viewerCtx = contexts.WithProjectID(viewerCtx, project.ID)
	scoped, err := resolver.RequestLogs(viewerCtx, nil, lo.ToPtr(20), nil, nil, order, where, &RequestLogFilterInput{Offset: lo.ToPtr(100)})
	require.NoError(t, err)
	require.Equal(t, ids[24], scoped.Edges[0].Node.ID)
	viewer.Scopes = nil
	crossProject, err := resolver.RequestLogs(viewerCtx, nil, lo.ToPtr(20), nil, nil, order, &ent.RequestWhereInput{ProjectID: &other.ID}, &RequestLogFilterInput{Offset: lo.ToPtr(20)})
	require.NoError(t, err)
	require.Empty(t, crossProject.Edges)
	require.Zero(t, crossProject.TotalCount)
	_, err = resolver.RequestLogs(context.Background(), nil, lo.ToPtr(20), nil, nil, order, where, &RequestLogFilterInput{Offset: lo.ToPtr(20)})
	require.Error(t, err)
}

func TestRequestListCalculatedFieldsDoNotSelectPayloadColumns(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_list_columns?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	body := objects.JSONRawMessage(`{"input":"` + strings.Repeat("x", 1024) + `"}`)
	req := client.Request.Create().SetModelID("columns").SetRequestBody(body).SetStatus("completed").SaveX(ctx)
	client.RequestExecution.Create().SetRequestID(req.ID).SetModelID("columns").SetRequestBody(body).SetStatus("completed").SaveX(ctx)
	var statements []string
	debug := ent.NewClient(ent.Driver(client.Driver()), ent.Debug(), ent.Log(func(args ...any) { statements = append(statements, fmt.Sprint(args...)) }))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: debug}}))
	httpRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{requestLogs(first:1){edges{node{id status outputTokensPerSecond executions(first:1){edges{node{id costSnapshot{applied{value}}}}}}}}}"}`)).WithContext(ent.NewContext(ctx, debug))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httpRequest)
	require.Equal(t, http.StatusOK, response.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.NotContains(t, payload, "errors", response.Body.String())
	require.NotEmpty(t, statements)
	for _, statement := range statements {
		for _, column := range []string{"request_body", "response_body", "response_chunks", "request_headers"} {
			require.NotContains(t, statement, column, "list queries must stay small even when older rows retain large bodies")
		}
	}
}
