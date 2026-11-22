package gql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestUpstreamTransportSettingsAccessAndPersistence(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:transportsettings?mode=memory&_fk=1")
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	r := &Resolver{client: db, systemService: biz.NewSystemService(biz.SystemServiceParams{Ent: db})}
	member := func(scopes ...string) context.Context {
		ctx := authz.NewUserContext(ent.NewContext(t.Context(), db), 101)
		return contexts.WithUser(ctx, &ent.User{ID: 101, Scopes: scopes})
	}
	reader, writer := member("read_settings"), member("write_settings")
	initial, err := r.Query().UpstreamTransportSettings(reader)
	require.NoError(t, err)
	require.Equal(t, biz.DefaultUpstreamTransportSettings(), *initial)
	_, err = r.Mutation().SetUpstreamHTTPProtocol(reader, httpclient.ProtocolHTTP1)
	require.Error(t, err)
	_, err = r.Mutation().SetUpstreamAutomaticCompression(reader, false)
	require.Error(t, err)
	_, err = r.Query().UpstreamTransportSettings(member())
	require.Error(t, err)
	_, err = r.Mutation().SetUpstreamAutomaticCompression(writer, false)
	require.NoError(t, err)
	_, err = r.Mutation().SetUpstreamHTTPProtocol(writer, httpclient.ProtocolPreferHTTP2)
	require.NoError(t, err)
	_, err = r.Mutation().SetUpstreamHTTPProtocol(writer, "invalid")
	require.Error(t, err)
	saved, err := r.Query().UpstreamTransportSettings(reader)
	require.NoError(t, err)
	require.Equal(t, biz.UpstreamTransportSettings{Protocol: httpclient.ProtocolPreferHTTP2}, *saved)
}
