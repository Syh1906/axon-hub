package gql

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/intercept"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestDeleteAPIKeysGraphQLAndHistoricalRequest(t *testing.T) {
	mutation, _, ctx, db := setupTestAPIKeyProfileTemplateResolvers(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	owner := createTestUserWithScopes(t, ctx, db)
	project := createTestProject(t, ctx, db)
	ctx = contexts.WithProjectID(contexts.WithUser(ctx, owner), project.ID)
	service := biz.NewAPIKeyService(biz.APIKeyServiceParams{Ent: db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, KeyPrefix: "ah"})
	t.Cleanup(service.Stop)
	mutation.apiKeyService = service
	mutation.systemService = biz.NewSystemService(biz.SystemServiceParams{Ent: db})
	key := db.APIKey.Create().SetName("history").SetKey("synthetic-delete-test").SetProjectID(project.ID).SetUserID(owner.ID).SaveX(ctx)
	req := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetModelID("fixture").SetStatus(request.StatusCompleted).SetRequestBody([]byte(`{}`)).SaveX(ctx)
	db.UsageLog.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetRequestID(req.ID).SetModelID("fixture").SetPromptTokens(10).SetCompletionTokens(2).SetTotalTokens(12).SetTotalCost(1.25).SaveX(ctx)
	query := &queryResolver{mutation.Resolver}
	counts, err := query.RequestStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	tokens, err := query.TokenStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	costs, err := query.CostStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	_, schemaErr := gqlparser.LoadQuery(NewExecutableSchema(Config{}).Schema(), `mutation DeleteAPIKeys($ids:[ID!]!){deleteAPIKeys(ids:$ids)}`)
	require.Empty(t, schemaErr)
	ok, err := mutation.DeleteAPIKeys(ctx, []*objects.GUID{{Type: ent.TypeProject, ID: key.ID}})
	require.Error(t, err)
	require.False(t, ok)
	ok, err = mutation.DeleteAPIKeys(ctx, []*objects.GUID{{Type: ent.TypeAPIKey, ID: key.ID}})
	require.NoError(t, err)
	require.True(t, ok)
	resolver := &requestResolver{mutation.Resolver}
	identity, err := resolver.APIKey(ctx, req)
	require.NoError(t, err)
	require.Equal(t, key.ID, identity.ID)
	require.Equal(t, key.Name, identity.Name)
	require.NotEqual(t, key.Key, identity.Key)
	countsAfter, err := query.RequestStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, counts, countsAfter)
	tokensAfter, err := query.TokenStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, tokens, tokensAfter)
	costsAfter, err := query.CostStatsByAPIKey(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, costs, costsAfter)
}

func TestHistoricalRequestAPIKeyReusesDeletedIdentity(t *testing.T) {
	mutation, _, ctx, db := setupTestAPIKeyProfileTemplateResolvers(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	project := createTestProject(t, ctx, db)
	key := db.APIKey.Create().SetName("historical").SetKey("deleted:1:api_key:v3:456").
		SetDeletedAt(1).SetStatus(apikey.StatusDisabled).SetProjectID(project.ID).
		SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "historical-profile"}).SaveX(ctx)
	requests := make([]*ent.Request, 2)
	for i := range requests {
		requests[i] = db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).
			SetModelID("fixture").SetStatus(request.StatusCompleted).SetRequestBody([]byte(`{}`)).SaveX(ctx)
	}
	var queries atomic.Int64
	db.Intercept(intercept.Func(func(_ context.Context, query intercept.Query) error {
		if query.Type() == ent.TypeAPIKey {
			queries.Add(1)
		}
		return nil
	}))
	operation := withKeyIdentityCache(ctx)
	resolver := &requestResolver{mutation.Resolver}
	var group errgroup.Group
	results := make([]*ent.APIKey, 20)
	for i := range results {
		group.Go(func() error {
			var err error
			results[i], err = resolver.APIKey(operation, requests[i%len(requests)])
			return err
		})
	}
	require.NoError(t, group.Wait())
	require.Equal(t, int64(1), queries.Load(), "the same deleted identity is loaded once per operation")
	for _, result := range results {
		require.Equal(t, key.ID, result.ID)
		require.Equal(t, "historical-profile", result.Profiles.ActiveProfile, "different field selections retain complete historical data")
	}
	_, err := resolver.APIKey(withKeyIdentityCache(ctx), requests[0])
	require.NoError(t, err)
	require.Equal(t, int64(2), queries.Load(), "identity data is not shared between operations")
}
