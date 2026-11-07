package biz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestCodexCatalogResponsesTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "websocket", "dual"} {
		for _, source := range []string{"json", "url", "native"} {
			for _, oauth := range []bool{false, true} {
				name := transport + "/" + source + "/api_key"
				if oauth {
					name = transport + "/" + source + "/oauth"
				}
				t.Run(name, func(t *testing.T) {
					ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
					parsed, err := parseCodexCatalog([]byte(catalogFixture))
					require.NoError(t, err)
					parsed["test-model"]["apply_patch_tool_type"] = "freeform"
					parsed["test-model"]["tool_mode"] = "code_mode_only"
					body, err := json.Marshal(map[string]any{"models": []any{parsed["test-model"]}})
					require.NoError(t, err)
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						require.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
						path := "/models"
						if source == "url" {
							path = "/catalog"
						}
						require.Equal(t, path, r.URL.Path)
						_, _ = w.Write(body)
					}))
					defer server.Close()
					ep := objects.ChannelEndpoint{APIFormat: "openai/responses-ws"}
					if transport == "legacy" {
						ep.APIFormat = "openai/responses"
						ep.Transport = "websocket"
					}
					if source == "json" {
						ep.CatalogJSON = body
					}
					if source == "url" {
						ep.CatalogURL = server.URL + "/catalog"
					}
					endpoints := []objects.ChannelEndpoint{ep}
					if transport == "dual" {
						endpoints = append([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}, endpoints...)
					}
					credentials := objects.ChannelCredentials{APIKey: "fixture"}
					if oauth {
						credentials = objects.ChannelCredentials{OAuth: &objects.OAuthCredentials{AccessToken: "fixture", ExpiresAt: time.Now().Add(time.Hour)}}
					}
					ch := client.Channel.Create().SetType(channel.TypeCodex).SetName(name).
						SetBaseURL(server.URL).SetCredentials(credentials).SetSupportedModels([]string{"test-model"}).
						SetDefaultTestModel("test-model").SetEndpoints(endpoints).SetStatus(channel.StatusEnabled).SaveX(ctx)
					createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, &objects.ModelAssociation{
						Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "test-model"},
					})
					built, err := channels.buildChannelWithOutbounds(ch)
					require.NoError(t, err)
					channels.SetEnabledChannelsForTest([]*Channel{built})
					priority := 1
					if source == "json" {
						priority = 0
					}
					require.Equal(t, priority, codexCatalogSourcePriority(built))
					svc := NewCodexCatalogService(models, channels)
					_, err = svc.channelCatalog(ctx, channels.GetEnabledChannels()[0], "0.166.0")
					require.NoError(t, err)
					result, omitted, err := svc.Models(ctx, "0.166.0")
					require.NoError(t, err)
					require.Zero(t, omitted)
					metadata, err := parseCodexCatalog(result)
					require.NoError(t, err)
					require.Contains(t, metadata, "test-model")
					require.Equal(t, "freeform", metadata["test-model"]["apply_patch_tool_type"])
					require.Equal(t, "code_mode_only", metadata["test-model"]["tool_mode"])
					if source == "json" {
						require.Zero(t, calls.Load())
					} else {
						require.Positive(t, calls.Load())
					}
				})
			}
		}
	}
}

func TestCodexOfficialCatalogWebSocketOnly(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var err error
	models.codexSnapshots, err = NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeCodex).SetName("ws-official").
		SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"gpt-6.1-terra"}).SetDefaultTestModel("gpt-6.1-terra").
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses-ws", CatalogJSON: []byte(`{"models":[]}`)}}).
		SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "gpt-6.1-terra", model.TypeChat, &objects.ModelAssociation{
		Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID},
	})
	built, err := channels.buildChannelWithOutbounds(ch)
	require.NoError(t, err)
	channels.SetEnabledChannelsForTest([]*Channel{built})
	body, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, "0.167.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.True(t, strings.Contains(string(body), "gpt-6.1-terra"))
}
