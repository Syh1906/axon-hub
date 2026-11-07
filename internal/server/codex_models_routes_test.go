package server

import (
	"encoding/json"
	"fmt"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

const routeCatalogModel = `{"slug":%q,"display_name":"Fixture","description":"fixture","supported_reasoning_levels":[{"effort":"medium","description":"Medium"}],"default_reasoning_level":"medium","shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"base_instructions":"Fixture instructions","support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],"input_modalities":["text"],"context_window":32000,"upgrade":null}`

func addRouteCatalog(t *testing.T, h *codexRouteHarness, id, baseURL string, inline bool) *biz.Channel {
	t.Helper()
	ep := objects.ChannelEndpoint{APIFormat: "openai/responses"}
	if inline {
		ep.CatalogJSON = []byte(`{"models":[` + fmt.Sprintf(routeCatalogModel, id) + `]}`)
	}
	source := h.db.Channel.Create().SetName(id).SetType("openai_responses").SetBaseURL(baseURL).SetDefaultTestModel(id).SetSupportedModels([]string{id}).SetCredentials(objects.ChannelCredentials{APIKey: "catalog-fixture"}).SetEndpoints([]objects.ChannelEndpoint{ep}).SaveX(h.ctx)
	ch, err := h.channels.GetChannel(h.ctx, source.ID)
	require.NoError(t, err)
	h.registerModels(t, ch, "chat")
	return ch
}

func TestCodexRoutes_CallerIntersection(t *testing.T) {
	h := newCodexRouteHarness(t)
	a := addRouteCatalog(t, h, "model-a", "https://example.invalid", true)
	b := addRouteCatalog(t, h, "model-b", "https://example.invalid", true)
	h.channels.SetEnabledChannelsForTest([]*biz.Channel{a, b})
	// Both routes read the same multi-source catalog, independent of the optional PAT switch.
	for _, enabled := range []bool{false, true} {
		require.NoError(t, h.system.SetCodexCompatibilitySettings(h.ctx, biz.CodexCompatibilitySettings{Enabled: enabled}))
		for _, id := range []string{"model-a", "model-b"} {
			keyValue := fmt.Sprintf("ah-%s-%t", id, enabled)
			h.db.APIKey.Create().SetName(keyValue).SetKey(keyValue).SetType(apikey.TypeUser).SetUserID(h.key.UserID).SetProjectID(h.key.ProjectID).SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ModelIDs: []string{id}}}}).SaveX(h.ctx)
			result := h.request("GET", "/codex/models?client_version=0.166.0", keyValue)
			require.Equal(t, 200, result.Code, result.Body.String())
			var body struct {
				Models []struct {
					Slug string `json:"slug"`
				} `json:"models"`
			}
			require.NoError(t, json.Unmarshal(result.Body.Bytes(), &body))
			require.Len(t, body.Models, 1)
			require.Equal(t, id, body.Models[0].Slug)
			require.Equal(t, "private, no-store", result.Header().Get("Cache-Control"))
			require.Empty(t, result.Header().Get("ETag"))
			require.Empty(t, result.Header().Get("Set-Cookie"))
		}
	}
}

func TestCodexRoutes_CatalogErrors(t *testing.T) {
	for _, status := range []int{401, 404, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := newCodexRouteHarness(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-provider-body"))
			}))
			defer upstream.Close()
			ch := addRouteCatalog(t, h, "custom-model", upstream.URL, false)
			h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
			result := h.request("GET", "/codex/models?client_version=0.166.0", h.key.Key)
			require.Equal(t, 503, result.Code, result.Body.String())
			require.NotContains(t, result.Body.String(), "private-provider-body")
		})
	}
}

func TestCodexRoutes_CatalogVersionValidation(t *testing.T) {
	h := newCodexRouteHarness(t)
	for _, version := range []string{"", "bad", "1.2"} {
		require.Equal(t, 400, h.request("GET", "/codex/models?client_version="+version, h.key.Key).Code)
	}
}

func TestCodexRoutes_SettingReadFailure(t *testing.T) {
	h := newCodexRouteHarness(t)
	h.db.System.Create().SetKey(biz.SystemKeyCodexCompatibility).SetValue("bad JSON").SaveX(h.ctx)
	require.Equal(t, 500, h.request("GET", "/codex/v1/user-auth-credential/whoami", "").Code)
	require.Equal(t, 200, h.request("GET", "/codex/models?client_version=0.166.0", h.key.Key).Code)
}
