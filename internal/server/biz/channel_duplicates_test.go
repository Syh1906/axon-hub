package biz

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestChannelDuplicateNamesLifecycle(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	input := ent.CreateChannelInput{Type: "openai", Name: "same", BaseURL: lo.ToPtr("https://example.test"), Credentials: objects.ChannelCredentials{APIKey: "fixture"}, SupportedModels: []string{"model"}, DefaultTestModel: "model"}
	a, err := svc.CreateChannel(ctx, input)
	require.NoError(t, err)
	b, err := svc.CreateChannel(ctx, input)
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	c, err := svc.DuplicateChannel(ctx, a.ID, input)
	require.NoError(t, err)
	require.Equal(t, a.Name, c.Name)
	require.NotEqual(t, a.ID, c.ID)
	_, err = svc.UpdateChannel(ctx, b.ID, &ent.UpdateChannelInput{Name: lo.ToPtr("other")})
	require.NoError(t, err)
	_, err = svc.UpdateChannel(ctx, b.ID, &ent.UpdateChannelInput{Name: lo.ToPtr("same"), Tags: []string{"changed"}})
	require.NoError(t, err)
	imported, err := svc.BulkImportChannels(ctx, []*BulkImportChannelItem{
		{Type: "openai", Name: "same", BaseURL: input.BaseURL, APIKey: lo.ToPtr("fixture-1"), SupportedModels: input.SupportedModels, DefaultTestModel: "model"},
		{Type: "openai", Name: "same", BaseURL: input.BaseURL, APIKey: lo.ToPtr("fixture-2"), SupportedModels: input.SupportedModels, DefaultTestModel: "model"},
	})
	require.NoError(t, err)
	require.Equal(t, 2, imported.Created)
	require.Zero(t, imported.Failed)
	require.Equal(t, 5, client.Channel.Query().CountX(ctx))
	require.Empty(t, client.Channel.GetX(ctx, a.ID).Tags)
}

func TestChannelDuplicateNameSchemaUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:channel-name-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	for _, table := range old {
		if table.Name == "channels" {
			for _, index := range table.Indexes {
				if index.Name == "channels_by_name" {
					index.Unique = true
				}
			}
		}
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithForeignKeys(false)))
	create := func() (*ent.Channel, error) {
		return client.Channel.Create().SetName("same").SetType("openai").SetBaseURL("https://example.test").SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").Save(ctx)
	}
	a, err := create()
	require.NoError(t, err)
	_, err = create()
	require.True(t, ent.IsConstraintError(err))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false), migrate.WithDropIndex(true)))
	b, err := create()
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	require.Equal(t, a.Name, client.Channel.GetX(ctx, a.ID).Name)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false), migrate.WithDropIndex(true)))
	require.Equal(t, 2, client.Channel.Query().CountX(ctx))
}

func TestConsumptionDuplicateChannelsKeepIdentityAndCache(t *testing.T) {
	f := newConsumptionFixture(t)
	f.service.cache = xcache.NewMemoryWithOptions[*ConsumptionResult](time.Minute, 0)
	a := f.client.Channel.UpdateOne(f.channel).SetName("same").SetTags([]string{"shared", "小为"}).SaveX(f.ctx)
	b := f.client.Channel.Create().SetName("same").SetType("openai").SetBaseURL("https://example.test").SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetTags([]string{"shared", "裂魂"}).SaveX(f.ctx)
	at := time.Now().Add(-time.Minute)
	f.channel = a
	f.addRequest(t, at, "model", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
	f.channel = b
	f.addRequest(t, at, "model", request.StatusCompleted, 200, 20, 0, 0, 7, nil)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
	input := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	first, err := f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	byID := map[int]*ConsumptionEntry{}
	for _, entry := range first.Entries {
		byID[entry.ID.ID] = entry
		require.True(t, entry.DuplicateName)
	}
	require.Equal(t, 2.0, byID[a.ID].Metrics.TotalCost)
	require.Equal(t, 7.0, byID[b.ID].Metrics.TotalCost)
	require.Equal(t, "小为", byID[a.ID].ChannelTags[0])
	require.Equal(t, "裂魂", byID[b.ID].ChannelTags[0])
	f.client.Channel.UpdateOneID(a.ID).SetTags([]string{"shared", "备用"}).SaveX(f.ctx)
	second, err := f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.GeneratedAt, second.GeneratedAt, "tag changes must reuse the expensive aggregate")
	require.Equal(t, "小为", byID[a.ID].ChannelTags[0], "earlier returned snapshots must not be mutated")
	for _, entry := range second.Entries {
		if entry.ID.ID == a.ID {
			require.Equal(t, "备用", entry.ChannelTags[0])
		}
	}
	f.client.Channel.UpdateOneID(b.ID).SetName("unique").SaveX(f.ctx)
	third, err := f.service.Leaderboard(ctx, input)
	require.NoError(t, err)
	for _, entry := range third.Entries {
		require.False(t, entry.DuplicateName)
	}

	queries := 0
	f.client.Channel.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			queries++
			return next.Query(ctx, query)
		})
	}))
	_, err = f.service.withChannelIdentity(f.ctx, input, first)
	require.NoError(t, err)
	require.Equal(t, 1, queries, "metadata must be read in one batch regardless of row count")
}
