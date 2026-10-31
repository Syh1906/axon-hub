package biz

import (
	"testing"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

func TestModelStandardPricesMatchRulesAndPreserveChannelOverrides(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	price := func(value int64) *objects.ModelPrice {
		return &objects.ModelPrice{
			Items:        []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(value))}}},
			ServiceTiers: &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))}},
			VolumeTiers:  []objects.ModelPriceVolumeTier{{Above: 272000, LongContext: true, Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(value * 2))}}}}},
		}
	}
	create := func(pattern string, kind objects.ModelMatchType, priority int, status model.Status, value *objects.ModelPrice) *ent.Model {
		return client.Model.Create().SetDeveloper("test").SetModelID(pattern).SetMatchType(kind).SetMatchPriority(priority).
			SetName(pattern + " " + string(kind)).SetIcon("test").SetGroup("test").SetStatus(status).SetSettings(&objects.ModelSettings{}).
			SetPublicModelIds([]string{}).SetModelCard(&objects.ModelCard{Price: value}).SaveX(ctx)
	}
	exact := create("gpt-special", objects.ModelMatchExact, 100, model.StatusEnabled, price(3))
	create("gpt-special", objects.ModelMatchPrefix, 0, model.StatusEnabled, price(99))
	regex := create(`^gpt-.*-sol$`, objects.ModelMatchRegex, 10, model.StatusDisabled, price(2))
	create("gpt-", objects.ModelMatchPrefix, 10, model.StatusEnabled, price(9))
	create("-sol", objects.ModelMatchSuffix, 10, model.StatusEnabled, price(8))
	create("gpt-6.1-sol", objects.ModelMatchExact, 0, model.StatusArchived, price(100))
	create("gpt-no-price", objects.ModelMatchExact, 0, model.StatusEnabled, nil)
	ids := []string{"gpt-6.1-sol", "gpt-special", "gpt-no-price", "missing", "gpt-special", ""}
	matches, err := NewModelService(ModelServiceParams{Ent: client}).MatchModelStandardPrices(ctx, ids)
	require.NoError(t, err)
	require.Len(t, matches, 3)
	require.Equal(t, "gpt-6.1-sol", matches[0].RequestModelID)
	require.Equal(t, regex.ID, matches[0].Model.ID, "same-priority rules use ID order and disabled cards remain eligible for management pricing")
	require.Equal(t, exact.ID, matches[1].Model.ID, "exact wins over an identical prefix string")
	templates, err := svc.resolveChannelModelPriceTemplates(ctx, ids)
	require.NoError(t, err)
	require.Len(t, templates, 2, "do not fall through to lower rules when the winning card has no price")
	require.True(t, templates[0].Price.Equals(*regex.ModelCard.Price))
	require.True(t, templates[1].Price.Equals(*exact.ModelCard.Price))
	ch := createAutoPriceTestChannel(t, ctx, client, "regex pricing", []string{"gpt-6.1-sol", "gpt-special"})
	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "gpt-special", Price: *price(0)}}, nil)
	require.NoError(t, err)
	_, err = svc.ensureChannelModelPrices(ctx, ch.ID, ids)
	require.NoError(t, err)
	require.True(t, queryChannelModelPrice(t, ctx, client, ch.ID, "gpt-special").Price.Items[0].Pricing.UsagePerUnit.IsZero())
	require.True(t, queryChannelModelPrice(t, ctx, client, ch.ID, "gpt-6.1-sol").Price.Equals(*regex.ModelCard.Price))
}
