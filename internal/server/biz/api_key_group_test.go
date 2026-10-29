package biz

import (
	"context"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupAdministration(t *testing.T) {
	s, db := setupTestAPIKeyService(t, xcache.Config{Mode: xcache.ModeMemory})
	t.Cleanup(s.Stop)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	owner := db.User.Create().SetEmail("group-owner@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	member := db.User.Create().SetEmail("group-member@example.test").SetPassword("fixture").SaveX(ctx)
	p := db.Project.Create().SetName("group-project").SaveX(ctx)
	other := db.Project.Create().SetName("other-project").SaveX(ctx)
	ctx = contexts.WithProjectID(contexts.WithUser(ctx, owner), p.ID)
	group, err := s.SaveAPIKeyGroup(ctx, nil, "  recipient  ")
	require.NoError(t, err)
	require.Equal(t, "recipient", group.Name)
	_, err = s.SaveAPIKeyGroup(ctx, nil, "recipient")
	require.Error(t, err)
	foreign := db.APIKeyGroup.Create().SetProjectID(other.ID).SetName("recipient").SaveX(ctx)
	_, err = s.CreateAPIKey(ctx, ent.CreateAPIKeyInput{Name: "wrong-project", ProjectID: p.ID, GroupRef: &foreign.ID})
	require.Error(t, err)
	key, err := s.CreateAPIKey(ctx, ent.CreateAPIKeyInput{Name: "assigned", ProjectID: p.ID, GroupRef: &group.ID})
	require.NoError(t, err)
	memberCtx := contexts.WithProjectID(contexts.WithUser(authz.WithTestBypass(ent.NewContext(context.Background(), db)), member), p.ID)
	_, err = s.SaveAPIKeyGroup(memberCtx, nil, "unauthorized")
	require.Error(t, err)
	personal := apikey.TypePersonal
	_, err = s.CreateAPIKey(memberCtx, ent.CreateAPIKeyInput{Name: "unauthorized", ProjectID: p.ID, Type: &personal, GroupRef: &group.ID})
	require.Error(t, err)
	_, err = s.UpdateAPIKey(memberCtx, key.ID, ent.UpdateAPIKeyInput{ClearGroupRef: true})
	require.Error(t, err)
	require.Error(t, s.DeleteAPIKeyGroup(ctx, foreign.ID))
	_, err = s.SaveAPIKeyGroup(ctx, &group.ID, "renamed")
	require.NoError(t, err)
	require.NoError(t, s.DeleteAPIKeyGroup(ctx, group.ID))
	require.Nil(t, db.APIKey.GetX(ctx, key.ID).GroupRef)
	require.True(t, db.APIKeyGroup.Query().Where().ExistX(ctx))
}

func TestConsumptionAPIKeyGroupsCurrentMembership(t *testing.T) {
	f := newConsumptionFixture(t)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("recipient").SaveX(f.ctx)
	f.key = f.key.Update().SetGroupRef(group.ID).SaveX(f.ctx)
	at := time.Now().Add(-time.Hour)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
	first := f.key
	f.key = f.client.APIKey.Create().SetName("second").SetKey("fixture-second").SetProjectID(f.project.ID).SetUserID(f.user.ID).SetGroupRef(group.ID).SaveX(f.ctx)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 300, 40, 0, 0, 3, nil)
	f.key = f.client.APIKey.Create().SetName("ungrouped").SetKey("fixture-ungrouped").SetProjectID(f.project.ID).SetUserID(f.user.ID).SaveX(f.ctx)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 1000, 100, 0, 0, 10, nil)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKeyGroup, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	result, err := f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.Equal(t, 2, result.Entries[0].Metrics.RequestCount)
	require.Equal(t, 5.0, result.Entries[0].Metrics.TotalCost)
	next := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("next").SaveX(f.ctx)
	first.Update().SetGroupRef(next.ID).SaveX(f.ctx)
	result, err = f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
	input.GroupRef = &next.ID
	result, err = f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.Equal(t, 2.0, result.Entries[0].Metrics.TotalCost)
}

func TestSelfUsageGroupScopeAndReassignment(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	group := f.client.APIKeyGroup.Create().SetName("recipient").SetProjectID(f.project.ID).SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).SaveX(ctx)
	f.other.Update().SetGroupRef(group.ID).SaveX(ctx)
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "a")
	f.request(at, f.other.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "a")
	f.request(at, f.other.ID, f.otherProject.ID, request.SourceAPI, request.StatusCompleted, "a")
	f.request(at, f.other.ID, f.project.ID, request.SourcePlayground, request.StatusCompleted, "a")
	single, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01")
	require.NoError(t, err)
	require.Equal(t, 1, single.Totals.SuccessRequests)
	combined, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", true)
	require.NoError(t, err)
	require.Equal(t, 2, combined.Totals.SuccessRequests)
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20, true)
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	for _, row := range page.Items {
		require.Contains(t, row.APIKeyName, "recipient")
	}
	f.other.Update().ClearGroupRef().SaveX(ctx)
	combined, err = f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", true)
	require.NoError(t, err)
	require.Equal(t, 1, combined.Totals.SuccessRequests)
	_, err = f.service.Stats(contexts.WithAPIKey(f.ctx, f.other), "2026-01-01", "2026-01-01", true)
	require.Error(t, err)
}

func TestSelfUsageReportsCurrentGroupIdentity(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	a := f.client.APIKeyGroup.Create().SetName("A").SetProjectID(f.project.ID).SaveX(ctx)
	b := f.client.APIKeyGroup.Create().SetName("B").SetProjectID(f.project.ID).SaveX(ctx)
	f.key.Update().SetGroupRef(a.ID).SaveX(ctx)
	f.request(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "model")
	first, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", true)
	require.NoError(t, err)
	require.Equal(t, "A", *first.GroupName)
	f.key.Update().SetGroupRef(b.ID).SaveX(ctx)
	// Logs may refresh independently through status/model/pagination controls.
	for _, status := range []string{"SUCCESS", "FAILED"} {
		logs, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, status, 1, 20, true)
		require.NoError(t, err)
		require.NotEqual(t, first.ScopeID, logs.ScopeID)
		require.Equal(t, "B", *logs.GroupName)
		require.Equal(t, f.key.Name+" · B", logs.APIKeyName)
	}
	model := "model"
	emptyPage, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", &model, "SUCCESS", 2, 20, true)
	require.NoError(t, err)
	require.Empty(t, emptyPage.Items)
	require.Equal(t, "B", *emptyPage.GroupName)
	moved, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", true)
	require.NoError(t, err)
	require.Equal(t, "B", *moved.GroupName)
	require.Equal(t, emptyPage.ScopeID, moved.ScopeID)
	require.Equal(t, f.key.Name+" · B", moved.APIKeyName)
	require.Equal(t, 1, moved.Totals.SuccessRequests)
	f.key.Update().ClearGroupRef().SaveX(ctx)
	_, err = f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", true)
	require.Error(t, err)
	single, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01", false)
	require.NoError(t, err)
	require.Nil(t, single.GroupName)
	require.Equal(t, 1, single.Totals.SuccessRequests)
}

func TestConsumptionSameNamedGroupsRemainDistinct(t *testing.T) {
	f := newConsumptionFixture(t)
	firstProject := f.project
	at := time.Now().Add(-time.Hour)
	a := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("team").SaveX(f.ctx)
	f.key = f.key.Update().SetGroupRef(a.ID).SaveX(f.ctx)
	f.addRequest(t, at, "model", request.StatusCompleted, 10, 5, 0, 0, 1, nil)
	f.project = f.client.Project.Create().SetName("other").SaveX(f.ctx)
	b := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("team").SaveX(f.ctx)
	f.key = f.client.APIKey.Create().SetName("second").SetKey("fixture-other-project").SetProjectID(f.project.ID).SetUserID(f.user.ID).SetGroupRef(b.ID).SaveX(f.ctx)
	f.addRequest(t, at, "model", request.StatusCompleted, 10, 5, 0, 0, 2, nil)
	ctx := f.viewer(t, firstProject.ID, string(scopes.ScopeReadLeaderboard))
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKeyGroup, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	result, err := f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
	require.Equal(t, "team · other", result.Entries[0].Name)
	require.Equal(t, "team · stats", result.Entries[1].Name)
	input.ProjectID = &objects.GUID{Type: ent.TypeProject, ID: firstProject.ID}
	result, err = f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.Equal(t, "team", result.Entries[0].Name)
}
