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
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestRequestLogStatusCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_log_status_counts?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("计数项目").SaveX(ctx)
	other := client.Project.Create().SetName("其他项目").SaveX(ctx)
	user := client.User.Create().SetEmail("counts@example.test").SetPassword("unused-test-hash").SetFirstName("测试用户").SaveX(ctx)
	group := client.APIKeyGroup.Create().SetName("计数组").SetProjectID(project.ID).SaveX(ctx)
	key := client.APIKey.Create().SetName("测试 Key").SetKey("fixture-not-a-credential").SetUserID(user.ID).SetProjectID(project.ID).SetGroupRef(group.ID).SaveX(ctx)
	channel := client.Channel.Create().SetName("计数渠道").SetType("openai").SetBaseURL("https://example.test").SetSupportedModels([]string{"count-model"}).SetDefaultTestModel("count-model").SetCredentials(objects.ChannelCredentials{}).SetTags([]string{"counts"}).SaveX(ctx)
	at := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
	add := func(status request.Status, projectID int, at time.Time) {
		client.Request.Create().SetProjectID(projectID).SetAPIKeyID(key.ID).SetChannelID(channel.ID).SetModelID("count-model").SetSource("api").SetCreatedAt(at).SetRequestBody(objects.JSONRawMessage(`{"input":"test"}`)).SetStatus(status).SaveX(ctx)
	}
	// 先写失败再写六十条成功：第一页没有失败，但完整统计仍必须有失败数量。
	add(request.StatusFailed, project.ID, at)
	add(request.StatusFailed, project.ID, at)
	for range 60 {
		add(request.StatusCompleted, project.ID, at)
	}
	add(request.StatusFailed, other.ID, at)
	add(request.StatusFailed, project.ID, at.Add(-24*time.Hour))
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	require.NoError(t, system.SetGeneralSettings(ctx, biz.SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	resolver := &queryResolver{&Resolver{client: client, systemService: system}}
	where := &ent.RequestWhereInput{ProjectID: &project.ID, ModelIDContainsFold: lo.ToPtr("COUNT"), SourceIn: []request.Source{request.SourceAPI}, ChannelIDIn: []int{channel.ID}, APIKeyIDIn: []int{key.ID}}
	filter := &RequestLogFilterInput{ChannelTagsIn: []string{"counts"}, APIKeyGroupRefsIn: []int{group.ID}, TimeRange: &RequestLogTimeRangeInput{Period: biz.ConsumptionPeriodCustom, Start: lo.ToPtr("2026-11-03T00:00:00"), End: lo.ToPtr("2026-11-03T23:59:59")}, Offset: lo.ToPtr(100)}
	expected := []*RequestLogStatusCount{
		{Status: request.StatusPending}, {Status: request.StatusProcessing}, {Status: request.StatusCompleted, Count: 60}, {Status: request.StatusFailed, Count: 2}, {Status: request.StatusCanceled},
	}
	counts, err := resolver.RequestLogStatusCounts(ctx, where, filter)
	require.NoError(t, err)
	require.Equal(t, expected, counts)
	for _, mutate := range []func(*ent.RequestWhereInput, *RequestLogFilterInput){
		func(w *ent.RequestWhereInput, _ *RequestLogFilterInput) { w.ModelIDContainsFold = lo.ToPtr("missing") },
		func(w *ent.RequestWhereInput, _ *RequestLogFilterInput) {
			w.SourceIn = []request.Source{request.SourcePlayground}
		},
		func(_ *ent.RequestWhereInput, f *RequestLogFilterInput) { f.ChannelTagsIn = []string{"missing"} },
		func(_ *ent.RequestWhereInput, f *RequestLogFilterInput) { f.APIKeyGroupRefsIn = []int{0} },
	} {
		w, f := *where, *filter
		mutate(&w, &f)
		counts, err := resolver.RequestLogStatusCounts(ctx, &w, &f)
		require.NoError(t, err)
		require.Len(t, counts, 5)
		for _, count := range counts {
			require.Zero(t, count.Count)
		}
	}

	viewer := &ent.User{ID: user.ID, Edges: ent.UserEdges{ProjectUsers: []*ent.UserProject{{ProjectID: project.ID, Scopes: []string{"read_requests"}}}}}
	viewerCtx := contexts.WithUser(context.Background(), viewer)
	viewerCtx, err = authz.WithPrincipal(viewerCtx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	viewerCtx = contexts.WithProjectID(viewerCtx, project.ID)
	counts, err = resolver.RequestLogStatusCounts(viewerCtx, nil, filter)
	require.NoError(t, err)
	require.Equal(t, expected, counts)
	counts, err = resolver.RequestLogStatusCounts(viewerCtx, &ent.RequestWhereInput{ProjectID: &other.ID}, filter)
	require.NoError(t, err)
	for _, count := range counts {
		require.Zero(t, count.Count)
	}
	_, err = resolver.RequestLogStatusCounts(context.Background(), nil, nil)
	require.Error(t, err)

	// 从真实 GraphQL 接口验证枚举、零值，以及仅一条不读取正文的分组查询。
	var statements []string
	debug := ent.NewClient(ent.Driver(client.Driver()), ent.Debug(), ent.Log(func(args ...any) { statements = append(statements, fmt.Sprint(args...)) }))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: debug}}))
	body := fmt.Sprintf(`{"query":"{requestLogStatusCounts(where:{projectID:\"gid://axonhub/Project/%d\"}){status count}}"}`, project.ID)
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(body)).WithContext(ent.NewContext(ctx, debug))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Data struct {
			Counts []*RequestLogStatusCount `json:"requestLogStatusCounts"`
		} `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Empty(t, result.Errors, response.Body.String())
	expected[3].Count = 3 // 不带时间条件时包含前一天的失败。
	require.Equal(t, expected, result.Data.Counts)
	require.Len(t, statements, 1)
	require.Contains(t, statements[0], "GROUP BY")
	for _, field := range []string{"request_body", "response_body", "response_chunks", "request_headers"} {
		require.NotContains(t, statements[0], field)
	}
}
