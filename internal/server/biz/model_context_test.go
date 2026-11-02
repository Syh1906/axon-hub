package biz

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestModelContextResolution(t *testing.T) {
	for _, tc := range []struct {
		name        string
		model       *objects.ModelContextSettings
		channel     *objects.ChannelSettings
		wantDefault float64
		wantMaximum float64
	}{
		{name: "upstream unchanged", wantDefault: 32000, wantMaximum: 128000},
		{name: "model maximum becomes default", model: &objects.ModelContextSettings{MaxContextWindow: lo.ToPtr(96000)}, wantDefault: 96000, wantMaximum: 96000},
		{name: "separate model values", model: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(16000), MaxContextWindow: lo.ToPtr(96000)}, wantDefault: 16000, wantMaximum: 96000},
		{name: "channel default inherits maximum", model: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(16000), MaxContextWindow: lo.ToPtr(96000)}, channel: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "actual", DefaultContextWindow: lo.ToPtr(24000)}}}, wantDefault: 24000, wantMaximum: 96000},
		{name: "channel maximum inherits model default", model: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(16000)}, channel: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "actual", MaxContextWindow: lo.ToPtr(64000)}}}, wantDefault: 16000, wantMaximum: 64000},
		{name: "channel maximum only", channel: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "actual", MaxContextWindow: lo.ToPtr(64000)}}}, wantDefault: 64000, wantMaximum: 64000},
		{name: "different actual model does not override", channel: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "other", MaxContextWindow: lo.ToPtr(64000)}}}, wantDefault: 32000, wantMaximum: 128000},
		{name: "inherited default respects lower channel maximum", model: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(96000)}, channel: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "actual", MaxContextWindow: lo.ToPtr(64000)}}}, wantDefault: 64000, wantMaximum: 64000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := map[string]any{"context_window": float64(32000), "max_context_window": float64(128000), "base_instructions": "live instructions"}
			result := applyCodexContextSettings(source, &objects.ModelSettings{ContextSettings: tc.model}, tc.channel, "actual")
			require.Equal(t, tc.wantDefault, result["context_window"])
			require.Equal(t, tc.wantMaximum, result["max_context_window"])
			require.Equal(t, "live instructions", result["base_instructions"])
			require.Equal(t, float64(32000), source["context_window"], "must not mutate cached upstream metadata")
		})
	}
	result := applyCodexContextSettings(map[string]any{"max_context_window": float64(64000)}, nil, nil, "actual")
	require.Equal(t, float64(64000), result["context_window"], "missing upstream default also uses maximum")
}

func TestModelContextValidation(t *testing.T) {
	for _, value := range []*objects.ModelContextSettings{
		{DefaultContextWindow: lo.ToPtr(0)}, {MaxContextWindow: lo.ToPtr(-1)},
		{DefaultContextWindow: lo.ToPtr(96000), MaxContextWindow: lo.ToPtr(64000)},
	} {
		require.Error(t, validateModelSettings(&objects.ModelSettings{ContextSettings: value}))
		require.Error(t, validateChannelModelContexts(&objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "m", DefaultContextWindow: value.DefaultContextWindow, MaxContextWindow: value.MaxContextWindow}}}))
	}
	require.Error(t, validateChannelModelContexts(&objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "m"}, {ModelID: "m"}}}))
	require.Error(t, validateChannelModelContexts(&objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: " "}}}))
	require.NoError(t, validateModelSettings(&objects.ModelSettings{ContextSettings: &objects.ModelContextSettings{MaxContextWindow: lo.ToPtr(64000)}}))
}

func TestCodexCatalogContextOverridesAndInheritance(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			var ids []int
			for _, name := range []string{"first", "second"} {
				ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(name).SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(catalogFixture)}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
				ids = append(ids, ch.ID)
			}
			createConfiguredModel(t, ctx, client, "public-alias", model.TypeChat, &objects.ModelAssociation{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "test-model"}})
			m := client.Model.Query().OnlyX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			if manual {
				definition, err := models.PreviewCodexCatalog(ctx, m.ID, "0.166.0")
				require.NoError(t, err)
				m.Settings.CodexCatalog = definition
			}
			m.Settings.ContextSettings = &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(16000), MaxContextWindow: lo.ToPtr(128000)}
			_, err := models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
			require.NoError(t, err)
			svc := NewCodexCatalogService(models, channels)
			read := func(channelIDs []int, wantDefault, wantMaximum float64) {
				t.Helper()
				key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "test", Profiles: []objects.APIKeyProfile{{Name: "test", ChannelIDs: channelIDs}}}}
				body, omitted, err := svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
				require.NoError(t, err)
				require.Zero(t, omitted)
				var catalog struct {
					Models []map[string]any `json:"models"`
				}
				require.NoError(t, json.Unmarshal(body, &catalog))
				require.Len(t, catalog.Models, 1)
				require.Equal(t, "public-alias", catalog.Models[0]["slug"])
				require.Equal(t, wantDefault, catalog.Models[0]["context_window"])
				require.Equal(t, wantMaximum, catalog.Models[0]["max_context_window"])
			}
			read(nil, 16000, 128000)
			_, err = channels.UpdateChannel(ctx, ids[0], &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "test-model", DefaultContextWindow: lo.ToPtr(24000), MaxContextWindow: lo.ToPtr(64000)}}}})
			require.NoError(t, err)
			reloadEnabledChannels(t, ctx, client, channels)
			read([]int{ids[0]}, 24000, 64000)
			read(nil, 16000, 64000)
			_, err = channels.UpdateChannel(ctx, ids[0], &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{}}})
			require.NoError(t, err)
			reloadEnabledChannels(t, ctx, client, channels)
			m.Settings.ContextSettings = &objects.ModelContextSettings{MaxContextWindow: lo.ToPtr(96000)}
			_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
			require.NoError(t, err)
			read(nil, 96000, 96000)
		})
	}
}
