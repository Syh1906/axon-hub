package gql

import (
	"bytes"
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestResponsesServiceTierOverrideGraphQLRoundTrip(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	service := biz.NewChannelServiceForTest(client)
	resolver := &mutationResolver{&Resolver{channelService: service}}
	ec := &executionContext{}
	created, err := resolver.CreateChannel(ctx, ent.CreateChannelInput{
		Type: channel.TypeOpenaiResponses, Name: "Tier override", Credentials: objects.ChannelCredentials{APIKey: "fixture"},
		SupportedModels: []string{"test-model"}, DefaultTestModel: "test-model",
		Settings: &objects.ChannelSettings{ExtraModelPrefix: "preserved"},
	})
	require.NoError(t, err)
	require.Equal(t, objects.ResponsesServiceTierSupported, created.Settings.ResponsesServiceTierOverride)
	require.Equal(t, &objects.ResponsesTierSupport{AllowDowngrade: true}, created.Settings.ResponsesTierSupport)
	require.Equal(t, &objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, created.Settings.ServiceTierBilling)
	require.True(t, created.Settings.ResponsesTierRoutingEnabled)
	// Simulate an existing channel written before the new request settings were introduced.
	old := *created.Settings
	old.ResponsesTierRoutingEnabled = false
	old.ResponsesServiceTierOverride = objects.ResponsesServiceTierFast
	client.Channel.UpdateOneID(created.ID).SetSettings(&old).ExecX(ctx)
	_, err = resolver.UpdateChannelSettings(ctx, objects.GUID{Type: "Channel", ID: created.ID}, biz.UpdateChannelSettingsInput{DisableSessionReuse: lo.ToPtr(true)})
	require.NoError(t, err)
	require.False(t, client.Channel.GetX(ctx, created.ID).Settings.ResponsesTierRoutingEnabled)
	for _, tc := range []struct {
		gql    string
		stored objects.ResponsesServiceTierOverride
	}{
		{"STANDARD", objects.ResponsesServiceTierStandard}, {"FAST", objects.ResponsesServiceTierFast}, {"ULTRAFAST", objects.ResponsesServiceTierUltrafast}, {"INHERIT", objects.ResponsesServiceTierInherit},
	} {
		patch, err := ec.unmarshalInputUpdateChannelSettingsInput(ctx, map[string]any{
			"responsesServiceTierOverride":   tc.gql,
			"streamFirstEventTimeoutSeconds": 120, "nonStreamResponseTimeoutSeconds": 0,
			"disableSessionReuse": false, "allowImageGenerationTool": false,
		})
		require.NoError(t, err)
		_, err = resolver.UpdateChannelSettings(ctx, objects.GUID{Type: "Channel", ID: created.ID}, patch)
		require.NoError(t, err)
		read := client.Channel.GetX(ctx, created.ID)
		require.Equal(t, tc.stored, read.Settings.ResponsesServiceTierOverride)
		require.Equal(t, "preserved", read.Settings.ExtraModelPrefix)
		require.Equal(t, lo.ToPtr(120), read.Settings.StreamFirstEventTimeoutSeconds)
		require.Equal(t, lo.ToPtr(0), read.Settings.NonStreamResponseTimeoutSeconds)
		require.False(t, read.Settings.DisableSessionReuse)
		require.Equal(t, lo.ToPtr(false), read.Settings.AllowImageGenerationTool)
		require.Equal(t, created.Settings.ServiceTierBilling, read.Settings.ServiceTierBilling)
		var encoded bytes.Buffer
		read.Settings.ResponsesServiceTierOverride.MarshalGQL(&encoded)
		require.Equal(t, `"`+tc.gql+`"`, encoded.String())
	}
	_, err = ec.unmarshalInputUpdateChannelSettingsInput(ctx, map[string]any{"responsesServiceTierOverride": "BOGUS"})
	require.Error(t, err)
	for _, supportFast := range []bool{true, false} {
		patch, err := ec.unmarshalInputUpdateChannelSettingsInput(ctx, map[string]any{"responsesServiceTierOverride": "SUPPORTED",
			"responsesTierSupport": map[string]any{"fast": supportFast, "ultrafast": false, "allowDowngrade": supportFast}})
		require.NoError(t, err)
		_, err = resolver.UpdateChannelSettings(ctx, objects.GUID{Type: "Channel", ID: created.ID}, patch)
		require.NoError(t, err)
		read := client.Channel.GetX(ctx, created.ID)
		require.Equal(t, objects.ResponsesServiceTierSupported, read.Settings.ResponsesServiceTierOverride)
		require.Equal(t, &objects.ResponsesTierSupport{Fast: supportFast, AllowDowngrade: supportFast}, read.Settings.ResponsesTierSupport)
	}
	_, err = service.UpdateChannelSettings(ctx, created.ID, &biz.UpdateChannelSettingsInput{ResponsesServiceTierOverride: lo.ToPtr(objects.ResponsesServiceTierOverride("invalid"))})
	require.Error(t, err)
	// Full updates are used by the editor when clearing an optional timeout.
	settings, err := ec.unmarshalInputChannelSettingsInput(ctx, map[string]any{"responsesServiceTierOverride": "FAST", "serviceTierBilling": map[string]any{"enabled": true, "basis": "fixed_ultrafast"}, "streamFirstEventTimeoutSeconds": nil})
	require.NoError(t, err)
	_, err = resolver.UpdateChannel(ctx, objects.GUID{Type: "Channel", ID: created.ID}, ent.UpdateChannelInput{Settings: &settings})
	require.NoError(t, err)
	read := client.Channel.GetX(ctx, created.ID)
	require.Equal(t, objects.ResponsesServiceTierFast, read.Settings.ResponsesServiceTierOverride)
	require.Nil(t, read.Settings.StreamFirstEventTimeoutSeconds)
	require.True(t, read.Settings.ResponsesTierRoutingEnabled)
	require.Equal(t, objects.ServiceTierBasisFixedUltrafast, read.Settings.ServiceTierBilling.Basis)
	patch, err := ec.unmarshalInputUpdateChannelSettingsInput(ctx, map[string]any{"serviceTierBilling": map[string]any{"enabled": true, "basis": "fixed_fast"}})
	require.NoError(t, err)
	_, err = resolver.UpdateChannelSettings(ctx, objects.GUID{Type: "Channel", ID: created.ID}, patch)
	require.NoError(t, err)
	require.Equal(t, objects.ServiceTierBasisFixedFast, client.Channel.GetX(ctx, created.ID).Settings.ServiceTierBilling.Basis)
	// The released price dialog always sent its cached channel billing settings.
	multiplier := decimal.NewFromInt(2)
	_, err = resolver.SaveChannelModelPrices(ctx, objects.GUID{Type: "Channel", ID: created.ID}, nil, &multiplier, &objects.ServiceTierSettings{Enabled: false, Basis: objects.ServiceTierBasisUpstream}, nil)
	require.NoError(t, err)
	require.Equal(t, objects.ServiceTierBasisFixedFast, client.Channel.GetX(ctx, created.ID).Settings.ServiceTierBilling.Basis)

	require.Equal(t, "2", client.Channel.GetX(ctx, created.ID).Settings.ModelPriceMultiplier.String())
}
