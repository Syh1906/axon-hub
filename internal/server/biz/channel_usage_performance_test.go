package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// A large day must remain complete across pages, including tied timestamps and
// records inserted out of chronological order. Keep timing informational.
func TestChannelTodayUsageLargeDay(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	const count = 8192
	for offset := 0; offset < count; offset += 128 {
		builders := make([]*ent.RequestCreate, 128)
		for i := range builders {
			builders[i] = f.client.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).
				SetChannelID(f.channel.ID).SetModelID("public").SetStatus(request.StatusCompleted).
				SetRequestBody([]byte(`{}`)).SetCreatedAt(start.Add(time.Duration((offset+i)%17) * time.Minute))
		}
		requests := f.client.Request.CreateBulk(builders...).SaveX(f.ctx)
		logs := make([]*ent.UsageLogCreate, len(requests))
		for i, req := range requests {
			logs[i] = f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).
				SetChannelID(f.channel.ID).SetModelID("public").SetPromptTokens(100).SetCompletionTokens(10).
				SetTotalCost(0.25).SetCostItems([]objects.CostItem{{Subtotal: decimal.RequireFromString("0.25"),
				PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.RequireFromString("0.5"))}}})
		}
		f.client.UsageLog.CreateBulk(logs...).SaveX(f.ctx)
	}
	var queries atomic.Int64
	counted := ent.NewClient(ent.Driver(dialect.Debug(f.client.Driver(), func(values ...any) {
		if strings.Contains(fmt.Sprint(values...), "query=") {
			queries.Add(1)
		}
	})))
	for attempt := range 3 {
		queries.Store(0)
		began := time.Now()
		rows, err := queryChannelTodayUsageModels(f.ctx, counted, f.channel.ID, start, start.AddDate(0, 0, 1))
		t.Logf("uncached attempt=%d requests=%d queries=%d elapsed=%s", attempt+1, count, queries.Load(), time.Since(began))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, count, rows[0].RequestCount)
		require.Equal(t, int64(count*110), rows[0].TotalTokens)
		require.Equal(t, float64(count)/4, rows[0].TotalCost)
		require.Equal(t, float64(count)/2, *rows[0].StandardCost)
		require.Zero(t, rows[0].UnpricedCount)
		if attempt > 0 {
			require.LessOrEqual(t, queries.Load(), int64(5), "重复刷新只读数值，不分页读取历史计价")
		}
	}
	svc := &ChannelService{AbstractService: &AbstractService{db: counted}}
	_, err := svc.cachedTodayUsageModels(f.ctx, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	queries.Store(0)
	began := time.Now()
	warm, err := svc.cachedTodayUsageModels(f.ctx, f.channel.ID, start, start.AddDate(0, 0, 1))
	t.Logf("warm requests=%d queries=%d elapsed=%s", count, queries.Load(), time.Since(began))
	require.NoError(t, err)
	require.EqualValues(t, 0, queries.Load())
	require.Equal(t, count, warm[0].RequestCount)
	// Retention moves numeric billing into the archive while requests remain.
	// The same channel/day read must stay bounded and preserve all amounts.
	logs := f.client.UsageLog.Query().Where(usagelog.ChannelIDEQ(f.channel.ID)).Select(ArchivedUsageFields...).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, logs))
	f.client.UsageLog.Delete().Where(usagelog.ChannelIDEQ(f.channel.ID)).ExecX(f.ctx)
	queries.Store(0)
	began = time.Now()
	archived, err := queryChannelTodayUsageModels(f.ctx, counted, f.channel.ID, start, start.AddDate(0, 0, 1))
	t.Logf("archived requests=%d queries=%d elapsed=%s", count, queries.Load(), time.Since(began))
	require.NoError(t, err)
	require.Len(t, archived, 1)
	require.Equal(t, count, archived[0].RequestCount)
	require.Equal(t, float64(count)/4, archived[0].TotalCost)
	require.Equal(t, int64(count*110), archived[0].TotalTokens)
	require.Nil(t, archived[0].StandardCost, "numeric archives cannot invent deleted price evidence")
	require.LessOrEqual(t, queries.Load(), int64(5), "归档账目只需数值聚合")
}

func TestChannelTodayUsageSharedRefreshSurvivesCanceledHover(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Now().UTC().Truncate(24 * time.Hour)
	f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var queries atomic.Int64
	counted := ent.NewClient(ent.Driver(dialect.Debug(f.client.Driver(), func(values ...any) {
		if strings.Contains(fmt.Sprint(values...), "query=") && queries.Add(1) == 1 {
			entered <- struct{}{}
			<-release
		}
	})))
	svc := &ChannelService{AbstractService: &AbstractService{db: counted}}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	var group errgroup.Group
	finished := make(chan error, 1)
	group.Go(func() error {
		_, err := svc.cachedTodayUsageModels(ctx, f.channel.ID, start, start.AddDate(0, 0, 1))
		finished <- err
		return nil
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("shared query did not start")
	}
	cancel()
	select {
	case err := <-finished:
		close(release)
		require.True(t, errors.Is(err, context.Canceled), "closing the hover must stop waiting immediately: %v", err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("canceled hover kept waiting for the shared query")
	}
	require.NoError(t, group.Wait())
	models, err := svc.cachedTodayUsageModels(f.ctx, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err, "the reopened hover must not inherit cancellation")
	require.Equal(t, 1, models[0].RequestCount)
	require.LessOrEqual(t, queries.Load(), int64(11), "包括先选执行编号的首次数值准备，只进行一次共享刷新")
}

func TestChannelTodayHourlyRefreshAndRollback(t *testing.T) {
	f := newConsumptionFixture(t)
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}, SystemService: f.service.system}
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	req, exec := f.addRequest(t, start.Add(time.Minute), "first", request.StatusCompleted, 100, 50, 0, 0, 2, nil)
	read := func(client *ent.Client) *objects.ChannelTodayUsage {
		rows, err := svc.channelUsageWithModels(f.ctx, client, []int{f.channel.ID}, start, end)
		require.NoError(t, err)
		return rows[0]
	}
	before := read(f.client)
	require.Equal(t, 1, before.RequestCount)
	require.Equal(t, 2., before.TotalCost)
	f.addRequest(t, start.Add(2*time.Hour), "later", request.StatusCompleted, 100, 20, 0, 0, 3, nil)
	require.Equal(t, 5., read(f.client).TotalCost)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetTotalCost(7).ExecX(f.ctx)
	f.client.RequestExecution.UpdateOneID(exec.ID).SetMetricsFirstTokenLatencyMs(450).ExecX(f.ctx)
	corrected := read(f.client)
	require.Equal(t, 10., corrected.TotalCost)
	require.Equal(t, "first", corrected.Models[0].Model)
	require.Equal(t, 450., *corrected.Models[0].AvgFirstTokenMs)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	tx.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetTotalCost(17).ExecX(f.ctx)
	require.Equal(t, 20., read(tx.Client()).TotalCost)
	require.NoError(t, tx.Rollback())
	require.Equal(t, corrected, read(f.client))
}
