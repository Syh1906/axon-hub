package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func TestCodexDefinitionGraphQLSaveReadAndPermission(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:codex-definition-gql?mode=memory&_fk=0")
	defer client.Close()
	ctx := ent.NewContext(authz.WithTestBypass(context.Background()), client)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"slug":"test-model","display_name":"Test","shell_type":"shell_command","visibility":"list","supported_reasoning_levels":[],"experimental_supported_tools":[],"supported_in_api":true,"support_verbosity":false,"priority":1,"truncation_policy":{"mode":"tokens","limit":10000},"base_instructions":"fixture"}]}`))
	}))
	defer upstream.Close()
	ch := client.Channel.Create().SetName("source").SetType("openai").SetBaseURL(upstream.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetStatus("enabled").SaveX(ctx)
	m := client.Model.Create().SetDeveloper("fixture").SetModelID("test-model").SetName("Test").SetIcon("fixture").SetGroup("fixture").SetStatus("enabled").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "test-model"}}}}).SaveX(ctx)
	system := biz.NewSystemService(biz.SystemServiceParams{CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, Ent: client})
	channels := biz.NewChannelServiceForTest(client)
	built, err := channels.GetChannel(ctx, ch.ID)
	require.NoError(t, err)
	channels.SetEnabledChannelsForTest([]*biz.Channel{built})
	snapshots, err := biz.NewCodexSnapshotService(biz.CodexCatalogConfig{Disabled: true}, client)
	require.NoError(t, err)
	service := biz.NewModelService(biz.ModelServiceParams{Ent: client, ChannelService: channels, SystemService: system, CodexSnapshots: snapshots})
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client, modelService: service}}))
	query := func(document string, variables map[string]any) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": document, "variables": variables})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/admin/graphql", strings.NewReader(string(body))).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		require.Equal(t, 200, response.Code)
		var result map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.NotContains(t, result, "errors", response.Body.String())
		return result["data"].(map[string]any)
	}
	id := objects.GUID{Type: "Model", ID: m.ID}
	idText := fmt.Sprintf("gid://axonhub/Model/%d", m.ID)
	preview := query(`query($id:ID!){previewModelCodexCatalog(id:$id,clientVersion:"0.162.1")}`, map[string]any{"id": idText})["previewModelCodexCatalog"]
	associations := []any{map[string]any{"type": "channel_model", "channelModel": map[string]any{"channelId": ch.ID, "modelId": "test-model"}}}
	result := query(`mutation($id:ID!,$input:UpdateModelInput!){updateModel(id:$id,input:$input){settings{codexCatalog}}}`, map[string]any{"id": idText, "input": map[string]any{"settings": map[string]any{"associations": associations, "codexCatalog": preview}}})
	require.Equal(t, preview, result["updateModel"].(map[string]any)["settings"].(map[string]any)["codexCatalog"])
	saved := client.Model.GetX(ctx, m.ID)
	require.NotEmpty(t, saved.Settings.CodexCatalog)
	inherited := map[string]any{"mode": "inherit", "sourceModelId": "test-model", "overrides": map[string]any{"description": "custom"}}
	result = query(`mutation($id:ID!,$input:UpdateModelInput!){updateModel(id:$id,input:$input){settings{codexCatalog}}}`, map[string]any{"id": idText, "input": map[string]any{"settings": map[string]any{"associations": associations, "codexCatalog": inherited}}})
	require.Equal(t, inherited, result["updateModel"].(map[string]any)["settings"].(map[string]any)["codexCatalog"])
	_, err = (&queryResolver{&Resolver{modelService: service}}).PreviewModelCodexCatalog(context.Background(), id, "0.162.1")
	require.Error(t, err)
	query(`mutation($id:ID!,$input:UpdateModelInput!){updateModel(id:$id,input:$input){settings{codexCatalog}}}`, map[string]any{"id": idText, "input": map[string]any{"settings": map[string]any{"associations": associations, "codexCatalog": nil}}})
}
