package biz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestChannelService_SaveChannelModelPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create a test channel
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Test Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key1"}).
		SetSupportedModels([]string{"gpt-4", "gpt-3.5-turbo"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	price1 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.01"),
				},
			},
		},
	}

	price2 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeCompletion,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.02"),
				},
			},
		},
	}

	t.Run("batch create", func(t *testing.T) {
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   price1,
			},
			{
				ModelID: "gpt-3.5-turbo",
				Price:   price2,
			},
		}

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs, nil)
		require.NoError(t, err)
		require.Len(t, results, 2)

		for _, res := range results {
			// Check if version is created
			version, err := client.ChannelModelPriceVersion.Query().
				Where(channelmodelpriceversion.ChannelModelPriceID(res.ID)).
				Only(ctx)
			require.NoError(t, err)
			require.Equal(t, channelmodelpriceversion.StatusActive, version.Status)
			require.Equal(t, res.ReferenceID, version.ReferenceID)
			require.Len(t, res.ReferenceID, 8)
		}
	})

	t.Run("batch update and archive old version", func(t *testing.T) {
		// First update gpt-4
		newPrice1 := price1
		newPrice1.Items[0].Pricing.UsagePerUnit = loToDecimalPtr("0.015")

		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   newPrice1,
			},
		}

		// Store old ref id
		oldPrice, err := client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Only(ctx)
		require.NoError(t, err)

		oldRefID := oldPrice.ReferenceID

		// Wait a bit to ensure time difference
		time.Sleep(10 * time.Millisecond)

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)

		updatedPrice := results[0]
		require.NotEqual(t, oldRefID, updatedPrice.ReferenceID)
		require.Equal(t, newPrice1, updatedPrice.Price)

		// Check versions
		versions, err := client.ChannelModelPriceVersion.Query().
			Where(channelmodelpriceversion.ChannelModelPriceID(updatedPrice.ID)).
			Order(ent.Asc(channelmodelpriceversion.FieldEffectiveStartAt)).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, versions, 2)

		// Old version should be archived
		require.Equal(t, channelmodelpriceversion.StatusArchived, versions[0].Status)
		require.NotNil(t, versions[0].EffectiveEndAt)
		require.Equal(t, oldRefID, versions[0].ReferenceID)

		// New version should be active
		require.Equal(t, channelmodelpriceversion.StatusActive, versions[1].Status)
		require.Nil(t, versions[1].EffectiveEndAt)
		require.Equal(t, updatedPrice.ReferenceID, versions[1].ReferenceID)
	})

	t.Run("delete missing models", func(t *testing.T) {
		// Only send gpt-3.5-turbo, gpt-4 should be deleted
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-3.5-turbo",
				Price:   price2,
			},
		}

		// Verify gpt-4 exists before delete
		exists, err := client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Exist(ctx)
		require.NoError(t, err)
		require.True(t, exists)

		results, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs, nil)
		require.NoError(t, err)
		require.Len(t, results, 1) // Only gpt-3.5-turbo remains (as skip/update)

		// Verify gpt-4 is deleted
		exists, err = client.ChannelModelPrice.Query().
			Where(
				channelmodelprice.ChannelID(ch.ID),
				channelmodelprice.ModelID("gpt-4"),
			).Exist(ctx)
		require.NoError(t, err)
		require.False(t, exists)

		// Verify gpt-4 versions are archived
		versions, err := client.ChannelModelPriceVersion.Query().
			Where(
				channelmodelpriceversion.ChannelID(ch.ID),
				channelmodelpriceversion.ModelID("gpt-4"),
			).All(ctx)
		require.NoError(t, err)

		for _, v := range versions {
			require.Equal(t, channelmodelpriceversion.StatusArchived, v.Status)
			require.NotNil(t, v.EffectiveEndAt)
		}
	})

	t.Run("duplicate model id should error", func(t *testing.T) {
		inputs := []SaveChannelModelPriceInput{
			{
				ModelID: "gpt-4",
				Price:   price1,
			},
			{
				ModelID: "gpt-4",
				Price:   price2,
			},
		}

		_, err := svc.SaveChannelModelPrices(ctx, ch.ID, inputs, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duplicate model price input")
		require.Contains(t, err.Error(), "model_id=gpt-4")
	})
}

func loToDecimalPtr(s string) *decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return &d
}

func TestChannelService_DuplicateChannelCopiesModelPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	source, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Source Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key1"}).
		SetSupportedModels([]string{"gpt-4", "gpt-4o"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	price := objects.ModelPrice{
		Multiplier: loToDecimalPtr("0.15"),
		Items: []objects.ModelPriceItem{
			{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing: objects.Pricing{
					Mode:         objects.PricingModeUsagePerUnit,
					UsagePerUnit: loToDecimalPtr("0.01"),
				},
			},
		},
	}

	price.ServiceTiers = serviceTierTestPrice().ServiceTiers
	tierSettings := &objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}
	sourcePrices, err := svc.SaveChannelModelPrices(ctx, source.ID, []SaveChannelModelPriceInput{
		{ModelID: "gpt-4", Price: price},
	}, loToDecimalPtr("0.15"), tierSettings)
	require.NoError(t, err)
	require.Len(t, sourcePrices, 1)

	duplicated, err := svc.DuplicateChannel(ctx, source.ID, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Name:             "Source Channel (1)",
		Credentials:      objects.ChannelCredentials{APIKey: "key2"},
		SupportedModels:  []string{"gpt-4", "gpt-4o"},
		DefaultTestModel: "gpt-4",
	})
	require.NoError(t, err)
	require.Equal(t, "0.15", duplicated.Settings.ModelPriceMultiplier.String())
	require.Equal(t, tierSettings, duplicated.Settings.ServiceTierBilling)

	copiedPrices, err := client.ChannelModelPrice.Query().
		Where(channelmodelprice.ChannelID(duplicated.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, copiedPrices, 1)

	copiedPrice := copiedPrices[0]
	require.Equal(t, "gpt-4", copiedPrice.ModelID)
	require.Equal(t, price, copiedPrice.Price)
	require.NotEqual(t, sourcePrices[0].ReferenceID, copiedPrice.ReferenceID)
	require.Len(t, copiedPrice.ReferenceID, 8)

	copiedVersion, err := client.ChannelModelPriceVersion.Query().
		Where(channelmodelpriceversion.ChannelModelPriceID(copiedPrice.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, duplicated.ID, copiedVersion.ChannelID)
	require.Equal(t, "gpt-4", copiedVersion.ModelID)
	require.Equal(t, price, copiedVersion.Price)
	require.Equal(t, channelmodelpriceversion.StatusActive, copiedVersion.Status)
	require.Nil(t, copiedVersion.EffectiveEndAt)
	require.Equal(t, copiedPrice.ReferenceID, copiedVersion.ReferenceID)
}

func TestCalculatePriceChanges(t *testing.T) {
	price1 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeUsage},
		},
	}
	price2 := objects.ModelPrice{
		Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeCompletion},
		},
	}

	existingPrices := []*ent.ChannelModelPrice{
		{
			ID:      1,
			ModelID: "gpt-4",
			Price:   price1,
		},
	}

	tests := []struct {
		name           string
		existingPrices []*ent.ChannelModelPrice
		inputs         []SaveChannelModelPriceInput
		want           []PriceChangeAction
	}{
		{
			name:           "create and update",
			existingPrices: existingPrices,
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price1,
				},
				{
					ModelID: "gpt-3.5-turbo",
					Price:   price2,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeSkip,
					ModelID:       "gpt-4",
					Price:         price1,
					ExistingPrice: existingPrices[0],
				},
				{
					Type:          ActionTypeCreate,
					ModelID:       "gpt-3.5-turbo",
					Price:         price2,
					ExistingPrice: nil,
				},
			},
		},
		{
			name:           "all create",
			existingPrices: []*ent.ChannelModelPrice{},
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price1,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeCreate,
					ModelID:       "gpt-4",
					Price:         price1,
					ExistingPrice: nil,
				},
			},
		},
		{
			name:           "all update",
			existingPrices: existingPrices,
			inputs: []SaveChannelModelPriceInput{
				{
					ModelID: "gpt-4",
					Price:   price2,
				},
			},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeUpdate,
					ModelID:       "gpt-4",
					Price:         price2,
					ExistingPrice: existingPrices[0],
				},
			},
		},
		{
			name:           "delete missing",
			existingPrices: existingPrices,
			inputs:         []SaveChannelModelPriceInput{},
			want: []PriceChangeAction{
				{
					Type:          ActionTypeDelete,
					ModelID:       "gpt-4",
					ExistingPrice: existingPrices[0],
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculatePriceChanges(tt.existingPrices, tt.inputs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChannelService_PriceMultiplierHistory(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createAutoPriceTestChannel(t, ctx, client, "Multiplier history", []string{"priced"})
	var base objects.ModelPrice
	require.NoError(t, json.Unmarshal([]byte(`{"items":[{"itemCode":"prompt_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"2"}}]}`), &base))
	input := []SaveChannelModelPriceInput{{ModelID: "priced", Price: base}}
	initial, err := svc.SaveChannelModelPrices(ctx, ch.ID, input, nil)
	require.NoError(t, err)
	initialRef := initial[0].ReferenceID

	unchanged, err := svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("1"))
	require.NoError(t, err)
	require.Equal(t, initialRef, unchanged[0].ReferenceID)
	require.Equal(t, 1, countChannelModelPriceVersions(t, ctx, client, initial[0].ID))

	discounted, err := svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("0.15"))
	require.NoError(t, err)
	require.NotEqual(t, initialRef, discounted[0].ReferenceID)
	_, discountedCost := ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, discounted[0].Price, time.Time{})
	require.Equal(t, "0.3", discountedCost.String())
	oldVersion, err := client.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ReferenceID(initialRef)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, channelmodelpriceversion.StatusArchived, oldVersion.Status)
	require.Nil(t, oldVersion.Price.Multiplier)
	_, oldCost := ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, oldVersion.Price, time.Time{})
	require.Equal(t, "2", oldCost.String())

	unchanged, err = svc.SaveChannelModelPrices(ctx, ch.ID, input, nil)
	require.NoError(t, err)
	require.Equal(t, discounted[0].ReferenceID, unchanged[0].ReferenceID)
	free, err := svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("0"))
	require.NoError(t, err)
	require.NotEqual(t, discounted[0].ReferenceID, free[0].ReferenceID)
	items, cost := ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, free[0].Price, time.Time{})
	require.True(t, cost.IsZero())
	require.True(t, items[0].Subtotal.IsZero())

	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("-0.1"))
	require.Error(t, err)
	stored := queryChannelModelPrice(t, ctx, client, ch.ID, "priced")
	require.Equal(t, free[0].ReferenceID, stored.ReferenceID)
	require.Equal(t, 3, countChannelModelPriceVersions(t, ctx, client, stored.ID))
	storedChannel, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.True(t, storedChannel.Settings.ModelPriceMultiplier.IsZero())
}

func TestChannelService_PriceMultiplierEmptyChannelAndInheritance(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createAutoPriceTestChannel(t, ctx, client, "Multiplier inheritance", []string{"priced"})
	base := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")},
	}}}
	catalog := createModelLibraryEntry(t, ctx, client, "priced", model.StatusEnabled, &base)
	_, err := svc.SaveChannelModelPrices(ctx, ch.ID, nil, loToDecimalPtr("0.15"))
	require.NoError(t, err)
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{
		ExtraModelPrefix: "test", ModelPriceMultiplier: loToDecimalPtr("99"),
	}})
	require.NoError(t, err)
	storedChannel, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "0.15", storedChannel.Settings.ModelPriceMultiplier.String())
	require.Equal(t, "test", storedChannel.Settings.ExtraModelPrefix)

	_, err = svc.ensureChannelModelPrices(ctx, ch.ID, []string{"priced"})
	require.NoError(t, err)
	first := queryChannelModelPrice(t, ctx, client, ch.ID, "priced")
	_, cost := ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, first.Price, time.Time{})
	require.Equal(t, "0.3", cost.String())
	_, err = svc.ensureChannelModelPrices(ctx, ch.ID, []string{"priced"})
	require.NoError(t, err)
	require.Equal(t, first.ReferenceID, queryChannelModelPrice(t, ctx, client, ch.ID, "priced").ReferenceID)
	storedModel, err := client.Model.Get(ctx, catalog.ID)
	require.NoError(t, err)
	require.Nil(t, storedModel.ModelCard.Price.Multiplier)
	require.Equal(t, "2", storedModel.ModelCard.Price.Items[0].Pricing.UsagePerUnit.String())

	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, nil, loToDecimalPtr("0"))
	require.NoError(t, err)
	_, err = svc.ensureChannelModelPrices(ctx, ch.ID, []string{"priced"})
	require.NoError(t, err)
	free := queryChannelModelPrice(t, ctx, client, ch.ID, "priced")
	_, cost = ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, free.Price, time.Time{})
	require.True(t, cost.IsZero())
}

func TestChannelService_PriceMultiplierSaveRollback(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createAutoPriceTestChannel(t, ctx, client, "Multiplier rollback", []string{"priced"})
	base := objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeUsage,
		Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")},
	}}}
	input := []SaveChannelModelPriceInput{{ModelID: "priced", Price: base}}
	initial, err := svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("0.15"))
	require.NoError(t, err)
	failure := errors.New("version insert failed")
	client.ChannelModelPriceVersion.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ChannelModelPriceVersionFunc(func(ctx context.Context, mutation *ent.ChannelModelPriceVersionMutation) (ent.Value, error) {
			if mutation.Op() == ent.OpCreate {
				return nil, failure
			}
			return next.Mutate(ctx, mutation)
		})
	})
	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, input, loToDecimalPtr("0.5"))
	require.ErrorIs(t, err, failure)
	storedChannel, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "0.15", storedChannel.Settings.ModelPriceMultiplier.String())
	stored := queryChannelModelPrice(t, ctx, client, ch.ID, "priced")
	require.Equal(t, initial[0].ReferenceID, stored.ReferenceID)
	version, err := client.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ChannelID(ch.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, channelmodelpriceversion.StatusActive, version.Status)
	require.Nil(t, version.EffectiveEndAt)
	_, cost := ComputeUsageCost(&llm.Usage{PromptTokens: 1_000_000}, stored.Price, time.Time{})
	require.Equal(t, "0.3", cost.String())
}
