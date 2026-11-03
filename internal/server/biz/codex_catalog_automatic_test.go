package biz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestCodexCatalogAutomaticRefreshAndModelChanges(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var revision atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		body := catalogFixture
		if revision.Load() > 0 {
			body = strings.ReplaceAll(body, "32000", "64000")
		}
		body = strings.ReplaceAll(body, "Fixture model instructions", "instructions for "+r.URL.Query().Get("client_version"))
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("automatic").SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	read := func(version string) map[string]map[string]any {
		t.Helper()
		body, omitted, err := svc.Models(ctx, version)
		require.NoError(t, err)
		require.Zero(t, omitted)
		parsed, err := parseCodexCatalog(body)
		require.NoError(t, err)
		return parsed
	}
	require.Equal(t, float64(32000), read("0.166.0")["test-model"]["context_window"])
	revision.Store(1)
	require.Equal(t, float64(64000), read("0.166.0")["test-model"]["context_window"])
	require.Equal(t, "instructions for 0.167.0", read("0.167.0")["test-model"]["base_instructions"])
	createConfiguredModel(t, ctx, client, "new-alias", model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "test-model"}})
	require.Contains(t, read("0.167.0"), "new-alias", "ordinary model configuration is sufficient")
	for _, m := range client.Model.Query().AllX(ctx) {
		require.Empty(t, m.Settings.CodexCatalog)
	}
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ModelIDs: []string{"new-alias"}}}}}
	body, _, err := svc.Models(contexts.WithAPIKey(ctx, key), "0.167.0")
	require.NoError(t, err)
	parsed, err := parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	require.Contains(t, parsed, "new-alias")
	key.Profiles.Profiles[0].ModelIDs = []string{"revoked"}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.167.0")
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(body))
}

func TestCodexCatalogAutomaticSourcePriorityAndToolRoutes(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	metadata := parsed["test-model"]
	metadata["apply_patch_tool_type"] = "freeform"
	metadata["base_instructions"] = "preferred instructions"
	catalog, err := json.Marshal(map[string]any{"models": []any{metadata}})
	require.NoError(t, err)
	var associations []*objects.ModelAssociation
	var ids []int
	for i, name := range []string{"lower-priority", "preferred", "list-only"} {
		body := catalog
		if i == 0 {
			body = []byte(strings.ReplaceAll(string(catalog), "preferred instructions", "other instructions"))
		}
		if i == 2 {
			body = []byte(`{"models":[]}`)
		}
		ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(name).SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"test-model", "unknown"}).SetDefaultTestModel("test-model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
		ids = append(ids, ch.ID)
		priority := 5
		if i == 1 {
			priority = 1
		}
		associations = append(associations, &objects.ModelAssociation{Type: "channel_request", Priority: priority, ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	}
	createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, associations[0])
	m := client.Model.Query().OnlyX(ctx)
	m.Settings.Associations = associations
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelIDs: []int{ids[2]}}}}}
	body, omitted, err := svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	parsed, err = parseCodexCatalog(body)
	require.NoError(t, err)
	require.Equal(t, "preferred instructions", parsed["test-model"]["base_instructions"])
	t.Logf("automatic wire catalog: %s", body)
	visible, err := models.ListEnabledModels(contexts.WithAPIKey(ctx, key))
	require.NoError(t, err)
	require.Len(t, visible, 1)
	_, err = automaticCodexModel(ctx, visible[0], visible[0].Connections,
		map[int]map[string]map[string]any{ids[2]: {"test-model": metadata}}, map[int]*Channel{}, "0.166.0")
	require.ErrorContains(t, err, "no confirmed visible route", "a channel disabled during the read must not panic or remain advertised")
	// A Responses endpoint on the channel does not override a model-specific
	// Chat Completions restriction. Match the actual request's protocol choice.
	client.Channel.UpdateOneID(ids[2]).SetEndpoints([]objects.ChannelEndpoint{
		{APIFormat: "openai/responses", CatalogJSON: []byte(`{"models":[]}`)},
		{APIFormat: "openai/chat_completions"},
	}).SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "test-model", APIFormats: []string{"openai/chat_completions"}}}}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	_, omitted, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
	require.Error(t, err)
	require.Equal(t, 1, omitted)
	// Explicit associations also define opaque aliases without their own catalog.
	client.Channel.UpdateOneID(ids[2]).SetSettings(&objects.ChannelSettings{}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	m.Settings.Associations = []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ids[1], ModelID: "test-model"}}, {Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ids[2], ModelID: "unknown"}}}
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	body, omitted, err = svc.Models(ctx, "0.166.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	parsed, err = parseCodexCatalog(body)
	require.NoError(t, err)
	require.Equal(t, "preferred instructions", parsed["test-model"]["base_instructions"])
}

func TestCodexCatalogUsesCompleteAssociatedCatalog(t *testing.T) {
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	old := parsed["test-model"]
	var current map[string]any
	raw, err := json.Marshal(old)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &current))
	current["slug"] = "new-model"
	current["base_instructions"] = "new-model instructions"
	current["context_window"] = float64(64000)
	source := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses}
	listOnly := &ent.Channel{ID: 2, Type: channel.TypeOpenaiResponses}
	connections := []*ModelChannelConnection{
		{Channel: source, Models: []ChannelModelEntry{{ActualModel: "test-model"}}},
		{Channel: listOnly, Models: []ChannelModelEntry{{ActualModel: "new-model"}}},
	}
	m := ModelFacade{
		ID:              "client-alias",
		TargetID:        "new-model",
		Connections:     connections[1:],
		ConfiguredModel: &ent.Model{ModelID: "new-model"},
	}
	result, err := automaticCodexModel(t.Context(), m, connections,
		map[int]map[string]map[string]any{1: {"test-model": old, "new-model": current}},
		map[int]*Channel{1: {Channel: source}, 2: {Channel: listOnly}}, "0.166.0")
	require.NoError(t, err)
	require.Equal(t, "client-alias", result["slug"])
	require.Equal(t, "new-model instructions", result["base_instructions"])
	require.Equal(t, float64(64000), result["context_window"])
}
