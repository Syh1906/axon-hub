package biz

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
)

func TestStatisticsPerformanceLargeDay(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	const count = 8192
	for offset := 0; offset < count; offset += 128 {
		requests := make([]*ent.RequestCreate, 128)
		for i := range requests {
			requests[i] = f.client.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).
				SetChannelID(f.channel.ID).SetModelID("model-a").SetStatus("completed").
				SetCreatedAt(start).SetRequestBody([]byte(`{}`))
		}
		rows := f.client.Request.CreateBulk(requests...).SaveX(f.ctx)
		executions := make([]*ent.RequestExecutionCreate, len(rows))
		bills := make([]*ent.UsageLogCreate, len(rows))
		for i, row := range rows {
			executions[i] = f.client.RequestExecution.Create().SetRequestID(row.ID).SetProjectID(f.project.ID).
				SetChannelID(f.channel.ID).SetModelID("model-a").SetStatus("completed").
				SetCreatedAt(start).SetRequestBody([]byte(`{}`)).SetStream(true).
				SetMetricsLatencyMs(1200).SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(700)
			bills[i] = f.client.UsageLog.Create().SetRequestID(row.ID).SetProjectID(f.project.ID).
				SetChannelID(f.channel.ID).SetModelID("model-a").SetCompletionTokens(100).SetTotalTokens(100).SetTotalCost(1)
		}
		f.client.RequestExecution.CreateBulk(executions...).SaveX(f.ctx)
		f.client.UsageLog.CreateBulk(bills...).SaveX(f.ctx)
	}
	var queries atomic.Int64
	counted := ent.NewClient(ent.Driver(dialect.Debug(&statisticsExplainDriver{Driver: f.client.Driver(), t: t}, func(values ...any) {
		if strings.Contains(fmt.Sprint(values...), "query=") {
			queries.Add(1)
		}
	})))
	for attempt := range 2 {
		queries.Store(0)
		began := time.Now()
		ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
		rows, err := queryChannelAttemptGroups(ctx, counted, f.project.ID, ConsumptionFilter{}, []int{f.channel.ID}, &start, start.AddDate(0, 0, 1))
		cancel()
		t.Logf("performance attempt=%d executions=%d queries=%d elapsed=%s", attempt+1, count, queries.Load(), time.Since(began))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.EqualValues(t, count, rows[0].SpeedCount)
		require.EqualValues(t, count, rows[0].OutcomeCount)
		require.InDelta(t, count*100, rows[0].SpeedSum, 1e-9)
		if attempt > 0 {
			require.LessOrEqual(t, queries.Load(), int64(3), "重复读取不重新匹配每条执行的账目")
		}
	}
}

type statisticsExplainDriver struct {
	dialect.Driver
	t         *testing.T
	explained atomic.Int32
}

func (d *statisticsExplainDriver) BeginTx(ctx context.Context, opts *stdsql.TxOptions) (dialect.Tx, error) {
	tx, err := d.Driver.(interface {
		BeginTx(context.Context, *stdsql.TxOptions) (dialect.Tx, error)
	}).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &statisticsExplainTx{Tx: tx, owner: d}, nil
}

type statisticsExplainTx struct {
	dialect.Tx
	owner *statisticsExplainDriver
}

func (d *statisticsExplainTx) Query(ctx context.Context, query string, args, result any) error {
	if strings.Contains(query, "request_executions") && (strings.Contains(query, "statistics_ready") || strings.Contains(query, "statistics_version")) && d.owner.explained.Add(1) <= 2 {
		var cursor sql.Rows
		if err := d.Tx.Query(ctx, "EXPLAIN QUERY PLAN "+query, args, &cursor); err != nil {
			return err
		}
		var plan []struct {
			ID      int
			Parent  int
			NotUsed int
			Detail  string
		}
		err := sql.ScanSlice(&cursor, &plan)
		_ = cursor.Close()
		if err != nil {
			return err
		}
		for _, row := range plan {
			d.owner.t.Log(row.Detail)
		}
	}
	return d.Tx.Query(ctx, query, args, result)
}
