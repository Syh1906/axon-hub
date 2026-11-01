package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

func TestConsumptionPricingSamplesSurviveMissingEvidenceAndArchive(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().Add(-time.Hour)
	zero, _ := f.addRequest(t, at, "model", request.StatusCompleted, 100, 0, 0, 0, 0, nil)
	paid, _ := f.addRequest(t, at, "model", request.StatusCompleted, 100, 0, 0, 0, 2, nil)
	missing, _ := f.addRequest(t, at, "model", request.StatusCompleted, 10000, 0, 0, 0, 9, nil)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(missing.ID)).ClearTotalCost().SaveX(f.ctx)
	noUsage, _ := f.addRequest(t, at, "model", request.StatusCanceled, 0, 0, 0, 0, 0, nil)
	fixed, _ := f.addRequest(t, at, "model", request.StatusCompleted, 0, 0, 0, 0, 0, nil)
	f.client.UsageLog.Create().SetRequestID(fixed.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetAPIKeyID(f.key.ID).SetModelID("model").SetTotalCost(5).SaveX(f.ctx)
	partial, _ := f.addRequest(t, at, "model", request.StatusCompleted, 9000, 0, 0, 0, 1, nil)
	f.client.UsageLog.Create().SetRequestID(partial.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetAPIKeyID(f.key.ID).SetModelID("model").SetPromptTokens(1000).SaveX(f.ctx)
	filter := ConsumptionFilter{Dimension: ConsumptionDimensionChannel}
	check := func() {
		rows, err := f.service.queryGroups(f.ctx, f.project.ID, filter, nil, time.Now())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		m := rows[0].metrics()
		require.Equal(t, 6, m.RequestCount)
		require.Equal(t, 3, m.UnpricedRequestCount)
		require.Equal(t, 3, m.PricedRequestCount)
		require.Equal(t, 2, m.TokenPricedRequestCount)
		require.Equal(t, 8.0, m.TotalCost)
		require.InDelta(t, 7.0/3, *m.AvgCostPerRequest, 1e-9)
		require.Equal(t, 10000.0, *m.AvgCostPerMillionTokens)
	}
	check()
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{zero.ID, paid.ID, missing.ID, noUsage.ID, fixed.ID, partial.ID}))
	check()
}
