package biz

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChannelResponseTimeoutSaveAndRestore(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	defer svc.Stop()
	ctx := channelAutoPriceTestContext(client)
	ch := createTestChannel(t, client, ctx, "timeouts", []string{"model"}, nil)
	for _, settings := range []*objects.ChannelSettings{
		{StreamFirstEventTimeoutSeconds: lo.ToPtr(0), NonStreamResponseTimeoutSeconds: lo.ToPtr(900)},
		{StreamFirstEventTimeoutSeconds: lo.ToPtr(12)},
		{},
	} {
		_, err := svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: settings})
		require.NoError(t, err)
		stored := client.Channel.GetX(ctx, ch.ID)
		require.Equal(t, settings.StreamFirstEventTimeoutSeconds, stored.Settings.StreamFirstEventTimeoutSeconds)
		require.Equal(t, settings.NonStreamResponseTimeoutSeconds, stored.Settings.NonStreamResponseTimeoutSeconds)
	}
	_, err := svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: lo.ToPtr(-1)}})
	require.Error(t, err)
	require.Nil(t, client.Channel.GetX(ctx, ch.ID).Settings.NonStreamResponseTimeoutSeconds)
}
