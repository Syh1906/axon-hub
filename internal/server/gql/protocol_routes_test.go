package gql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/stretchr/testify/require"
)

func TestProtocolRoutesGraphQLPolicyRoundTrip(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:protocol_routes?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}})
	channels := biz.NewChannelServiceForTest(db)
	models := biz.NewModelService(biz.ModelServiceParams{Ent: db, ChannelService: channels, SystemService: system})
	root := &Resolver{client: db, systemService: system, channelService: channels, modelService: models}
	r := &queryResolver{root}
	ch := db.Channel.Create().SetName("A").SetType(channel.TypeAnthropic).SetStatus(channel.StatusEnabled).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"upstream"}).SetDefaultTestModel("upstream").SaveX(ctx)
	m := db.Model.Create().SetModelID("public").SetName("Public").SetDeveloper("test").SetIcon("test").SetGroup("test").SetStatus(model.StatusEnabled).SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "upstream"}}}}).SaveX(ctx)
	page, err := r.ProtocolRoutes(ctx, "openai/chat_completions", "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, 1, page.Items[0].Converted)
	page, err = r.ProtocolRoutes(ctx, "openai/chat_completions", "missing", 0, 20)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: root}))
	id := objects.GUID{Type: "Channel", ID: ch.ID}
	var idJSON bytes.Buffer
	id.MarshalGQL(&idJSON)
	body := []byte(`{"query":"mutation($id:ID!){updateChannelSettings(id:$id,input:{protocolPolicy:{allowedProtocols:[\"anthropic/messages\"],sameProtocolOnly:true}}){settings{protocolPolicy{allowedProtocols sameProtocolOnly}}}}","variables":{"id":` + idJSON.String() + `}}`)
	httpReq := httptest.NewRequest(http.MethodPost, "/admin/graphql", bytes.NewReader(body)).WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, httpReq)
	var result map[string]any
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &result))
	require.NotContains(t, result, "errors", res.Body.String())
	read := db.Channel.GetX(ctx, ch.ID)
	require.True(t, read.Settings.ProtocolPolicy.SameProtocolOnly)
	require.Equal(t, []string{"anthropic/messages"}, read.Settings.ProtocolPolicy.AllowedProtocols)
	page, err = r.ProtocolRoutes(ctx, "openai/chat_completions", "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, page.Items[0].Excluded)
	page, err = r.ProtocolRoutes(ctx, "anthropic/messages", "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, page.Items[0].Native)
	// Evidence matches the input protocol and public ID, not just a channel's
	// unrelated successful request. No prompt/response body is read by the view.
	now := time.Now().Add(time.Second)
	record := db.Request.Create().SetProjectID(1).SetModelID("public").SetFormat(llm.APIFormatAnthropicMessage.String()).SetStatus(request.StatusCompleted).SetRequestBody([]byte(`{}`)).SetStream(false).SaveX(ctx)
	execution := db.RequestExecution.Create().SetRequestID(record.ID).SetProjectID(1).SetChannelID(ch.ID).SetModelID("upstream").SetFormat(llm.APIFormatAnthropicMessage.String()).SetStatus(requestexecution.StatusCompleted).SetRequestBody([]byte(`{}`)).SetStream(false).SetUpdatedAt(now).SaveX(ctx)
	detail, err := r.ProtocolRouteDetail(ctx, "anthropic/messages", "public")
	require.NoError(t, err)
	require.Len(t, detail.Routes, 1)
	require.Len(t, detail.Evidence, 1)
	require.True(t, detail.Evidence[0].LastSuccess.Equal(execution.CreatedAt))
	detail, err = r.ProtocolRouteDetail(ctx, "openai/chat_completions", "public")
	require.NoError(t, err)
	require.Empty(t, detail.Evidence)
	db.Model.UpdateOne(m).SetUpdatedAt(now.Add(time.Second)).SaveX(ctx)
	detail, err = r.ProtocolRouteDetail(ctx, "anthropic/messages", "public")
	require.NoError(t, err)
	require.Empty(t, detail.Evidence)
	// Body GC and other metadata updates refresh UpdatedAt without executing
	// the request again. They must not turn pre-configuration success into proof.
	db.RequestExecution.UpdateOne(execution).SetUpdatedAt(now.Add(2 * time.Second)).SaveX(ctx)
	detail, err = r.ProtocolRouteDetail(ctx, "anthropic/messages", "public")
	require.NoError(t, err)
	require.Empty(t, detail.Evidence)
	db.Model.UpdateOne(m).SetPublicModelIds([]string{}).SaveX(ctx)
	_, err = r.ProtocolRouteDetail(ctx, "anthropic/messages", "public")
	require.ErrorContains(t, err, "not a public chat model")
}

func TestProtocolRoutesRejectProjectOnlyScope(t *testing.T) {
	ctx := authz.NewUserContext(context.Background(), 7)
	ctx = contexts.WithUser(ctx, &ent.User{ID: 7, Edges: ent.UserEdges{ProjectUsers: []*ent.UserProject{{ProjectID: 1, IsOwner: true}}}})
	ctx = contexts.WithProjectID(ctx, 1)
	require.Error(t, authorizeProtocolRoutes(ctx, "openai/responses"))
	require.Error(t, authorizeProtocolRoutes(context.Background(), "openai/responses"))
}

func TestProtocolRoutesRespectNonChatModelPrecedence(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:protocol_routes_types?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}})
	models := biz.NewModelService(biz.ModelServiceParams{Ent: db, ChannelService: biz.NewChannelServiceForTest(db), SystemService: system})
	r := &queryResolver{&Resolver{client: db, systemService: system, modelService: models}}
	db.Model.Create().SetModelID("demo-").SetMatchType(objects.ModelMatchPrefix).SetPublicModelIds([]string{"demo-image", "demo-text"}).SetType(model.TypeChat).
		SetName("Chat rule").SetDeveloper("test").SetIcon("test").SetGroup("test").SetStatus(model.StatusEnabled).SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{}).SaveX(ctx)
	db.Model.Create().SetModelID("demo-image").SetType(model.TypeImageGeneration).
		SetName("Exact image").SetDeveloper("test").SetIcon("test").SetGroup("test").SetStatus(model.StatusEnabled).SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{}).SaveX(ctx)
	resolved, err := models.ResolveModel(ctx, "demo-image")
	require.NoError(t, err)
	require.Equal(t, model.TypeImageGeneration, resolved.Type)
	page, err := r.ProtocolRoutes(ctx, "openai/chat_completions", "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, "demo-text", page.Items[0].ModelID)
	_, err = r.ProtocolRouteDetail(ctx, "openai/chat_completions", "demo-image")
	require.ErrorContains(t, err, "not a public chat model")
}
