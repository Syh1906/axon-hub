package biz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

const catalogFixture = `{"models":[{"slug":"test-model","display_name":"Test","description":"fixture","supported_reasoning_levels":[{"effort":"medium","description":"Medium"}],"default_reasoning_level":"medium","shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"base_instructions":"Fixture model instructions","support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],"input_modalities":["text"],"context_window":32000,"upgrade":null}]}`

func TestCodexCatalogOverlappingRulesRespectChannelScope(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	const id = "test-model"
	for _, tag := range []string{"Codex", "OpenAI"} {
		body := strings.Replace(catalogFixture, "Fixture model instructions", tag+" instructions", 1)
		client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(tag).SetTags([]string{tag}).
			SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
			SetSupportedModels([]string{id}).SetDefaultTestModel(id).SetStatus(channel.StatusEnabled).
			SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(body)}}).SaveX(ctx)
	}
	reloadEnabledChannels(t, ctx, client, channels)
	exact := createRoutingRule(t, ctx, models, "exact", id, objects.ModelMatchExact, 0, []string{id}, &objects.ModelSettings{
		Associations: []*objects.ModelAssociation{{Type: "channel_tags_model", ChannelTagsModel: &objects.ChannelTagsModelAssociation{ChannelTags: []string{"OpenAI"}, ModelID: id}}},
	})
	createRoutingRule(t, ctx, models, "regex", "^test-.*$", objects.ModelMatchRegex, 0, []string{id}, &objects.ModelSettings{
		Associations: []*objects.ModelAssociation{{Type: "channel_tags_regex", ChannelTagsRegex: &objects.ChannelTagsRegexAssociation{ChannelTags: []string{"Codex"}, Pattern: "^test-.*$"}}},
	})
	catalog := NewCodexCatalogService(models, channels)
	read := func(tags ...string) map[string]map[string]any {
		t.Helper()
		key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelTags: tags}}}}
		body, omitted, err := catalog.Models(contexts.WithAPIKey(ctx, key), "0.162.1")
		require.NoError(t, err)
		require.Zero(t, omitted)
		result, err := parseCodexCatalog(body)
		require.NoError(t, err)
		return result
	}
	require.Equal(t, "Codex instructions", read("Codex")[id]["base_instructions"])
	require.Equal(t, "OpenAI instructions", read("OpenAI")[id]["base_instructions"])
	require.Equal(t, "OpenAI instructions", read("Codex", "OpenAI")[id]["base_instructions"])
	client.Model.UpdateOne(exact).SetPublicModelIds([]string{}).SaveX(ctx)
	require.Empty(t, read("OpenAI", "Codex"), "the regex must not publish a hidden in-scope exact model")
	require.Contains(t, read("Codex"), id)
	client.Model.UpdateOne(exact).SetPublicModelIds([]string{id}).SaveX(ctx)
	openAI := client.Channel.Query().Where(channel.NameEQ("OpenAI")).OnlyX(ctx)
	client.Channel.UpdateOne(openAI).SetSupportedModels([]string{"different-model"}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	require.Empty(t, read("OpenAI", "Codex"), "a missing upstream model must not switch configurations")
	require.Contains(t, read("Codex"), id)
}

func TestCodexCatalogPreservesMetadataFiltersAndVersionCache(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "Bearer catalog-fixture", r.Header.Get("Authorization"))
		require.NotEmpty(t, r.URL.Query().Get("client_version"))
		require.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Cache-Control", "private, max-age=60")
		_, _ = w.Write([]byte(catalogFixture))
	}))
	defer server.Close()
	ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName("catalog").SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "catalog-fixture"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "test-model"}})
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	body, omitted, err := svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.Zero(t, omitted)
	var result struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	require.Len(t, result.Models, 1)
	require.Equal(t, "Fixture model instructions", result.Models[0]["base_instructions"])
	_, _, err = svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	_, _, err = svc.Models(ctx, "0.163.0")
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load())
	aliasKey := &ent.APIKey{Profiles: &objects.APIKeyProfiles{
		ActiveProfile: "catalog",
		Profiles:      []objects.APIKeyProfile{{Name: "catalog", ModelIDs: []string{"public-alias"}, ModelMappings: []objects.ModelMapping{{From: "public-alias", To: "test-model"}}}},
	}}
	body, omitted, err = svc.Models(contexts.WithAPIKey(ctx, aliasKey), "0.162.1")
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.NoError(t, json.Unmarshal(body, &result))
	require.Len(t, result.Models, 1)
	require.Equal(t, "public-alias", result.Models[0]["slug"])
	require.Equal(t, int32(2), calls.Load(), "public aliases can share upstream metadata, never visibility")
	aliasKey.Edges.Project = &ent.Project{Profiles: &objects.ProjectProfiles{
		ActiveProfile: "restricted", Profiles: []objects.ProjectProfile{{Name: "restricted", ChannelIDs: []int{ch.ID + 1}}},
	}}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, aliasKey), "0.162.1")
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(body), "a warm metadata cache must not bypass project admission")
	client.Model.Update().SetStatus(model.StatusDisabled).ExecX(ctx)
	body, _, err = svc.Models(ctx, "0.162.1")
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(body))
}

func TestCodexCatalogValidationAndCapabilityIntersection(t *testing.T) {
	_, err := parseCodexCatalog([]byte(`{"data":[{"id":"test"}]}`))
	require.Error(t, err)
	models, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	one := models["test-model"]
	two, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	two["test-model"]["context_window"] = float64(16000)
	two["test-model"]["supported_reasoning_levels"] = []any{}
	merged, err := mergeCodexModels("public-alias", []map[string]any{one, two["test-model"]})
	require.NoError(t, err)
	require.Equal(t, "public-alias", merged["slug"])
	require.Equal(t, float64(16000), merged["context_window"])
	require.Nil(t, merged["default_reasoning_level"])
	require.Equal(t, float64(32000), one["context_window"], "merging must not mutate cached metadata")
	two["test-model"]["base_instructions"] = "different model"
	_, err = mergeCodexModels("public-alias", []map[string]any{one, two["test-model"]})
	require.Error(t, err)
}

func TestCodexCatalogEndpointConfiguration(t *testing.T) {
	valid := objects.ChannelEndpoint{APIFormat: "openai/responses", SupportsWebSockets: lo.ToPtr(true), CatalogJSON: objects.JSONRawMessage(catalogFixture)}
	require.NoError(t, ValidateEndpoints([]objects.ChannelEndpoint{valid}))
	valid.CatalogJSON = objects.JSONRawMessage(`"invalid"`)
	require.Error(t, ValidateEndpoints([]objects.ChannelEndpoint{valid}))
	valid.CatalogJSON = nil
	valid.CatalogURL = "https://user:password@example.com/models"
	require.Error(t, ValidateEndpoints([]objects.ChannelEndpoint{valid}))
	legacy := objects.ChannelEndpoint{APIFormat: "openai/responses", Transport: "websocket"}
	require.True(t, legacy.WebSocketsSupported())
	legacy.SupportsWebSockets = lo.ToPtr(false)
	require.False(t, legacy.WebSocketsSupported())
	require.True(t, (objects.ChannelEndpoint{BaseURL: " WSS://example.com/v1 "}).WebSocketsSupported())
}

func TestCodexCatalogCacheFreshness(t *testing.T) {
	for _, tt := range []struct {
		control string
		age     string
		want    time.Duration
	}{
		{"private, max-age=60", "10", 50 * time.Second},
		{"max-age=3600", "", 5 * time.Minute},
		{"no-store", "", 0},
		{"no-cache, max-age=60", "", 0},
		{"max-age=10", "20", 0},
		{"max-age=0", "", 0},
	} {
		t.Run(tt.control+tt.age, func(t *testing.T) {
			h := make(http.Header)
			h.Set("Cache-Control", tt.control)
			h.Set("Age", tt.age)
			require.Equal(t, tt.want, catalogCacheTTL(h))
		})
	}
}

func TestCodexCatalogDefaultURL(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     channel.Type
		base     string
		endpoint objects.ChannelEndpoint
		want     string
	}{
		{"official API", channel.TypeOpenai, "https://api.openai.com/v1", objects.ChannelEndpoint{}, "https://chatgpt.com/backend-api/codex/models"},
		{"official WS", channel.TypeOpenaiResponses, "wss://api.openai.com/v1#", objects.ChannelEndpoint{}, "https://chatgpt.com/backend-api/codex/models"},
		{"official Codex", channel.TypeCodex, "https://chatgpt.com", objects.ChannelEndpoint{}, "https://chatgpt.com/backend-api/codex/models"},
		{"proxy retains credentials destination", channel.TypeOpenaiResponses, "https://proxy.example", objects.ChannelEndpoint{}, "https://proxy.example/v1/models"},
		{"endpoint exact base", channel.TypeOpenaiResponses, "https://proxy.example/v1", objects.ChannelEndpoint{BaseURL: "https://other.example/custom#"}, "https://other.example/custom/models"},
		{"endpoint overrides official", channel.TypeOpenai, "https://api.openai.com", objects.ChannelEndpoint{BaseURL: "https://proxy.example/v1"}, "https://proxy.example/v1/models"},
		{"explicit catalog", channel.TypeOpenai, "https://api.openai.com", objects.ChannelEndpoint{CatalogURL: "https://catalog.example/codex/models"}, "https://catalog.example/codex/models"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ch := &Channel{Channel: &ent.Channel{Type: tt.kind, BaseURL: tt.base}}
			require.Equal(t, tt.want, codexCatalogURL(ch, tt.endpoint))
		})
	}
}
