package gql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func TestVersionOrderGraphQLPreview(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:version-preview?mode=memory&_fk=1")
	defer db.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	db.Channel.Create().SetName("version").SetType(channel.TypeOpenai).SetCredentials(objects.ChannelCredentials{APIKey: "test-only"}).SetSupportedModels([]string{"gpt-6.9-sol", "gpt-6.10-sol", "gpt-latest-sol"}).SetDefaultTestModel("gpt-6.9-sol").SetStatus(channel.StatusEnabled).SaveX(ctx)
	svc := biz.NewModelService(biz.ModelServiceParams{Ent: db})
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: db, modelService: svc}}))
	body, err := json.Marshal(map[string]any{"query": `query($rules:[ModelAssociationInput!]!){queryModelChannelConnections(associations:$rules){unrankedModelIDs preferLatestVersion priority models{requestModel actualModel}}}`, "variables": map[string]any{"rules": []any{map[string]any{"type": "regex", "priority": 6, "preferLatestVersion": true, "regex": map[string]any{"pattern": `^gpt-(?<version>.+)-sol$`}}}}})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Equal(t, 200, response.Code)
	var result struct {
		Errors []any `json:"errors"`
		Data   struct {
			Connections []struct {
				UnrankedModelIDs    []string
				PreferLatestVersion bool
				Priority            int
				Models              []struct{ RequestModel string }
			} `json:"queryModelChannelConnections"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Empty(t, result.Errors, response.Body.String())
	require.Len(t, result.Data.Connections, 1)
	conn := result.Data.Connections[0]
	require.Equal(t, []string{"gpt-latest-sol"}, conn.UnrankedModelIDs)
	require.True(t, conn.PreferLatestVersion)
	require.Equal(t, 6, conn.Priority)
	require.Equal(t, "gpt-6.10-sol", conn.Models[0].RequestModel)
	require.Equal(t, "gpt-6.9-sol", conn.Models[1].RequestModel)
}
