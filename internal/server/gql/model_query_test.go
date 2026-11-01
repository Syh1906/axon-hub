package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func TestModelCatalogQueriesAvoidRepeatedAndUnneededData(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:model_catalog_projection?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	client.Channel.Create().SetName("catalog").SetType("openai").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetStatus("enabled").SetSupportedModels([]string{"upstream"}).SetDefaultTestModel("upstream").SaveX(ctx)
	for i := range 3 {
		client.Model.Create().SetDeveloper("fixture").SetModelID(fmt.Sprintf("model-%d", i)).SetName(fmt.Sprintf("model-%d", i)).SetIcon("fixture").
			SetGroup("fixture").SetStatus("enabled").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{{Type: "model", ModelID: &objects.ModelIDAssociation{ModelID: "upstream"}}},
		}).SaveX(ctx)
	}
	var mu sync.Mutex
	var statements []string
	debug := ent.NewClient(ent.Driver(client.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		mu.Lock()
		defer mu.Unlock()
		statements = append(statements, fmt.Sprint(args...))
	}))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{
		client: debug, modelService: biz.NewModelService(biz.ModelServiceParams{Ent: debug}),
	}}))
	server.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		return next(biz.WithModelAssociationQueryCache(ctx))
	})
	query := func(document string) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": document})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body))).WithContext(ent.NewContext(ctx, debug))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.NotContains(t, payload, "errors", response.Body.String())
		return payload
	}
	query(`{models(first:3){edges{node{id associatedChannelCount}}}}`)
	var channelReads int
	for _, statement := range statements {
		if strings.Contains(statement, "FROM `channels`") {
			channelReads++
			require.NotContains(t, statement, "credentials")
		}
	}
	require.Equal(t, 1, channelReads)
	statements = nil
	payload := query(`{models(first:2,orderBy:{field:NAME,direction:ASC},where:{statusIn:[enabled],typeIn:[chat]}){edges{node{id modelID name}}pageInfo{hasNextPage endCursor}}}`)
	models := payload["data"].(map[string]any)["models"].(map[string]any)
	require.Len(t, models["edges"], 2)
	require.Equal(t, true, models["pageInfo"].(map[string]any)["hasNextPage"])
	for _, statement := range statements {
		require.NotContains(t, statement, "FROM `channels`")
		require.NotContains(t, statement, "model_card")
		require.NotContains(t, statement, "settings")
	}
}
