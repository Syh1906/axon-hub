package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestPublicModelAPIsPublishConcreteRuleIDs(t *testing.T) {
	client, channelService, system, router, ctx := setupOpenAIRetrieveTest(t)
	ch := client.Channel.Create().SetType("openai").SetName("dynamic").SetBaseURL("https://example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"old"}).SetDefaultTestModel("old").SetStatus("enabled").SaveX(ctx)
	channelService.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})
	rule := client.Model.Create().SetDeveloper("openai").SetModelID("^gpt-.*-luna$").SetMatchType(objects.ModelMatchRegex).
		SetPublicModelIds([]string{"gpt-6-luna", "gpt-6.1-luna"}).SetName("Luna family").SetIcon("openai").SetGroup("gpt").
		SetModelCard(&objects.ModelCard{Limit: objects.ModelCardLimit{Context: 12345}}).SetStatus(model.StatusEnabled).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}}}}).SaveX(ctx)
	service := biz.NewModelService(biz.ModelServiceParams{Ent: client, ChannelService: channelService, SystemService: system})
	anthropic := &AnthropicHandlers{ModelService: service}
	gemini := &GeminiHandlers{ModelService: service}
	router.GET("/anthropic/models", anthropic.ListModels)
	router.GET("/gemini/models", gemini.ListModels)
	for _, tc := range []struct {
		path, list, field string
		want              []string
	}{
		{"/v1/models", "data", "id", []string{"gpt-6-luna", "gpt-6.1-luna"}},
		{"/v1/models?include=all", "data", "id", []string{"gpt-6-luna", "gpt-6.1-luna"}},
		{"/anthropic/models", "data", "id", []string{"gpt-6-luna", "gpt-6.1-luna"}},
		{"/gemini/models", "models", "name", []string{"models/gpt-6-luna", "models/gpt-6.1-luna"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, response.Code)
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			var items []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body[tc.list], &items))
			var ids []string
			for _, item := range items {
				var id string
				require.NoError(t, json.Unmarshal(item[tc.field], &id))
				ids = append(ids, id)
			}
			require.ElementsMatch(t, tc.want, ids)
		})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models/gpt-6.1-luna?include=all", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var details OpenAIModel
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &details))
	require.Equal(t, "gpt-6.1-luna", details.ID)
	require.Equal(t, "Luna family", details.Name)
	require.Equal(t, 12345, details.ContextLength)
	client.Model.UpdateOne(rule).SetPublicModelIds([]string{}).SaveX(ctx)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models/gpt-6.1-luna", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
	_, err := service.ResolveModel(ctx, "gpt-6.1-luna")
	require.NoError(t, err)
}
