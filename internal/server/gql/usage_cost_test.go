package gql

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/intercept"
	"github.com/looplj/axonhub/internal/objects"
)

func TestManagedGroupUsageRequestsSupportsPricingEvidence(t *testing.T) {
	// The managed group page shares the self-service request list. Its nested
	// cost evidence must also be accepted by the main GraphQL schema.
	_, err := gqlparser.LoadQuery(NewExecutableSchema(Config{}).Schema(), `
		query {
			managedGroupUsageRequests(groupRef: 1, start: "2026-10-31", end: "2026-11-01") {
				items {
					inputTokens outputTokens cacheReadTokens cacheWriteTokens reasoningTokens
					costItems {
						pricingEvidence {
							serviceTier { mode basis client outbound upstream confirmed reason source referenceModel version }
							context { kind inputTokens threshold longContext name }
							baseSubtotal baseTierSubtotals modeMultiplier costMultiplier
							itemMethod ruleSource baseUnitPrice finalUnitPrice modeSubtotal modeTierSubtotals
							reference { source referenceModel version sourceURL verifiedAt application currency unit }
						}
					}
				}
			}
		}`)
	require.Empty(t, err)
}

func TestUsageLogCostPriceMultiplierBatchesVisibleRows(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:usage-multiplier-batch?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	setupCtx := authz.WithTestBypass(context.Background())
	ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName("price-version-test").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"model"}).
		SetDefaultTestModel("model").SaveX(setupCtx)
	price := client.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID("model").
		SetReferenceID("current").SetPrice(objects.ModelPrice{}).SaveX(setupCtx)
	for _, tc := range []struct {
		ref   string
		value *decimal.Decimal
	}{
		{ref: "legacy", value: nil},
		{ref: "discount", value: lo.ToPtr(decimal.RequireFromString("0.5"))},
		{ref: "free", value: lo.ToPtr(decimal.Zero)},
	} {
		client.ChannelModelPriceVersion.Create().SetChannelID(ch.ID).SetChannelModelPriceID(price.ID).
			SetModelID("model").SetReferenceID(tc.ref).SetEffectiveStartAt(time.Now()).
			SetStatus(channelmodelpriceversion.StatusArchived).
			SetPrice(objects.ModelPrice{Multiplier: tc.value}).SaveX(setupCtx)
	}
	var queries atomic.Int64
	client.Intercept(intercept.Func(func(_ context.Context, query intercept.Query) error {
		if query.Type() == ent.TypeChannelModelPriceVersion {
			queries.Add(1)
		}
		return nil
	}))
	loader, fire := newManualUsagePriceLoader(client)
	ctx := context.WithValue(context.Background(), usagePriceLoaderKey{}, loader)
	resolver := &usageLogResolver{Resolver: &Resolver{client: client}}
	refs := []string{"legacy", "discount", "free", "missing-version", ""}
	cost := 1.0
	const rowCount = 30
	results := make([]*decimal.Decimal, rowCount)
	var group errgroup.Group
	for i := range results {
		i := i
		group.Go(func() error {
			value, err := resolver.CostPriceMultiplier(ctx, &ent.UsageLog{CostPriceReferenceID: refs[i%len(refs)], TotalCost: &cost})
			results[i] = value
			return err
		})
	}
	// Every row with a reference must be waiting on the same batch before it is released.
	waitForPendingUsagePrices(t, loader, rowCount-rowCount/len(refs))
	(<-fire)()
	require.NoError(t, group.Wait())
	for i, value := range results {
		switch refs[i%len(refs)] {
		case "discount":
			require.Equal(t, "0.5", value.String())
		case "free":
			require.Equal(t, "0", value.String())
		default:
			// Legacy snapshots, missing versions and unreferenced records were billed without a multiplier.
			require.Equal(t, "1", value.String(), fmt.Sprintf("reference %q", refs[i%len(refs)]))
		}
	}
	require.Equal(t, int64(1), queries.Load(), "all rows in one batch must share one version query")

	again, err := resolver.CostPriceMultiplier(ctx, &ent.UsageLog{CostPriceReferenceID: "discount", TotalCost: &cost})
	require.NoError(t, err)
	require.Equal(t, "0.5", again.String())
	require.Equal(t, int64(1), queries.Load(), "a resolved reference must not be queried again in the same operation")

	unpriced, err := resolver.CostPriceMultiplier(ctx, &ent.UsageLog{CostPriceReferenceID: "discount"})
	require.NoError(t, err)
	require.Nil(t, unpriced, "an unpriced record has no multiplier")
}

func TestUsagePriceLoaderCallerCancellationDoesNotAffectSiblings(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:usage-multiplier-cancel?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	setupCtx := authz.WithTestBypass(context.Background())
	ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName("price-version-cancel").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"model"}).
		SetDefaultTestModel("model").SaveX(setupCtx)
	price := client.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID("model").
		SetReferenceID("current").SetPrice(objects.ModelPrice{}).SaveX(setupCtx)
	client.ChannelModelPriceVersion.Create().SetChannelID(ch.ID).SetChannelModelPriceID(price.ID).
		SetModelID("model").SetReferenceID("discount").SetEffectiveStartAt(time.Now()).
		SetStatus(channelmodelpriceversion.StatusArchived).
		SetPrice(objects.ModelPrice{Multiplier: lo.ToPtr(decimal.RequireFromString("0.5"))}).SaveX(setupCtx)

	loader, fire := newManualUsagePriceLoader(client)
	canceledCtx, cancel := context.WithCancel(context.Background())
	canceledErr := make(chan error, 1)
	go func() {
		_, err := loader.load(canceledCtx, "discount")
		canceledErr <- err
	}()
	sibling := make(chan *decimal.Decimal, 1)
	go func() {
		value, _ := loader.load(context.Background(), "discount")
		sibling <- value
	}()
	waitForPendingUsagePrices(t, loader, 2)

	cancel()
	require.ErrorIs(t, <-canceledErr, context.Canceled)
	(<-fire)()
	require.Equal(t, "0.5", (<-sibling).String())
}

// newManualUsagePriceLoader returns a loader whose batch window closes only when the test calls the delivered fire func.
func newManualUsagePriceLoader(client *ent.Client) (*usagePriceLoader, <-chan func()) {
	fire := make(chan func(), 1)
	loader := newUsagePriceLoader(context.Background(), client)
	loader.schedule = func(f func()) { fire <- f }

	return loader, fire
}

func waitForPendingUsagePrices(t *testing.T, loader *usagePriceLoader, waiters int) {
	t.Helper()
	require.Eventually(t, func() bool {
		loader.mu.Lock()
		defer loader.mu.Unlock()
		count := 0
		for _, pending := range loader.pending {
			count += len(pending)
		}
		return count == waiters
	}, 5*time.Second, time.Millisecond)
}

func TestCostItemVariantCodeReturnsNullWhenUnset(t *testing.T) {
	resolver := &costItemResolver{}
	empty, err := resolver.PromptWriteCacheVariantCode(context.Background(), &objects.CostItem{})
	require.NoError(t, err)
	require.Nil(t, empty)
	set, err := resolver.PromptWriteCacheVariantCode(context.Background(), &objects.CostItem{
		PromptWriteCacheVariantCode: objects.PromptWriteCacheVariantCode5Min,
	})
	require.NoError(t, err)
	require.Equal(t, objects.PromptWriteCacheVariantCode5Min, *set)
}
