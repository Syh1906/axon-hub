package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

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
