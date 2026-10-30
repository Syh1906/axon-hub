package biz

import (
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRequestLogFiltersCombineAndKeepTimezone(t *testing.T) {
	f := newConsumptionFixture(t)
	zone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	day := time.Date(2026, 3, 8, 12, 0, 0, 0, zone)
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, zone)
	group := f.client.APIKeyGroup.Create().SetName("team").SetProjectID(f.project.ID).SaveX(f.ctx)
	f.key = f.key.Update().SetGroupRef(group.ID).SaveX(f.ctx)
	f.channel = f.channel.Update().SetTags([]string{"production", "fast"}).SaveX(f.ctx)
	before, _ := f.addRequest(t, start.Add(-time.Second), "a", request.StatusCompleted, 1, 1, 0, 0, 0, nil)
	within, _ := f.addRequest(t, start.Add(22*time.Hour), "a", request.StatusCompleted, 1, 1, 0, 0, 0, nil)
	after, _ := f.addRequest(t, start.Add(23*time.Hour), "a", request.StatusCompleted, 1, 1, 0, 0, 0, nil)
	p, err := RequestLogDatePredicates(ConsumptionPeriodToday, nil, nil, day, zone)
	require.NoError(t, err)
	require.Equal(t, []int{within.ID}, f.client.Request.Query().Where(p...).IDsX(f.ctx))
	gp, err := RequestLogGroupPredicate([]int{0, group.ID})
	require.NoError(t, err)
	require.Equal(t, []int{within.ID}, f.client.Request.Query().Where(p...).Where(gp, RequestLogTagPredicate([]string{"fast", "missing"})).IDsX(f.ctx))
	gp, err = RequestLogGroupPredicate([]int{0})
	require.NoError(t, err)
	require.Empty(t, f.client.Request.Query().Where(gp).IDsX(f.ctx))
	f.key.Update().ClearGroupRef().ExecX(f.ctx)
	require.ElementsMatch(t, []int{before.ID, within.ID, after.ID}, f.client.Request.Query().Where(gp).IDsX(f.ctx))
	from, until := "2026-03-08T00:00:00", "2026-03-08T23:59:59"
	p, err = RequestLogDatePredicates(ConsumptionPeriodCustom, &from, &until, day, zone)
	require.NoError(t, err)
	require.Equal(t, []int{within.ID}, f.client.Request.Query().Where(p...).IDsX(f.ctx))
	from = "2026-03-08T02:30:00" // nonexistent DST wall time
	_, err = RequestLogDatePredicates(ConsumptionPeriodCustom, &from, &until, day, zone)
	require.Error(t, err)
	_, err = RequestLogGroupPredicate([]int{-1})
	require.Error(t, err)
}

func TestManagedGroupUsagePermissionAndDisabledKeys(t *testing.T) {
	f := newConsumptionFixture(t)
	service := &APIKeyService{AbstractService: &AbstractService{db: f.client}}
	group := f.client.APIKeyGroup.Create().SetName("team").SetProjectID(f.project.ID).SaveX(f.ctx)
	f.key = f.key.Update().SetGroupRef(group.ID).SetStatus(apikey.StatusDisabled).SaveX(f.ctx)
	at := time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	ctx := ent.NewContext(f.viewer(t, f.project.ID, string(scopes.ScopeReadAPIKeys)), f.client)
	stats, err := service.ManagedGroupUsageStats(ctx, f.service.system, group.ID, "2026-10-30", "2026-10-30")
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Totals.SuccessRequests)
	rows, err := service.ManagedGroupUsageRequests(ctx, f.service.system, group.ID, "2026-10-30", "2026-10-30", nil, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	empty := f.client.APIKeyGroup.Create().SetName("empty").SetProjectID(f.project.ID).SaveX(f.ctx)
	stats, err = service.ManagedGroupUsageStats(ctx, f.service.system, empty.ID, "2026-10-30", "2026-10-30")
	require.NoError(t, err)
	require.Zero(t, stats.Totals.SuccessRequests)
	_, err = service.ManagedGroupUsageStats(ent.NewContext(f.viewer(t, f.project.ID), f.client), f.service.system, group.ID, "2026-10-30", "2026-10-30")
	require.Error(t, err)
	other := f.client.Project.Create().SetName("other").SaveX(f.ctx)
	_, err = service.ManagedGroupUsageStats(contexts.WithProjectID(ctx, other.ID), f.service.system, group.ID, "2026-10-30", "2026-10-30")
	require.Error(t, err)
}
