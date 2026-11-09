package gql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestQueryChannelsMultipleModelsGraphQL(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	service := biz.NewChannelServiceForTest(client)
	defer service.Stop()

	for _, fixture := range []struct {
		name     string
		model    string
		status   channel.Status
		tag      string
		settings *objects.ChannelSettings
	}{
		{"direct", "gpt-4", channel.StatusEnabled, "A", nil},
		{"mapped", "claude", channel.StatusDisabled, "B", &objects.ChannelSettings{
			ModelMappings: []objects.ModelMapping{{From: "alias", To: "claude"}},
		}},
		{"other", "other-model", channel.StatusEnabled, "A", nil},
		{"archived", "gpt-4", channel.StatusArchived, "A", nil},
	} {
		client.Channel.Create().SetName(fixture.name).SetType(channel.TypeOpenai).
			SetStatus(fixture.status).SetCredentials(objects.ChannelCredentials{}).
			SetSupportedModels([]string{fixture.model}).SetDefaultTestModel(fixture.model).
			SetTags([]string{fixture.tag}).SetSettings(fixture.settings).SaveX(ctx)
	}
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client, channelService: service}}))
	for _, tc := range []struct {
		name     string
		input    map[string]any
		expected []string
	}{
		{"multiple models", map[string]any{"models": []string{"gpt-4", "alias"}}, []string{"direct", "mapped"}},
		{"tag intersection", map[string]any{"models": []string{"gpt-4", "alias"}, "hasTag": "B"}, []string{"mapped"}},
		{"no matches", map[string]any{"models": []string{"missing"}}, nil},
		{"cleared models", map[string]any{}, []string{"direct", "mapped", "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.input["where"] = map[string]any{"statusIn": []string{"enabled", "disabled"}}
			body, err := json.Marshal(map[string]any{
				"query":     `query Channels($input: QueryChannelInput!) { queryChannels(input: $input) { totalCount edges { node { name } } } }`,
				"variables": map[string]any{"input": tc.input},
			})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/admin/graphql", bytes.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			require.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Errors json.RawMessage
				Data   struct {
					QueryChannels struct {
						TotalCount int
						Edges      []struct{ Node struct{ Name string } }
					}
				}
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.Empty(t, payload.Errors, response.Body.String())
			names := make([]string, 0, len(payload.Data.QueryChannels.Edges))
			for _, edge := range payload.Data.QueryChannels.Edges {
				names = append(names, edge.Node.Name)
			}
			require.ElementsMatch(t, tc.expected, names)
			require.Equal(t, len(tc.expected), payload.Data.QueryChannels.TotalCount)
		})
	}
}
