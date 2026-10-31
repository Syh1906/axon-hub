package biz

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelService_ManualPricingPreservesFullModelPrice(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	price := &objects.ModelPrice{
		Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("0")},
		}, {
			ItemCode: objects.PriceItemCodeWriteCachedTokens,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("3")},
			PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{{
				VariantCode: objects.PromptWriteCacheVariantCode1Hour,
				Pricing:     objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("5")},
			}},
		}},
		VolumeTiers: []objects.ModelPriceVolumeTier{{
			Above: 200000,
			Items: []objects.ModelPriceItem{{
				ItemCode: objects.PriceItemCodeUsage,
				Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("2")},
			}, {
				ItemCode: objects.PriceItemCodeWriteCachedTokens,
				Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("6")},
				PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{{
					VariantCode: objects.PromptWriteCacheVariantCode1Hour,
					Pricing:     objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("10")},
				}},
			}},
		}},
		Schedule: &objects.PriceSchedule{
			Timezone: "UTC",
			Overrides: []objects.PriceOverride{{
				Name: "night", Priority: 1,
				When: objects.OverrideWhen{DailyTime: &objects.DailyTimeRange{Start: "00:00", End: "06:00"}},
				Items: []objects.ModelPriceItem{{
					ItemCode: objects.PriceItemCodeUsage,
					Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("0.5")},
				}},
			}},
		},
	}
	catalog := createModelLibraryEntry(t, ctx, client, "full-price", model.StatusEnabled, price)
	ch := createAutoPriceTestChannel(t, ctx, client, "Full price", []string{"full-price"})
	_, err := svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "full-price", Price: *price}}, nil)
	require.NoError(t, err)
	current := queryChannelModelPrice(t, ctx, client, ch.ID, "full-price")
	require.True(t, price.Equals(current.Price))
	version, err := client.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ChannelID(ch.ID)).Only(ctx)
	require.NoError(t, err)
	require.True(t, price.Equals(version.Price))

	_, err = client.Model.UpdateOne(catalog).SetModelCard(&objects.ModelCard{Price: &objects.ModelPrice{
		Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{
			Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: loToDecimalPtr("99"),
		}}},
	}}).Save(ctx)
	require.NoError(t, err)
	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "full-price", Price: *price}}, nil)
	require.NoError(t, err)
	unchanged := queryChannelModelPrice(t, ctx, client, ch.ID, "full-price")
	require.True(t, price.Equals(unchanged.Price))
	require.Equal(t, current.ReferenceID, unchanged.ReferenceID)
	require.Equal(t, 1, countChannelModelPriceVersions(t, ctx, client, current.ID))
	version, err = client.ChannelModelPriceVersion.Get(ctx, version.ID)
	require.NoError(t, err)
	require.True(t, price.Equals(version.Price))

}

func TestChannelService_CreateChannel_InheritsPricesWithoutCopies(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "priced-model", model.StatusDisabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}}})
	createModelLibraryEntry(t, ctx, client, "zero-model", model.StatusEnabled, nil)

	ch, err := svc.CreateChannel(ctx, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		Name:             "Auto Price Create",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Credentials:      objects.ChannelCredentials{APIKey: "key"},
		SupportedModels:  []string{"priced-model", "zero-model", "missing-model"},
		DefaultTestModel: "priced-model",
	})
	require.NoError(t, err)

	require.Empty(t, queryChannelModelPrices(t, ctx, client, ch.ID))
	require.Zero(t, countAllChannelModelPriceVersions(t, ctx, client))
	returnedPrices, err := ch.QueryChannelModelPrices().All(ctx)
	require.NoError(t, err)
	require.Empty(t, returnedPrices)
}

func TestChannelService_CreateChannel_DefersReloadForCallerOwnedTransaction(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "outer-tx-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})

	notifier := &channelSyncNotifierSpy{}
	svc.channelNotifier = notifier
	previousAsyncReloadDisabled := asyncReloadDisabled
	asyncReloadDisabled = false
	t.Cleanup(func() {
		asyncReloadDisabled = previousAsyncReloadDisabled
	})

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := ent.NewTxContext(ctx, tx)
	txCtx = ent.NewContext(txCtx, tx.Client())

	ch, err := svc.CreateChannel(txCtx, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		Name:             "Outer transaction create",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Credentials:      objects.ChannelCredentials{APIKey: "key"},
		SupportedModels:  []string{"outer-tx-model"},
		DefaultTestModel: "outer-tx-model",
	})
	require.NoError(t, err)
	require.Zero(t, notifier.notifyCount)

	prices, err := ch.QueryChannelModelPrices().All(txCtx)
	require.NoError(t, err)
	require.Empty(t, prices)

	require.NoError(t, tx.Commit())
	ch.Unwrap()
	prices, err = ch.QueryChannelModelPrices().All(ctx)
	require.NoError(t, err)
	require.Empty(t, prices)
	require.Equal(t, 1, notifier.notifyCount)

	rollbackTx, err := client.Tx(ctx)
	require.NoError(t, err)
	rollbackCtx := ent.NewTxContext(ctx, rollbackTx)
	rollbackCtx = ent.NewContext(rollbackCtx, rollbackTx.Client())
	_, err = svc.CreateChannel(rollbackCtx, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		Name:             "Rolled-back outer transaction create",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Credentials:      objects.ChannelCredentials{APIKey: "key"},
		SupportedModels:  []string{"outer-tx-model"},
		DefaultTestModel: "outer-tx-model",
	})
	require.NoError(t, err)
	require.NoError(t, rollbackTx.Rollback())
	require.Equal(t, 1, notifier.notifyCount)
}

func TestChannelService_UpdateChannel_DoesNotFillPricesAndPreservesOverrides(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "existing-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})

	ch, err := svc.CreateChannel(ctx, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		Name:             "Auto Price Update",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Credentials:      objects.ChannelCredentials{APIKey: "key"},
		SupportedModels:  []string{"existing-model", "late-library-model"},
		DefaultTestModel: "existing-model",
	})
	require.NoError(t, err)

	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "existing-model", Price: objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 7)}}}}, nil)
	require.NoError(t, err)
	existingPrice := queryChannelModelPrice(t, ctx, client, ch.ID, "existing-model")
	existingVersionCount := countChannelModelPriceVersions(t, ctx, client, existingPrice.ID)

	createModelLibraryEntry(t, ctx, client, "late-library-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}}})
	createModelLibraryEntry(t, ctx, client, "added-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(3))}}}})

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Name: lo.ToPtr("Auto Price Update Renamed")})
	require.NoError(t, err)
	require.False(t, channelModelPriceExists(t, ctx, client, ch.ID, "late-library-model"))

	updated, err := svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		SupportedModels: []string{"existing-model", "late-library-model", "added-model"},
	})
	require.NoError(t, err)
	require.False(t, channelModelPriceExists(t, ctx, client, ch.ID, "added-model"))
	require.False(t, channelModelPriceExists(t, ctx, client, ch.ID, "late-library-model"))
	returnedPrices, err := updated.QueryChannelModelPrices().All(ctx)
	require.NoError(t, err)
	require.Len(t, returnedPrices, 1)

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		SupportedModels: []string{"late-library-model", "added-model"},
	})
	require.NoError(t, err)
	require.True(t, channelModelPriceExists(t, ctx, client, ch.ID, "existing-model"))

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		SupportedModels: []string{"existing-model", "late-library-model", "added-model"},
	})
	require.NoError(t, err)

	preserved := queryChannelModelPrice(t, ctx, client, ch.ID, "existing-model")
	require.Equal(t, existingPrice.ID, preserved.ID)
	require.Equal(t, existingPrice.ReferenceID, preserved.ReferenceID)
	require.Equal(t, existingPrice.Price, preserved.Price)
	require.Equal(t, existingVersionCount, countChannelModelPriceVersions(t, ctx, client, preserved.ID))
}

func TestChannelService_DuplicateChannel_CopiesOnlyExplicitSourcePrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "custom-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(9))}}}})
	createModelLibraryEntry(t, ctx, client, "gap-model", model.StatusDisabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(4))}}}})
	createModelLibraryEntry(t, ctx, client, "no-cost-model", model.StatusEnabled, nil)

	source := createAutoPriceTestChannel(t, ctx, client, "Duplicate Source", []string{"custom-model", "gap-model", "no-cost-model"})
	customPrice := objects.ModelPrice{Items: []objects.ModelPriceItem{
		{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing: objects.Pricing{
				Mode:         objects.PricingModeUsagePerUnit,
				UsagePerUnit: loToDecimalPtr("42"),
			},
		},
	}}
	sourcePrices, err := svc.SaveChannelModelPrices(ctx, source.ID, []SaveChannelModelPriceInput{
		{ModelID: "custom-model", Price: customPrice},
	}, nil)
	require.NoError(t, err)
	require.Len(t, sourcePrices, 1)

	duplicated, err := svc.DuplicateChannel(ctx, source.ID, ent.CreateChannelInput{
		Type:             channel.TypeOpenai,
		Name:             "Duplicate Target",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		Credentials:      objects.ChannelCredentials{APIKey: "target-key"},
		SupportedModels:  []string{"custom-model", "gap-model", "no-cost-model"},
		DefaultTestModel: "custom-model",
	})
	require.NoError(t, err)

	prices := queryChannelModelPrices(t, ctx, client, duplicated.ID)
	require.Len(t, prices, 1)

	copiedCustom := queryChannelModelPrice(t, ctx, client, duplicated.ID, "custom-model")
	require.Equal(t, customPrice, copiedCustom.Price)
	require.NotEqual(t, sourcePrices[0].ReferenceID, copiedCustom.ReferenceID)
	require.Equal(t, 1, countChannelModelPriceVersions(t, ctx, client, copiedCustom.ID))

	require.False(t, channelModelPriceExists(t, ctx, client, duplicated.ID, "gap-model"))
	require.False(t, channelModelPriceExists(t, ctx, client, duplicated.ID, "no-cost-model"))
	returnedPrices, err := duplicated.QueryChannelModelPrices().All(ctx)
	require.NoError(t, err)
	require.Len(t, returnedPrices, 1)
}

func TestChannelService_UpdateChannel_DoesNotDependOnPriceVersionWrites(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "rollback-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})
	ch := createAutoPriceTestChannel(t, ctx, client, "Update rollback", []string{"existing-model"})

	errInjected := errors.New("injected update price version failure")
	client.ChannelModelPriceVersion.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ChannelModelPriceVersionFunc(func(ctx context.Context, mutation *ent.ChannelModelPriceVersionMutation) (ent.Value, error) {
			modelID, _ := mutation.ModelID()
			if mutation.Op() == ent.OpCreate && modelID == "rollback-model" {
				return nil, errInjected
			}

			return next.Mutate(ctx, mutation)
		})
	})

	updated, err := svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		SupportedModels: []string{"existing-model", "rollback-model"},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	persisted, err := client.Channel.Get(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"existing-model", "rollback-model"}, persisted.SupportedModels)
	require.Equal(t, 0, countAllChannelModelPrices(t, ctx, client))
	require.Equal(t, 0, countAllChannelModelPriceVersions(t, ctx, client))
}

func TestChannelService_BulkCreateChannels_DoesNotCreatePriceVersions(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "bulk-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})

	errInjected := errors.New("injected channel model price version failure")
	var versionCreates atomic.Int32
	client.ChannelModelPriceVersion.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ChannelModelPriceVersionFunc(func(ctx context.Context, mutation *ent.ChannelModelPriceVersionMutation) (ent.Value, error) {
			if mutation.Op() == ent.OpCreate && versionCreates.Add(1) == 2 {
				return nil, errInjected
			}

			return next.Mutate(ctx, mutation)
		})
	})

	channels, err := svc.BulkCreateChannels(ctx, BulkCreateChannelsInput{
		Type:             channel.TypeOpenai,
		Name:             "Bulk Rollback",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		APIKeys:          []string{"key-1", "key-2", "key-3"},
		SupportedModels:  []string{"bulk-model"},
		DefaultTestModel: "bulk-model",
	})
	require.NoError(t, err)
	require.Len(t, channels, 3)
	require.Zero(t, versionCreates.Load())
	require.Equal(t, 3, countAllChannels(t, ctx, client))
	require.Equal(t, 0, countAllChannelModelPrices(t, ctx, client))
	require.Equal(t, 0, countAllChannelModelPriceVersions(t, ctx, client))
}

func TestChannelService_BulkCreateChannels_ReturnsQueryableEntities(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "bulk-query-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})

	channels, err := svc.BulkCreateChannels(ctx, BulkCreateChannelsInput{
		Type:             channel.TypeOpenai,
		Name:             "Bulk Queryable",
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		APIKeys:          []string{"key-1", "key-2"},
		SupportedModels:  []string{"bulk-query-model"},
		DefaultTestModel: "bulk-query-model",
	})
	require.NoError(t, err)
	require.Len(t, channels, 2)

	for _, ch := range channels {
		prices, err := ch.QueryChannelModelPrices().All(ctx)
		require.NoError(t, err)
		require.Empty(t, prices)
	}
}

func TestChannelService_BulkImportChannels_RollsBackOnlyFailingItem(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, ctx, client, "import-good-1", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})
	createModelLibraryEntry(t, ctx, client, "import-fail", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}}})
	createModelLibraryEntry(t, ctx, client, "import-good-2", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(3))}}}})

	errInjected := errors.New("injected channel import failure")
	client.Channel.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ChannelFunc(func(ctx context.Context, mutation *ent.ChannelMutation) (ent.Value, error) {
			name, _ := mutation.Name()
			if mutation.Op() == ent.OpCreate && name == "Import Failure" {
				return nil, errInjected
			}

			return next.Mutate(ctx, mutation)
		})
	})

	items := []*BulkImportChannelItem{
		newBulkImportAutoPriceTestItem("Import Good 1", "import-good-1"),
		newBulkImportAutoPriceTestItem("Import Failure", "import-fail"),
		newBulkImportAutoPriceTestItem("Import Good 2", "import-good-2"),
	}
	result, err := svc.BulkImportChannels(ctx, items)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Success)
	require.Equal(t, 2, result.Created)
	require.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	require.Contains(t, result.Errors[0], errInjected.Error())
	require.Len(t, result.Channels, 2)
	require.ElementsMatch(t, []string{"Import Good 1", "Import Good 2"}, lo.Map(result.Channels, func(ch *ent.Channel, _ int) string {
		return ch.Name
	}))

	require.Equal(t, 2, countAllChannels(t, ctx, client))
	require.Equal(t, 0, countAllChannelModelPrices(t, ctx, client))
	require.Equal(t, 0, countAllChannelModelPriceVersions(t, ctx, client))

	failureExists, err := client.Channel.Query().Where(channel.Name("Import Failure")).Exist(ctx)
	require.NoError(t, err)
	require.False(t, failureExists)

	for _, ch := range result.Channels {
		returnedPrices, err := ch.QueryChannelModelPrices().All(ctx)
		require.NoError(t, err)
		require.Empty(t, returnedPrices)
	}
}

func TestChannelService_PriceInheritanceSupportsWriteOnlyChannelMutations(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	setupCtx := channelAutoPriceTestContext(client)
	createModelLibraryEntry(t, setupCtx, client, "write-only-model", model.StatusEnabled, &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}})
	channelToUpdate := createAutoPriceTestChannel(t, setupCtx, client, "Write-only update", []string{"old-model"})

	writeCtx := authz.NewUserContext(ent.NewContext(context.Background(), client), 42)
	writeCtx = contexts.WithUser(writeCtx, &ent.User{
		ID:     42,
		Scopes: []string{"write_channels"},
	})

	updated, err := svc.UpdateChannel(writeCtx, channelToUpdate.ID, &ent.UpdateChannelInput{
		SupportedModels: []string{"old-model", "write-only-model"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"old-model", "write-only-model"}, updated.SupportedModels)
	require.False(t, channelModelPriceExists(t, setupCtx, client, channelToUpdate.ID, "write-only-model"))

	result, err := svc.BulkImportChannels(writeCtx, []*BulkImportChannelItem{
		newBulkImportAutoPriceTestItem("Write-only import", "write-only-model"),
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, result.Created)
	require.Len(t, result.Channels, 1)
	require.False(t, channelModelPriceExists(t, setupCtx, client, result.Channels[0].ID, "write-only-model"))
}

func channelAutoPriceTestContext(client *ent.Client) context.Context {
	ctx := ent.NewContext(context.Background(), client)

	return authz.WithTestBypass(ctx)
}

func createModelLibraryEntry(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	modelID string,
	status model.Status,
	price *objects.ModelPrice,
) *ent.Model {
	t.Helper()

	entity, err := client.Model.Create().
		SetDeveloper("test").
		SetModelID(modelID).
		SetName("Test " + modelID).
		SetIcon("test").
		SetGroup("test").
		SetModelCard(&objects.ModelCard{Price: price}).
		SetSettings(&objects.ModelSettings{}).
		SetStatus(status).
		Save(ctx)
	require.NoError(t, err)

	return entity
}

func createAutoPriceTestChannel(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	name string,
	supportedModels []string,
) *ent.Channel {
	t.Helper()

	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName(name).
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels(supportedModels).
		SetDefaultTestModel(supportedModels[0]).
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	return ch
}

func queryChannelModelPrices(t *testing.T, ctx context.Context, client *ent.Client, channelID int) []*ent.ChannelModelPrice {
	t.Helper()

	prices, err := client.ChannelModelPrice.Query().
		Where(channelmodelprice.ChannelID(channelID)).
		All(ctx)
	require.NoError(t, err)

	return prices
}

func queryChannelModelPrice(t *testing.T, ctx context.Context, client *ent.Client, channelID int, modelID string) *ent.ChannelModelPrice {
	t.Helper()

	price, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(channelID),
			channelmodelprice.ModelID(modelID),
		).
		Only(ctx)
	require.NoError(t, err)

	return price
}

func channelModelPriceExists(t *testing.T, ctx context.Context, client *ent.Client, channelID int, modelID string) bool {
	t.Helper()

	exists, err := client.ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(channelID),
			channelmodelprice.ModelID(modelID),
		).
		Exist(ctx)
	require.NoError(t, err)

	return exists
}

func countChannelModelPriceVersions(t *testing.T, ctx context.Context, client *ent.Client, priceID int) int {
	t.Helper()

	count, err := client.ChannelModelPriceVersion.Query().
		Where(channelmodelpriceversion.ChannelModelPriceID(priceID)).
		Count(ctx)
	require.NoError(t, err)

	return count
}

func countAllChannels(t *testing.T, ctx context.Context, client *ent.Client) int {
	t.Helper()

	count, err := client.Channel.Query().Count(ctx)
	require.NoError(t, err)

	return count
}

func countAllChannelModelPrices(t *testing.T, ctx context.Context, client *ent.Client) int {
	t.Helper()

	count, err := client.ChannelModelPrice.Query().Count(ctx)
	require.NoError(t, err)

	return count
}

func countAllChannelModelPriceVersions(t *testing.T, ctx context.Context, client *ent.Client) int {
	t.Helper()

	count, err := client.ChannelModelPriceVersion.Query().Count(ctx)
	require.NoError(t, err)

	return count
}

func newBulkImportAutoPriceTestItem(name, modelID string) *BulkImportChannelItem {
	return &BulkImportChannelItem{
		Type:             channel.TypeOpenai.String(),
		Name:             name,
		BaseURL:          lo.ToPtr("https://api.openai.com/v1"),
		APIKey:           lo.ToPtr(fmt.Sprintf("key-%s", modelID)),
		SupportedModels:  []string{modelID},
		DefaultTestModel: modelID,
	}
}
