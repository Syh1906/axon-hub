package biz

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

// The former small, single-day fixture could not expose an ID-ordered scan
// through all preceding history. Use a disposable PostgreSQL schema and the
// complete page/date worker path, including initially unprepared executions.
func TestStatisticsLargeHistoryPostgres(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	const history, dayCount = 160000, 20000
	start := time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
	queries := []string{
		fmt.Sprintf(`INSERT INTO requests (id,created_at,updated_at,project_id,api_key_id,channel_id,model_id,status,request_body)
SELECT n, CASE WHEN n <= %d THEN $1::timestamptz - interval '15 days' + ((n-1)*7 %% 1296000)*interval '1 second'
ELSE $1::timestamptz + ((n-%d-1)*4 %% 86400)*interval '1 second' END, $1, %d,%d,%d,'model-' || (n %% 4),'completed','{}'
FROM generate_series(1,%d) n`, history, history, f.project.ID, f.key.ID, f.channel.ID, history+dayCount),
		`INSERT INTO request_executions (id,created_at,updated_at,request_id,project_id,channel_id,model_id,status,request_body,stream,metrics_latency_ms,metrics_first_token_latency_ms,metrics_first_byte_latency_ms)
SELECT id,created_at,updated_at,id,project_id,channel_id,model_id,status,'{}',true,1200,700,200 FROM requests`,
		`INSERT INTO usage_logs (id,created_at,updated_at,request_id,project_id,api_key_id,channel_id,model_id,prompt_tokens,completion_tokens,total_tokens,total_cost)
SELECT id,created_at,updated_at,id,project_id,api_key_id,channel_id,model_id,100,100,200,1 FROM requests`,
	}
	for i, query := range queries {
		var args []any
		if i == 0 {
			args = []any{start}
		}
		require.NoError(t, f.client.Driver().Exec(f.ctx, query, args, nil))
	}
	// Only this disposable synthetic schema is analyzed; no production settings.
	for _, table := range []string{"requests", "request_executions", "usage_logs"} {
		require.NoError(t, f.client.Driver().Exec(f.ctx, "ANALYZE "+table, []any{}, nil))
	}
	var count atomic.Int64
	counted := ent.NewClient(ent.Driver(dialect.Debug(f.client.Driver(), func(values ...any) {
		if strings.Contains(fmt.Sprint(values...), "query=") {
			count.Add(1)
		}
	})))
	reader := *f.service
	reader.client = counted
	f.service = &reader
	day := "2026-11-16"
	input := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost,
		Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day}
	f.user.IsOwner = true
	ctx := contexts.WithUser(f.ctx, f.user)
	for _, phase := range []string{"cold-day", "unchanged-day", "corrected-hour", "cold-month", "unchanged-month"} {
		if phase == "corrected-hour" {
			f.client.UsageLog.UpdateOneID(history + 1).SetTotalCost(2).ExecX(f.ctx)
		}
		if strings.Contains(phase, "month") {
			first := "2026-11-01"
			input.StartDate = &first
		}
		count.Store(0)
		began := time.Now()
		page, err := f.readSlices(t, ctx, input, nil, "leaderboard")
		elapsed := time.Since(began)
		t.Logf("phase=%s history=%d day=%d queries=%d elapsed=%s", phase, history, dayCount, count.Load(), elapsed)
		require.NoError(t, err)
		if phase == "cold-day" {
			require.Less(t, count.Load(), int64(350), "首次统计不逐批回写执行后再读取")
			require.Zero(t, f.client.RequestExecution.Query().Where(requestexecution.StatisticsReadyEQ(true)).CountX(f.ctx), "汇总读取只写小汇总，不重写两万条执行")
		}
		requests, cost := 0, 0.0
		for _, slice := range page.Slices {
			for _, entry := range slice.Entries {
				requests += entry.Metrics.RequestCount
				cost += entry.Metrics.TotalCost
			}
		}
		want := dayCount
		if strings.Contains(phase, "month") {
			want += history
		}
		correction := 0.0
		if phase == "corrected-hour" || strings.Contains(phase, "month") {
			correction = 1
		}
		require.Equal(t, want, requests)
		require.Equal(t, float64(want)+correction, cost)
		if strings.HasPrefix(phase, "unchanged") {
			require.LessOrEqual(t, count.Load(), int64(10), "未变化范围只读修订和小汇总")
		}
	}
	ready := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryIsNil()).CountX(f.ctx)
	require.Greater(t, ready, 24)
	// Project usage follows the same hour pipeline with its own authorization.
	input.Dimension, input.StartDate, input.OmitPerformance = ConsumptionDimensionAPIKey, &day, true
	ctx = contexts.WithProjectID(ctx, f.project.ID)
	began := time.Now()
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	t.Logf("phase=project-usage-day elapsed=%s", time.Since(began))
	require.NoError(t, err)
	require.Equal(t, dayCount, page.Slices[0].Entries[0].Metrics.RequestCount)
	require.Equal(t, float64(dayCount+1), page.Slices[0].Entries[0].Metrics.TotalCost)
	require.Equal(t, history+dayCount, f.client.UsageLog.Query().Where(usagelog.TotalCostNotNil()).CountX(f.ctx))
}
