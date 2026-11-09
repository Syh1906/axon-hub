package gql

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
)

func TestRequestUsageSummaryGraphQLBatch(t *testing.T) {
	f := newContinuationFixture(t)
	for i := range 50 {
		req := f.add(f.at.Add(time.Duration(i)*time.Second), "completed", fmt.Sprint(i))
		for _, cost := range []float64{0.02, 0.03} {
			f.db.UsageLog.Create().SetProjectID(f.project.ID).SetRequestID(req.ID).SetModelID("model").
				SetTotalTokens(100).SetTotalCost(cost).SaveX(f.ctx)
		}
	}
	var queries atomic.Int64
	client := ent.NewClient(ent.Driver(dialect.Debug(f.db.Driver(), func(args ...any) {
		text := fmt.Sprint(args...)
		if strings.Contains(text, "query=") {
			queries.Add(1)
		}
	})))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client}}))
	for _, size := range []int{1, 50} {
		queries.Store(0)
		query := fmt.Sprintf(`{"query":"{ requestLogs(first: %d) { edges { node { id usageSummary { usageRecords unpricedRecords totalTokens totalCost } } } } }"}`, size)
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query)).WithContext(ent.NewContext(withRequestUsageCache(f.ctx), client))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		var payload struct {
			Errors []any
			Data   struct {
				RequestLogs struct {
					Edges []struct {
						Node struct {
							UsageSummary struct {
								UsageRecords    int
								UnpricedRecords int
								TotalTokens     int
								TotalCost       float64
							}
						}
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.Empty(t, payload.Errors, response.Body.String())
		require.Len(t, payload.Data.RequestLogs.Edges, size)
		for _, edge := range payload.Data.RequestLogs.Edges {
			require.Equal(t, 2, edge.Node.UsageSummary.UsageRecords)
			require.Equal(t, 200, edge.Node.UsageSummary.TotalTokens)
			require.InDelta(t, 0.05, edge.Node.UsageSummary.TotalCost, 1e-12)
		}
		t.Logf("page=%d database_reads=%d", size, queries.Load())
		require.LessOrEqual(t, queries.Load(), int64(6), "summary reads must not grow per row")
	}
}
