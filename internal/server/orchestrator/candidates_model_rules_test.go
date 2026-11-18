package orchestrator

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestTaggedModelRulesRouteAndList(t *testing.T) {
	ctx, client := setupTest(t)
	const older, newer = "gpt-6-astra", "gpt-6.2-astra"
	const pattern = "^gpt-(?<version>[0-9]+(?:[.][0-9]+)*)-astra$"
	for _, tag := range []string{"Codex", "OpenAI"} {
		client.Channel.Create().SetType("openai").SetName(tag).SetTags([]string{tag}).SetStatus("enabled").
			SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
			SetSupportedModels([]string{older, newer}).SetDefaultTestModel(older).SaveX(ctx)
	}
	create := func(id string, kind objects.ModelMatchType, publicIDs []string, association *objects.ModelAssociation) *ent.Model {
		return client.Model.Create().SetDeveloper("test").SetModelID(id).SetMatchType(kind).SetPublicModelIds(publicIDs).
			SetName(id).SetIcon("test").SetGroup("test").SetModelCard(&objects.ModelCard{}).SetStatus(model.StatusEnabled).
			SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{association}}).SaveX(ctx)
	}
	rule := create(pattern, objects.ModelMatchRegex, []string{older, newer}, &objects.ModelAssociation{
		Type: "channel_tags_regex", PreferLatestVersion: true,
		ChannelTagsRegex: &objects.ChannelTagsRegexAssociation{ChannelTags: []string{"Codex"}, Pattern: pattern},
	})
	for _, id := range []string{older, newer} {
		create(id, objects.ModelMatchExact, []string{id}, &objects.ModelAssociation{
			Type: "channel_tags_model", ChannelTagsModel: &objects.ChannelTagsModelAssociation{ChannelTags: []string{"OpenAI"}, ModelID: id},
		})
	}
	channels := newTestChannelServiceForChannels(client)
	system := newTestSystemService(client)
	models := biz.NewModelService(biz.ModelServiceParams{Ent: client, ChannelService: channels, SystemService: system})
	selector := NewDefaultSelector(channels, models, system)
	for _, tc := range []struct {
		name                 string
		keyTags, projectTags []string
		wantChannel          string
	}{
		{"Codex only", []string{"Codex"}, nil, "Codex"},
		{"OpenAI only", []string{"OpenAI"}, nil, "OpenAI"},
		{"both prefer exact", []string{"OpenAI", "Codex"}, nil, "OpenAI"},
		{"project narrows key", []string{"OpenAI", "Codex"}, []string{"Codex"}, "Codex"},
		{"disjoint scopes", []string{"OpenAI"}, []string{"Codex"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "key", Profiles: []objects.APIKeyProfile{{Name: "key", ChannelTags: tc.keyTags}}}}
			key.Edges.Project = &ent.Project{Profiles: &objects.ProjectProfiles{ActiveProfile: "project", Profiles: []objects.ProjectProfile{{Name: "project", ChannelTags: tc.projectTags}}}}
			requestCtx := contexts.WithAPIKey(ctx, key)
			listed, err := models.ListEnabledModels(requestCtx)
			require.NoError(t, err)
			if tc.wantChannel == "" {
				require.Empty(t, listed)
			} else {
				require.Len(t, listed, 2)
			}
			for _, id := range []string{older, newer} {
				candidates, err := selector.Select(requestCtx, &llm.Request{Model: id})
				if tc.wantChannel == "" {
					require.ErrorIs(t, err, biz.ErrInvalidModel)
					continue
				}
				require.NoError(t, err)
				require.Len(t, candidates, 1)
				require.Equal(t, tc.wantChannel, candidates[0].Channel.Name)
				expected := []string{id}
				expectedRule := id
				if tc.wantChannel == "Codex" {
					expected = []string{newer, older}
					expectedRule = rule.ModelID
				}
				require.Len(t, candidates[0].Models, len(expected))
				for index, actual := range expected {
					require.Equal(t, actual, candidates[0].Models[index].ActualModel)
				}
				item, found := lo.Find(listed, func(item biz.ModelFacade) bool { return item.ID == id })
				require.True(t, found, "missing %s", id)
				require.Equal(t, expectedRule, item.ConfiguredModel.ModelID)
				require.Len(t, item.Connections, 1)
				require.Equal(t, tc.wantChannel, item.Connections[0].Channel.Name)
			}
		})
	}

	openAI := client.Channel.Query().Where(channel.NameEQ("OpenAI")).OnlyX(ctx)
	codex := channels.GetEnabledChannel(client.Channel.Query().Where(channel.NameEQ("Codex")).OnlyX(ctx).ID)
	require.NotNil(t, codex)
	client.Channel.UpdateOne(openAI).SetStatus(channel.StatusDisabled).SaveX(ctx)
	channels.SetEnabledChannelsForTest([]*biz.Channel{codex})
	checkRoute := func(tags []string, wantCodex bool) {
		t.Helper()
		key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "key", Profiles: []objects.APIKeyProfile{{Name: "key", ChannelTags: tags}}}}
		requestCtx := contexts.WithAPIKey(ctx, key)
		listed, err := models.ListEnabledModels(requestCtx)
		require.NoError(t, err)
		candidates, err := selector.Select(requestCtx, &llm.Request{Model: older})
		require.NoError(t, err)
		_, visible := lo.Find(listed, func(item biz.ModelFacade) bool { return item.ID == older })
		require.Equal(t, wantCodex, visible)
		if wantCodex {
			require.Len(t, candidates, 1)
			require.Equal(t, codex.ID, candidates[0].Channel.ID)
			require.Equal(t, newer, candidates[0].Models[0].ActualModel)
		} else {
			require.Empty(t, candidates)
		}
	}
	checkRoute([]string{"OpenAI", "Codex"}, false)
	checkRoute([]string{"Codex"}, true)
	exact := client.Model.Query().Where(model.ModelIDEQ(older)).OnlyX(ctx)
	client.Model.UpdateOne(exact).SetSettings(&objects.ModelSettings{}).SaveX(ctx)
	checkRoute([]string{"OpenAI", "Codex"}, false)
}

func TestDefaultSelectorRuleRequestsKeepTheirModelIDs(t *testing.T) {
	ctx, client := setupTest(t)
	channels := createTestChannels(t, ctx, client)
	modelService := newTestModelService(client)
	channelService := newTestChannelServiceForChannels(client)
	system := newTestSystemService(client)
	rule := client.Model.Create().SetDeveloper("openai").SetModelID("gpt-").SetMatchType(objects.ModelMatchPrefix).
		SetName("Rule").SetIcon("openai").SetGroup("test").SetModelCard(&objects.ModelCard{}).SetStatus(model.StatusEnabled).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{
			Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: channels[0].ID},
		}}}).SaveX(ctx)
	selector := NewDefaultSelector(channelService, modelService, system)
	for _, id := range []string{"gpt-6-luna", "gpt-6.1-luna", "gpt-6-luna"} {
		candidates, err := selector.selectModelCandidates(ctx, &llm.Request{Model: id})
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		require.Equal(t, id, candidates[0].Models[0].ActualModel)
	}
	require.Empty(t, selector.associationCache, "request IDs must not enter a shared rule cache")
	// Inherited developer associations also bind the concrete request ID.
	client.Model.UpdateOne(rule).SetSettings(&objects.ModelSettings{}).SaveX(ctx)
	require.NoError(t, system.SetModelSettings(ctx, biz.SystemModelSettings{DeveloperSettings: []*biz.DeveloperModelSettings{{
		Developer: "openai", Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: channels[0].ID}}},
	}}}))
	settings, err := system.ModelSettings(ctx)
	require.NoError(t, err)
	for _, developer := range []string{"openai", "OpenAI"} {
		updated := client.Model.UpdateOne(rule).SetDeveloper(developer).SaveX(ctx)
		developerCount, modelCount, disabled := effectiveAssociationSourceCounts(settings, updated)
		require.Equal(t, 1, developerCount)
		require.Zero(t, modelCount)
		require.False(t, disabled)
		for _, id := range []string{"gpt-4", "gpt-3.5-turbo"} {
			candidates, err := selector.selectModelCandidates(ctx, &llm.Request{Model: id})
			require.NoError(t, err)
			require.Len(t, candidates, 1)
			require.Equal(t, id, candidates[0].Models[0].ActualModel)
		}
	}
}
