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
