package biz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
)

func TestModelDeveloperPersistence(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"OpenAI", "openai"}, {" OPENAI ", "openai"}, {"Anthropic", "anthropic"},
		{"MyProvider", "MyProvider"}, {"myprovider", "myprovider"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()
			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			svc := &ModelService{AbstractService: &AbstractService{db: client}}
			input := ent.CreateModelInput{Developer: tc.input, ModelID: "developer-single", Name: "Single", Icon: "test", Group: "test", ModelCard: &objects.ModelCard{}, Settings: &objects.ModelSettings{}}
			created, err := svc.CreateModel(ctx, input)
			require.NoError(t, err)
			require.Equal(t, tc.want, created.Developer)
			input.ModelID = "developer-bulk"
			input.Name = "Bulk"
			bulk, err := svc.BulkCreateModels(ctx, []*ent.CreateModelInput{&input})
			require.NoError(t, err)
			require.Equal(t, tc.want, bulk[0].Developer)
			_, err = svc.UpdateModel(ctx, created.ID, &ent.UpdateModelInput{Developer: lo.ToPtr(tc.input)})
			require.NoError(t, err)
			persisted, err := client.Model.Get(ctx, created.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, persisted.Developer)
		})
	}
}

func TestModelDeveloperInheritance(t *testing.T) {
	settings := &SystemModelSettings{DeveloperSettings: []*DeveloperModelSettings{
		{Developer: "openai", Associations: []*objects.ModelAssociation{{Type: "channel_model", Priority: 2, ChannelModel: &objects.ChannelModelAssociation{ChannelID: 1}}}},
		{Developer: "OpenAI", Associations: []*objects.ModelAssociation{{Type: "channel_model", Priority: 1, ChannelModel: &objects.ChannelModelAssociation{ChannelID: 2}}}},
		{Developer: "Custom", Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: 3}}}},
	}}
	for _, developer := range []string{"openai", "OpenAI", " OPENAI "} {
		inherited := EffectiveModelAssociations(settings, &ent.Model{Developer: developer, ModelID: "gpt-demo"})
		require.Len(t, inherited, 2)
		require.Equal(t, 2, inherited[0].ChannelModel.ChannelID)
		require.Equal(t, "gpt-demo", inherited[0].ChannelModel.ModelID)
		require.Equal(t, 1, inherited[1].ChannelModel.ChannelID)
	}
	require.Len(t, DeveloperModelAssociations(settings, "Custom"), 1)
	require.Empty(t, DeveloperModelAssociations(settings, "custom"))
	require.Empty(t, EffectiveModelAssociations(settings, &ent.Model{Developer: "OpenAI", ModelID: "gpt-demo", Settings: &objects.ModelSettings{DisableDeveloperSettingsInheritance: true}}))
	normalizeSystemModelSettings(settings)
	require.Equal(t, "openai", settings.DeveloperSettings[1].Developer)
	require.ErrorContains(t, validateSystemModelSettings(settings), "duplicate model developer")
	settings.DeveloperSettings = settings.DeveloperSettings[1:]
	require.NoError(t, validateSystemModelSettings(settings))
}
