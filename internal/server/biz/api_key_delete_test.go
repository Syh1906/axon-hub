package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestDeleteAPIKeysDestroysCredentialAndPreservesHistory(t *testing.T) {
	f := newBudgetFixture(t)
	keyCtx := context.WithValue(f.ctx, apiKeyCtxKey{}, f.key.Key)
	cacheKey := buildAPIKeyCacheKey(f.key.Key)
	_, err := f.admin.APIKeyCache.Get(keyCtx, cacheKey)
	require.NoError(t, err)
	// Another instance has a separate memory watcher, so it misses the event.
	peer := NewAPIKeyService(APIKeyServiceParams{Ent: f.db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, ProjectService: f.admin.ProjectService, SystemService: f.admin.SystemService, KeyPrefix: "ah"})
	t.Cleanup(peer.Stop)
	_, found := peer.APIKeyCache.GetCached(cacheKey)
	require.True(t, found)

	at := time.Now().UTC()
	req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetModelID("fixture").SetStatus(request.StatusCompleted).SetSource(request.SourceAPI).SetRequestBody([]byte(`{}`)).SetCreatedAt(at).SaveX(f.ctx)
	f.db.UsageLog.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetRequestID(req.ID).SetModelID("fixture").SetTotalTokens(42).SetTotalCost(1.25).SaveX(f.ctx)
	date := at.In(f.admin.SystemService.TimeLocation(f.ctx)).Format("2006-01-02")
	before, err := f.admin.ManagedGroupUsageStats(f.ctx, f.admin.SystemService, f.group.ID, date, date)
	require.NoError(t, err)
	require.NoError(t, f.admin.DeleteAPIKeys(f.ctx, []int{f.key.ID, f.key.ID}))
	require.False(t, f.db.APIKey.Query().Where(apikey.IDEQ(f.key.ID)).ExistX(f.ctx))
	deleted := f.db.APIKey.GetX(schematype.SkipSoftDelete(f.ctx), f.key.ID)
	require.NotZero(t, deleted.DeletedAt)
	require.Equal(t, apikey.StatusDisabled, deleted.Status)
	require.NotEqual(t, f.key.Key, deleted.Key)
	require.Empty(t, deleted.Scopes)
	require.Equal(t, cacheKey, apiKeyCacheIdentity(deleted))
	require.Equal(t, f.key.ID, f.db.Request.GetX(f.ctx, req.ID).APIKeyID)
	require.Equal(t, 1, f.db.UsageLog.Query().CountX(f.ctx))
	after, err := f.admin.ManagedGroupUsageStats(f.ctx, f.admin.SystemService, f.group.ID, date, date)
	require.NoError(t, err)
	require.Equal(t, before, after)
	page, err := f.admin.ManagedGroupUsageRequests(f.ctx, f.admin.SystemService, f.group.ID, date, date, nil, "", 1, 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Contains(t, page.Items[0].APIKeyName, f.key.Name)
	_, err = f.admin.GetAPIKey(f.ctx, f.key.Key)
	require.Error(t, err)
	_, err = f.admin.GetAPIKey(f.ctx, deleted.Key)
	require.Error(t, err, "deletion markers are not credentials")
	require.Error(t, f.admin.CheckAPIKeyGroupEnabled(f.ctx, f.key.ID, f.key.ProjectID), "a stale authentication cache must not authorize a deleted key")
	_, err = f.key.Update().SetKey("stale-rotation").SetStatus(apikey.StatusEnabled).Save(f.ctx)
	require.True(t, ent.IsNotFound(err), "an editor loaded before deletion cannot overwrite the deleted row")
	_, err = f.admin.RotateAPIKey(f.ctx, f.key.ID)
	require.Error(t, err)
	_, err = f.admin.UpdateAPIKeyStatus(f.ctx, f.key.ID, apikey.StatusEnabled)
	require.Error(t, err)
	require.NoError(t, peer.APIKeyCache.Load(f.ctx))
	_, found = peer.APIKeyCache.GetCached(cacheKey)
	require.False(t, found, "periodic refresh must evict the original cache identity without retaining its secret")
	_, err = f.admin.CreateAPIKey(f.ctx, ent.CreateAPIKeyInput{Name: f.key.Name, ProjectID: f.key.ProjectID, GroupRef: f.key.GroupRef})
	require.NoError(t, err, "deleted names can be reused")
}

func TestDeleteAPIKeysPreservesInflightSettlement(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "10")
	f.configure(t, "group", f.group.ID, "20")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 71, 71, budgetAmount(t, "2"), nil))
	require.NoError(t, f.service.MarkSent(f.ctx, 71))
	keyBefore := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey), budgetaccount.OwnerIDEQ(f.key.ID)).OnlyX(f.ctx)
	groupBefore := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindGroup), budgetaccount.OwnerIDEQ(f.group.ID)).OnlyX(f.ctx)
	require.NoError(t, f.admin.DeleteAPIKeys(f.ctx, []int{f.key.ID}))
	require.Equal(t, keyBefore.Reserved, f.db.BudgetAccount.GetX(f.ctx, keyBefore.ID).Reserved)
	require.Equal(t, groupBefore.Reserved, f.db.BudgetAccount.GetX(f.ctx, groupBefore.ID).Reserved)
	require.Error(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 72, 72, budgetAmount(t, "1"), nil))
	cost := budgetAmount(t, "1.25")
	_, handled, err := f.service.Settle(f.ctx, 71, &cost, nil)
	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, f.db.BudgetAccount.GetX(f.ctx, keyBefore.ID).Reserved)
	require.Zero(t, f.db.BudgetAccount.GetX(f.ctx, groupBefore.ID).Reserved)
	require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx))
	_, _, err = f.service.Settle(f.ctx, 71, &cost, nil)
	require.NoError(t, err)
	require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx), "late retries cannot charge twice")
}

func TestDeleteAPIKeysRejectsWholeSelection(t *testing.T) {
	for _, kind := range []string{"foreign", "personal", "system", "missing"} {
		t.Run(kind, func(t *testing.T) {
			f := newBudgetFixture(t)
			badID := f.key.ID + 999
			if kind != "missing" {
				builder := f.db.APIKey.Create().SetName("protected").SetKey("protected").SetProjectID(f.key.ProjectID)
				switch kind {
				case "foreign":
					builder.SetProjectID(f.db.Project.Create().SetName("foreign").SaveX(f.ctx).ID)
				case "personal":
					other := f.db.User.Create().SetEmail("other@example.test").SetPassword("fixture").SaveX(f.ctx)
					builder.SetType(apikey.TypePersonal).SetUserID(other.ID)
				case "system":
					builder.SetType(apikey.TypeNoauth)
				}
				badID = builder.SaveX(f.ctx).ID
			}
			require.Error(t, f.admin.DeleteAPIKeys(f.ctx, []int{f.key.ID, badID}))
			require.Equal(t, f.key.Key, f.db.APIKey.GetX(f.ctx, f.key.ID).Key)
			require.Zero(t, f.db.BudgetAccount.Query().CountX(f.ctx), "failed deletion must also roll back lock-account creation")
		})
	}
}

func TestDeleteAPIKeysCommitBoundary(t *testing.T) {
	f := newBudgetFixture(t)
	cacheKey := buildAPIKeyCacheKey(f.key.Key)
	_, err := f.admin.APIKeyCache.Get(context.WithValue(f.ctx, apiKeyCtxKey{}, f.key.Key), cacheKey)
	require.NoError(t, err)
	for _, commit := range []bool{false, true} {
		tx, err := f.db.Tx(f.ctx)
		require.NoError(t, err)
		ctx := ent.NewContext(ent.NewTxContext(f.ctx, tx), tx.Client())
		require.NoError(t, f.admin.DeleteAPIKeys(ctx, []int{f.key.ID}))
		_, found := f.admin.APIKeyCache.GetCached(cacheKey)
		require.True(t, found, "outer GraphQL transaction has not committed")
		if commit {
			require.NoError(t, tx.Commit())
			_, found = f.admin.APIKeyCache.GetCached(cacheKey)
			require.False(t, found)
		} else {
			require.NoError(t, tx.Rollback())
			require.Equal(t, f.key.Key, f.db.APIKey.GetX(f.ctx, f.key.ID).Key)
		}
	}
}

func TestDeleteAPIKeysUsesProjectWritePermission(t *testing.T) {
	f := newBudgetFixture(t)
	actor := f.db.User.Create().SetEmail("writer@example.test").SetPassword("fixture").SaveX(f.ctx)
	f.db.UserProject.Create().SetUserID(actor.ID).SetProjectID(f.key.ProjectID).SetScopes([]string{string(scopes.ScopeReadAPIKeys), string(scopes.ScopeWriteAPIKeys)}).SaveX(f.ctx)
	actor.Edges.ProjectUsers = actor.QueryProjectUsers().AllX(f.ctx)
	ctx := contexts.WithProjectID(contexts.WithUser(ent.NewContext(context.Background(), f.db), actor), f.key.ProjectID)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &actor.ID})
	require.NoError(t, err)
	require.Error(t, f.admin.DeleteAPIKeys(contexts.WithProjectID(f.ctx, 0), []int{f.key.ID}))
	f.db.UserProject.Update().Where().SetScopes([]string{string(scopes.ScopeReadAPIKeys)}).ExecX(f.ctx)
	require.Error(t, f.admin.DeleteAPIKeys(ctx, []int{f.key.ID}))
	f.db.UserProject.Update().Where().SetScopes([]string{string(scopes.ScopeReadAPIKeys), string(scopes.ScopeWriteAPIKeys)}).ExecX(f.ctx)
	ctx = contexts.WithProjectID(contexts.WithUser(ent.NewContext(context.Background(), f.db), actor), f.key.ProjectID)
	ctx, err = authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &actor.ID})
	require.NoError(t, err)
	require.NoError(t, f.admin.DeleteAPIKeys(ctx, []int{f.key.ID}))
}

func TestDeleteAPIKeysKeepsLeaderboardTotals(t *testing.T) {
	f := newConsumptionFixture(t)
	f.user.Update().SetIsOwner(true).ExecX(f.ctx)
	group := f.client.APIKeyGroup.Create().SetName("historical").SetProjectID(f.project.ID).SaveX(f.ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(f.ctx)
	f.addRequest(t, time.Now().Add(-time.Hour), "model-a", request.StatusCompleted, 100, 10, 0, 0, 1.25, nil)
	s := NewAPIKeyService(APIKeyServiceParams{Ent: f.client, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, ProjectService: &ProjectService{ProjectCache: xcache.NewFromConfig[xcache.Entry[ent.Project]](xcache.Config{})}, SystemService: f.service.system, KeyPrefix: "ah"})
	t.Cleanup(s.Stop)
	actor := f.client.User.GetX(f.ctx, f.user.ID)
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, actor), f.project.ID)
	reader := contexts.WithUser(f.viewer(t, f.project.ID), actor)
	for _, dimension := range []ConsumptionDimension{ConsumptionDimensionAPIKey, ConsumptionDimensionAPIKeyGroup, ConsumptionDimensionUser} {
		input := ConsumptionFilter{Dimension: dimension, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll, ProjectID: &objects.GUID{Type: ent.TypeProject, ID: f.project.ID}}
		before, err := f.service.Leaderboard(reader, input)
		require.NoError(t, err)
		require.Len(t, before.Entries, 1)
		if dimension == ConsumptionDimensionAPIKey {
			require.NoError(t, s.DeleteAPIKeys(ctx, []int{f.key.ID}))
		}
		after, err := f.service.Leaderboard(reader, input)
		require.NoError(t, err)
		require.Equal(t, before.Entries, after.Entries)
		require.Equal(t, 1.25, after.Entries[0].Metrics.TotalCost)
	}
}
