package biz

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/pkg/xcache"
)

func TestSystemService_CurrencyDisplayDecimals(t *testing.T) {
	service, client := setupTestSystemService(t, xcache.Config{Mode: xcache.ModeMemory})
	defer client.Close()
	ctx := ent.NewContext(t.Context(), client)
	admin := authz.WithTestBypass(ctx)

	settings, err := service.GeneralSettings(admin)
	require.NoError(t, err)
	require.Nil(t, settings.CurrencyDisplayDecimals)

	// Existing installations have no precision field in their persisted settings.
	require.NoError(t, service.setSystemValue(admin, SystemKeyGeneralSettings, `{"currency_code":"USD","timezone":"UTC"}`))
	settings, err = service.GeneralSettings(admin)
	require.NoError(t, err)
	require.Nil(t, settings.CurrencyDisplayDecimals)

	member := contexts.WithUser(ctx, &ent.User{ID: 1})
	_, err = service.CurrencyDisplayDecimals(ctx)
	require.ErrorContains(t, err, "authenticated user")
	for _, decimals := range []*int{lo.ToPtr(0), lo.ToPtr(2), lo.ToPtr(8), nil} {
		settings.CurrencyDisplayDecimals = decimals
		require.NoError(t, service.SetGeneralSettings(admin, *settings))
		read, err := service.GeneralSettings(admin)
		require.NoError(t, err)
		require.Equal(t, decimals, read.CurrencyDisplayDecimals)
		preference, err := service.CurrencyDisplayDecimals(member)
		require.NoError(t, err)
		require.Equal(t, decimals, preference)
	}

	for _, invalid := range []int{-1, 9} {
		settings.CurrencyDisplayDecimals = lo.ToPtr(invalid)
		require.ErrorContains(t, service.SetGeneralSettings(admin, *settings), "between 0 and 8")
		read, err := service.GeneralSettings(admin)
		require.NoError(t, err)
		require.Nil(t, read.CurrencyDisplayDecimals)
	}
}
