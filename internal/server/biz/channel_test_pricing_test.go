package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelTestEntrypointsLoadPricingSnapshot(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ch := client.Channel.Create().SetType("openai").SetName("test-price").SetBaseURL("https://example.test").SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")}}}}
	client.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID("model").SetReferenceID("test-reference").SetPrice(price).SaveX(ctx)
	for _, key := range []string{"", "synthetic-override"} {
		var selected *Channel
		var err error
		if key == "" {
			selected, err = svc.GetChannel(ctx, ch.ID)
		} else {
			selected, err = svc.GetChannelWithKey(ctx, ch.ID, key)
		}
		require.NoError(t, err)
		snapshot := captureRequestCostSnapshot(ctx, client, selected, "model", nil)
		require.Equal(t, "test-reference", snapshot.PriceReferenceID)
		require.NotNil(t, snapshot.CurrentPrice())
	}
}
