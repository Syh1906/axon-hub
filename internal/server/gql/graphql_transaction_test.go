package gql

import (
	"context"
	"errors"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestBulkImportChannelsUsesPerRowTransactionsForAnyOperationName(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name: "custom operation name",
			query: `
				mutation ImportChannels($input: BulkImportChannelsInput!) {
					bulkImportChannels(input: $input) {
						success
						created
						failed
						errors
					}
				}
			`,
		},
		{
			name: "anonymous operation",
			query: `
				mutation ($input: BulkImportChannelsInput!) {
					bulkImportChannels(input: $input) {
						success
						created
						failed
						errors
					}
				}
			`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
			defer db.Close()

			ctx := authz.WithTestBypass(context.Background())
			for _, modelID := range []string{"import-good-1", "import-fail", "import-good-2"} {
				_, err := db.Model.Create().
					SetDeveloper("test").
					SetModelID(modelID).
					SetName(modelID).
					SetIcon("test").
					SetGroup("test").
					SetModelCard(&objects.ModelCard{Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}}}).
					SetSettings(&objects.ModelSettings{}).
					SetStatus(model.StatusEnabled).
					Save(ctx)
				require.NoError(t, err)
			}

			errInjected := errors.New("injected failure after channel insert")
			db.Channel.Use(func(next ent.Mutator) ent.Mutator {
				return hook.ChannelFunc(func(ctx context.Context, mutation *ent.ChannelMutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					if err != nil {
						return nil, err
					}
					// Import inherits model prices without copying price rows. Fail
					// after the insert so this still verifies a real row rollback.
					name, _ := mutation.Name()
					if mutation.Op() == ent.OpCreate && name == "Import Failure" {
						return nil, errInjected
					}

					return value, nil
				})
			})

			channelService := biz.NewChannelServiceForTest(db)
			defer channelService.Stop()

			handler := NewGraphqlHandlers(Dependencies{
				Ent:            db,
				ChannelService: channelService,
			})
			graphqlClient := client.New(handler.Graphql, func(request *client.Request) {
				request.HTTP = request.HTTP.WithContext(authz.WithTestBypass(request.HTTP.Context()))
			})

			input := map[string]any{
				"channels": []map[string]any{
					bulkImportChannelInput("Import Good 1", "import-good-1"),
					bulkImportChannelInput("Import Failure", "import-fail"),
					bulkImportChannelInput("Import Good 2", "import-good-2"),
				},
			}
			var response struct {
				BulkImportChannels struct {
					Success bool
					Created int
					Failed  int
					Errors  []string
				}
			}
			err := graphqlClient.Post(tt.query, &response, client.Var("input", input))
			require.NoError(t, err)
			require.False(t, response.BulkImportChannels.Success)
			require.Equal(t, 2, response.BulkImportChannels.Created)
			require.Equal(t, 1, response.BulkImportChannels.Failed)
			require.Len(t, response.BulkImportChannels.Errors, 1)
			require.Contains(t, response.BulkImportChannels.Errors[0], errInjected.Error())

			channelCount, err := db.Channel.Query().Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 2, channelCount)
			failureExists, err := db.Channel.Query().Where(channel.Name("Import Failure")).Exist(ctx)
			require.NoError(t, err)
			require.False(t, failureExists)

			priceCount, err := db.ChannelModelPrice.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, priceCount)
			versionCount, err := db.ChannelModelPriceVersion.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, versionCount)
		})
	}
}

func bulkImportChannelInput(name, modelID string) map[string]any {
	return map[string]any{
		"type":             "openai",
		"name":             name,
		"baseURL":          "https://api.openai.com/v1",
		"apiKey":           "test-key",
		"supportedModels":  []string{modelID},
		"defaultTestModel": modelID,
	}
}
