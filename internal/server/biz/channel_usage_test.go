package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
)

func TestChannelTodayUsageReaderGetsSystemCurrency(t *testing.T) {
	f := newConsumptionFixture(t)
	require.NoError(t, f.service.system.SetGeneralSettings(f.ctx, SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "CNY"}))
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}, SystemService: f.service.system}
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadChannels))
	require.False(t, authz.HasScope(ctx, scopes.ScopeReadSettings))
	rows, err := svc.TodayUsage(ctx, []int{f.channel.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "CNY", rows[0].CurrencyCode)
	require.Equal(t, "Asia/Shanghai", rows[0].Timezone)
	_, err = svc.TodayUsage(f.viewer(t, f.project.ID), []int{f.channel.ID})
	require.Error(t, err)
}

func TestChannelTodayUsageDateBoundaryAndMissingPrices(t *testing.T) {
	f := newConsumptionFixture(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 1)
	f.addRequest(t, start.Add(-time.Nanosecond), "outside-before", request.StatusCompleted, 10, 1, 0, 0, 100, nil)
	f.addRequest(t, end, "outside-after", request.StatusCompleted, 10, 1, 0, 0, 100, nil)
	f.addRequest(t, start, "zero-price", request.StatusCompleted, 10, 1, 0, 0, 0, nil)
	priced, _ := f.addRequest(t, start.Add(time.Hour), "priced", request.StatusCompleted, 10, 1, 0, 0, 2, nil)
	// A second usage row increases cost, but not the request count.
	f.client.UsageLog.Create().SetRequestID(priced.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("priced").
		SetPromptTokens(10).SetCompletionTokens(1).SetTotalTokens(11).SetTotalCost(3).SaveX(f.ctx)
	unpriced, _ := f.addRequest(t, start.Add(2*time.Hour), "no-price", request.StatusCompleted, 0, 0, 0, 0, 0, nil)
	f.client.UsageLog.Create().SetRequestID(unpriced.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("no-price").
		SetPromptTokens(10).SetCompletionTokens(1).SetTotalTokens(11).SaveX(f.ctx)
	f.addRequest(t, start.Add(3*time.Hour), "no-usage", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	results, err := queryChannelTodayUsage(f.ctx, f.client, []int{f.channel.ID, f.channel.ID + 1}, start, end)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 4, results[0].RequestCount)
	require.Equal(t, 2, results[0].UnpricedCount)
	require.Equal(t, 5.0, results[0].TotalCost)
	require.Equal(t, "Asia/Shanghai", results[0].Timezone)
	require.Zero(t, results[1].RequestCount)
	require.Zero(t, results[1].UnpricedCount)
	require.Zero(t, results[1].TotalCost)
}
