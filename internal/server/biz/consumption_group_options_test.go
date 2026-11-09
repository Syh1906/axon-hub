package biz

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestConsumptionGroupOptionsRespectRangeCacheAndScope(t *testing.T) {
	f := newConsumptionFixture(t)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("active").SaveX(f.ctx)
	f.client.APIKey.UpdateOne(f.key).SetGroupRef(group.ID).SaveX(f.ctx)
	f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("unused").SaveX(f.ctx)
	at := time.Now().UTC().Add(-time.Minute)
	req, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 1, lo.ToPtr(int64(200)))
	filter := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKeyGroup, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
	var queries []string
	client := ent.NewClient(ent.Driver(f.client.Driver()), ent.Debug(), ent.Log(func(args ...any) { queries = append(queries, fmt.Sprint(args...)) }))
	f.service.client = client
	options, err := f.service.LeaderboardGroupOptions(ctx, filter)
	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Equal(t, group.ID, options[0].ID.ID)
	require.Equal(t, "active · stats", options[0].Name)
	require.NotContains(t, strings.Join(queries, "\n"), "request_executions", "options must not compute performance")
	require.NotContains(t, strings.Join(queries, "\n"), "SUM(", "options must not aggregate token or cost totals")
	filter.Metric = ConsumptionMetricCache
	options, err = f.service.LeaderboardGroupOptions(ctx, filter)
	require.NoError(t, err)
	require.Empty(t, options)
	// Archived facts survive request cleanup and keep current key-group membership.
	f.client.UsageArchive.Create().SetKind(usagearchive.KindRequest).SetSourceID(req.ID).SetRequestID(req.ID).
		SetAPIKeyID(f.key.ID).SetProjectID(f.project.ID).SetModelID("model-a").SetOccurredAt(at).SetCacheRequests(1).SaveX(f.ctx)
	options, err = f.service.LeaderboardGroupOptions(ctx, filter)
	require.NoError(t, err)
	require.Len(t, options, 1)
	filter.Period = ConsumptionPeriodCustom
	filter.StartDate, filter.EndDate = lo.ToPtr("2020-01-01"), lo.ToPtr("2020-01-02")
	options, err = f.service.LeaderboardGroupOptions(ctx, filter)
	require.NoError(t, err)
	require.Empty(t, options)
	filter.Period, filter.Metric = ConsumptionPeriodAll, ConsumptionMetricCost
	filter.ProjectID = lo.ToPtr(objects.GUID{Type: ent.TypeProject, ID: f.project.ID})
	options, err = f.service.LeaderboardGroupOptions(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, "active", options[0].Name)
	_, err = f.service.LeaderboardGroupOptions(context.Background(), filter)
	require.Error(t, err)
	_, err = f.service.UsageGroupOptions(f.viewer(t, f.project.ID+1, string(scopes.ScopeReadRequests)), filter)
	require.ErrorContains(t, err, "must match")
}
