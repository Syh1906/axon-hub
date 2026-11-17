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
	"github.com/stretchr/testify/require"
	"github.com/zhenzou/executors"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

// Exercise the same pending/known/asOf/verify protocol used by the page, with
// real GraphQL resolvers, database and scheduler. No response is mocked.
func TestConsumptionGraphQLScheduledRanges(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:consumption_scheduled_ranges?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	setup := authz.WithTestBypass(t.Context())
	project := client.Project.Create().SetName("统计联调").SaveX(setup)
	owner := client.User.Create().SetEmail("statistics-flow@example.test").SetPassword("fixture-unused").SetIsOwner(true).SaveX(setup)
	key := client.APIKey.Create().SetName("统计密钥").SetKey("fixture-not-a-credential").SetUserID(owner.ID).SetProjectID(project.ID).SaveX(setup)
	channel := client.Channel.Create().SetName("统计渠道").SetType("openai").SetBaseURL("https://example.test").
		SetSupportedModels([]string{"model-a"}).SetDefaultTestModel("model-a").SetCredentials(objects.ChannelCredentials{}).SaveX(setup)
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	require.NoError(t, system.SetGeneralSettings(setup, biz.SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	loc := system.TimeLocation(setup)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	week := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	type fact struct {
		at   time.Time
		cost float64
	}
	facts := []fact{{month, 2}, {week, 3}, {today, 5}}
	for _, value := range facts {
		r := client.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(channel.ID).SetModelID("model-a").
			SetStatus("completed").SetRequestBody([]byte(`{}`)).SetCreatedAt(value.at.UTC()).SaveX(setup)
		client.RequestExecution.Create().SetProjectID(project.ID).SetRequestID(r.ID).SetChannelID(channel.ID).SetModelID("model-a").
			SetStatus("completed").SetRequestBody([]byte(`{}`)).SetStream(true).SetMetricsLatencyMs(1200).
			SetMetricsFirstTokenLatencyMs(700).SetMetricsFirstByteLatencyMs(200).SetCreatedAt(value.at.UTC()).SaveX(setup)
		client.UsageLog.Create().SetProjectID(project.ID).SetRequestID(r.ID).SetAPIKeyID(key.ID).SetChannelID(channel.ID).SetModelID("model-a").
			SetPromptTokens(100).SetCompletionTokens(100).SetTotalTokens(200).SetTotalCost(value.cost).SetCreatedAt(value.at.UTC()).SaveX(setup)
	}
	svc := biz.NewConsumptionService(client, system, xcache.Config{})
	executor := executors.NewPoolScheduleExecutor()
	tasks := scheduler.New(executor)
	require.NoError(t, svc.RegisterScheduledTasks(setup, tasks))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tasks.Shutdown(ctx)
		require.NoError(t, executor.Shutdown(ctx))
	})
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{consumptionService: svc}}))
	viewer := contexts.WithProjectID(contexts.WithUser(t.Context(), owner), project.ID)
	viewer, err := authz.WithPrincipal(viewer, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &owner.ID})
	require.NoError(t, err)
	type slice struct {
		RangeStart string
		Revision   string
		Entries    []struct {
			Metrics struct {
				RequestCount, TotalTokens int
				TotalCost                 float64
			}
		}
	}
	type page struct {
		Pending    bool
		Days       []struct{ Start, Revision string }
		Slices     []slice
		ResolvedAt string
		Next       *string
	}
	for _, view := range []string{"consumptionSlices", "usageSlices"} {
		for _, period := range []string{"MONTH", "WEEK", "TODAY"} {
			t.Run(view+"/"+period, func(t *testing.T) {
				dimension := "CHANNEL"
				performance := "successCount outcomeCount"
				if view == "usageSlices" {
					dimension = "API_KEY"
					performance = ""
				}
				query := fmt.Sprintf(`query($known:[ConsumptionSliceRevisionInput!],$asOf:Time,$verify:Boolean!){
				 %s(input:{dimension:%s,metric:COST,period:%s},known:$known,asOf:$asOf,verify:$verify){
				 pending resolvedAt next days{start revision} slices{rangeStart revision entries{metrics{requestCount totalTokens totalCost %s}}}}}`, view, dimension, period, performance)
				var known []map[string]string
				var asOf *string
				fetch := func(verify bool) page {
					body, err := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"known": known, "asOf": asOf, "verify": verify}})
					require.NoError(t, err)
					req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(string(body))).WithContext(viewer)
					req.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					server.ServeHTTP(response, req)
					require.Equal(t, http.StatusOK, response.Code)
					var envelope struct {
						Data   map[string]page
						Errors []json.RawMessage
					}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
					require.Empty(t, envelope.Errors, string(response.Body.Bytes()))
					return envelope.Data[view]
				}
				cached := map[string]slice{}
				var completed page
				for deadline := time.Now().Add(10 * time.Second); ; {
					value := fetch(false)
					if asOf == nil {
						asOf = &value.ResolvedAt
					}
					for _, item := range value.Slices {
						cached[item.RangeStart] = item
					}
					known = nil
					for _, item := range cached {
						known = append(known, map[string]string{"start": item.RangeStart, "revision": item.Revision})
					}
					if !value.Pending {
						completed = value
						break
					}
					require.True(t, time.Now().Before(deadline), "真实调度器没有完成范围")
					time.Sleep(25 * time.Millisecond)
				}
				require.Nil(t, completed.Next)
				require.False(t, fetch(true).Pending, "完整日期清单必须通过最终版本复核")
				repeat := fetch(false)
				require.False(t, repeat.Pending)
				require.Empty(t, repeat.Slices, "known 应省去已收到日期载荷")
				count, tokens, cost := 0, 0, 0.0
				for _, state := range completed.Days {
					item, ok := cached[state.Start]
					require.True(t, ok)
					require.Equal(t, state.Revision, item.Revision)
					for _, entry := range item.Entries {
						count += entry.Metrics.RequestCount
						tokens += entry.Metrics.TotalTokens
						cost += entry.Metrics.TotalCost
					}
				}
				from := map[string]time.Time{"TODAY": today, "WEEK": week, "MONTH": month}[period]
				wantCount, wantCost := 0, 0.0
				for _, value := range facts {
					if !value.at.Before(from) {
						wantCount++
						wantCost += value.cost
					}
				}
				require.Equal(t, wantCount, count)
				require.Equal(t, wantCount*200, tokens)
				require.Equal(t, wantCost, cost)
			})
		}
	}
}
