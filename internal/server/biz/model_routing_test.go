package biz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

func createRoutingRule(t *testing.T, ctx context.Context, svc *ModelService, name, pattern string, kind objects.ModelMatchType, priority int, publicIDs []string, settings *objects.ModelSettings) *ent.Model {
	t.Helper()
	m, err := svc.CreateModel(ctx, ent.CreateModelInput{
		Name: name, Developer: "openai", ModelID: pattern, MatchType: &kind, MatchPriority: &priority,
		PublicModelIds: publicIDs, Type: lo.ToPtr(model.TypeChat), Icon: "openai", Group: "test",
		ModelCard: &objects.ModelCard{}, Settings: settings,
	})
	require.NoError(t, err)
	m, err = svc.UpdateModelStatus(ctx, m.ID, model.StatusEnabled)
	require.NoError(t, err)
	return m
}

func TestModelRoutingPrecedenceAndVisibility(t *testing.T) {
	ctx, client, svc, channels, _ := setupListEnabledModelsTest(t)
	ch := client.Channel.Create().SetType("openai").SetName("dynamic").SetBaseURL("https://example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"old-model"}).
		SetDefaultTestModel("old-model").SetStatus("enabled").SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	settings := &objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}}}}
	regex := createRoutingRule(t, ctx, svc, "regex", `^gpt-.*-luna$`, objects.ModelMatchRegex, 10, []string{"gpt-6-luna", "gpt-6.1-luna"}, settings)
	prefix := createRoutingRule(t, ctx, svc, "prefix", "gpt-", objects.ModelMatchPrefix, 20, nil, settings)
	suffix := createRoutingRule(t, ctx, svc, "suffix", "-luna", objects.ModelMatchSuffix, 5, nil, settings)
	exact := createRoutingRule(t, ctx, svc, "exact", "gpt-6-luna", objects.ModelMatchExact, 100, nil, settings)
	resolved, err := svc.ResolveModel(ctx, "gpt-6-luna")
	require.NoError(t, err)
	require.Equal(t, exact.ID, resolved.ID)
	resolved, err = svc.ResolveModel(ctx, "gpt-6.1-luna")
	require.NoError(t, err)
	require.Equal(t, suffix.ID, resolved.ID)
	// A hidden winning rule does not leak an ID published by a lower rule.
	require.Equal(t, map[string]bool{"gpt-6-luna": true}, listedModelIDs(t, ctx, svc))
	client.Model.UpdateOne(suffix).SetStatus(model.StatusDisabled).SaveX(ctx)
	resolved, err = svc.ResolveModel(ctx, "gpt-6.1-luna")
	require.NoError(t, err)
	require.Equal(t, regex.ID, resolved.ID)
	require.Equal(t, map[string]bool{"gpt-6-luna": true, "gpt-6.1-luna": true}, listedModelIDs(t, ctx, svc))
	resolved, err = svc.ResolveModel(ctx, "gpt-new")
	require.NoError(t, err)
	require.Equal(t, prefix.ID, resolved.ID)
	_, err = svc.UpdateModel(ctx, exact.ID, &ent.UpdateModelInput{PublicModelIds: []string{}})
	require.NoError(t, err)
	hidden := client.Model.GetX(ctx, exact.ID)
	require.NotNil(t, hidden.PublicModelIds)
	require.Empty(t, PublicModelIDs(hidden))
	require.Equal(t, map[string]bool{"gpt-6.1-luna": true}, listedModelIDs(t, ctx, svc))
	_, err = svc.ResolveModel(ctx, "gpt-6-luna")
	require.NoError(t, err, "hiding discovery does not disable requests")
	_, err = svc.UpdateModel(ctx, regex.ID, &ent.UpdateModelInput{PublicModelIds: []string{"wrong-id"}})
	require.Error(t, err)
	_, err = svc.UpdateModel(ctx, regex.ID, &ent.UpdateModelInput{ModelID: lo.ToPtr("[")})
	require.Error(t, err)
	// Different match types may use the same expression.
	createRoutingRule(t, ctx, svc, "same-expression", "gpt-", objects.ModelMatchExact, 0, nil, settings)
}

func TestModelRoutingPublicAliasesRespectScope(t *testing.T) {
	ctx, client, svc, channels, _ := setupListEnabledModelsTest(t)
	ch := client.Channel.Create().SetType("openai").SetName("dynamic").SetBaseURL("https://example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"old-model"}).
		SetDefaultTestModel("old-model").SetTags([]string{"allowed"}).SetStatus("enabled").SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	settings := &objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}}}}
	createRoutingRule(t, ctx, svc, "rule", "gpt-", objects.ModelMatchPrefix, 10, []string{"gpt-6-luna", "gpt-6.1-luna"}, settings)
	profile := objects.APIKeyProfile{Name: "active", ModelIDs: []string{"client-alias", "hidden-alias"}, ChannelTags: []string{"allowed"}, ModelMappings: []objects.ModelMapping{
		{From: "client-alias", To: "gpt-6.1-luna"}, {From: "hidden-alias", To: "gpt-new"},
	}}
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "active", Profiles: []objects.APIKeyProfile{profile}}}
	requestCtx := contexts.WithAPIKey(ctx, key)
	require.Equal(t, map[string]bool{"client-alias": true}, listedModelIDs(t, requestCtx, svc))
	key.Profiles.Profiles[0].ChannelTags = []string{"excluded"}
	require.Empty(t, listedModelIDs(t, requestCtx, svc))
}

func TestRequestModelAssociationsAndInheritance(t *testing.T) {
	channels := []*Channel{{Channel: &ent.Channel{ID: 1, SupportedModels: []string{"gpt-new"}}}}
	assoc := &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: 1}}
	for _, id := range []string{"gpt-new", "gpt-newer"} {
		connections := MatchConnectionsForModel([]*objects.ModelAssociation{assoc}, channels, id)
		require.Len(t, connections, 1)
		require.Equal(t, []ChannelModelEntry{{RequestModel: id, ActualModel: id, Source: "request"}}, connections[0].Models)
	}
	require.Empty(t, MatchConnectionsForModel([]*objects.ModelAssociation{assoc}, nil, "gpt-new"))
}

func TestRuleModelInheritedChannelCount(t *testing.T) {
	ctx, client, svc, _, systemSvc := setupListEnabledModelsTest(t)
	ch := client.Channel.Create().SetType("openai").SetName("inherited").SetBaseURL("https://example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"gpt-6-luna", "unrelated"}).
		SetDefaultTestModel("gpt-6-luna").SetStatus("enabled").SaveX(ctx)
	require.NoError(t, systemSvc.SetModelSettings(ctx, SystemModelSettings{
		DeveloperSettings: []*DeveloperModelSettings{{Developer: "openai", Associations: []*objects.ModelAssociation{
			{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID}},
		}}},
	}))
	m := createRoutingRule(t, ctx, svc, "inherited rule", `^gpt-.*-luna$`, objects.ModelMatchRegex, 10, nil, &objects.ModelSettings{})
	count, err := svc.CountModelAssociatedChannels(ctx, m)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	unassociated, err := svc.QueryUnassociatedChannels(ctx)
	require.NoError(t, err)
	require.Len(t, unassociated, 1)
	require.Equal(t, []string{"unrelated"}, unassociated[0].Models)
}
