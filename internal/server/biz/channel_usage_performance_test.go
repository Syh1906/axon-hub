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
		require.EqualValues(t, count/channelUsageBatchSize+3, queries.Load(), "分页费用读取，加两次渠道执行统计读取")
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
	require.EqualValues(t, count/channelUsageBatchSize+3, queries.Load(), "archived bills must not query deleted price snapshots per page")
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
	require.EqualValues(t, 4, queries.Load(), "费用与执行统计共用一次刷新，取消调用不重复计算")
}
