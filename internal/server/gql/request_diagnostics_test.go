package gql

import (
	"context"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRequestSelectionRequiresChannelPermission(t *testing.T) {
	resolver := &requestExecutionResolver{}
	record := &ent.RequestExecution{Selection: &objects.RequestExecutionSelection{ChannelName: "private-channel", KeyRemark: "private-remark"}}
	viewer := &ent.User{ID: 1, Scopes: []string{"read_requests"}}
	ctx := contexts.WithUser(context.Background(), viewer)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	value, err := resolver.Selection(ctx, record)
	require.NoError(t, err)
	require.Nil(t, value)
	viewer.Scopes = append(viewer.Scopes, "read_channels")
	value, err = resolver.Selection(ctx, record)
	require.NoError(t, err)
	require.Equal(t, record.Selection, value)
}

func TestRequestLogTimezoneWithoutSettingsPermission(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:request_log_timezone?mode=memory&_fk=0")
	defer client.Close()
	setup := authz.WithTestBypass(context.Background())
	writer := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	require.NoError(t, writer.SetGeneralSettings(setup, biz.SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	// A cold service must read the stored timezone without relying on an owner's cache.
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	resolver := &queryResolver{&Resolver{systemService: system}}
	viewer := &ent.User{ID: 1, Scopes: []string{"read_requests"}}
	ctx := contexts.WithUser(context.Background(), viewer)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	timezone, err := resolver.RequestLogTimezone(ctx)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", timezone)
	viewer.Scopes = nil
	viewer.Edges.ProjectUsers = []*ent.UserProject{{ProjectID: 7, Scopes: []string{"read_requests"}}}
	projectCtx := contexts.WithProjectID(ctx, 7)
	timezone, err = resolver.RequestLogTimezone(projectCtx)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", timezone)
	_, err = resolver.RequestLogTimezone(contexts.WithProjectID(ctx, 8))
	require.Error(t, err)
	// contexts share a container: clear the project before testing absent scope.
	ctx = contexts.WithProjectID(ctx, 0)
	_, err = resolver.RequestLogTimezone(ctx)
	require.Error(t, err)
	_, err = resolver.RequestLogTimezone(context.Background())
	require.Error(t, err)
}
