package orchestrator

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// The failed Luna attempt and the successful Terra retry share one channel.
// Both its override and the model card exist in the override case.
func TestAssociationPricingOnSameChannelRetry(t *testing.T) {
	const luna, terra = "gpt-6-luna", "gpt-6.1-terra"
	for _, tc := range []struct {
		name     string
		override bool
		want     int64
	}{
		{"Terra model price", false, 2}, {"Terra channel override", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &titleTestExecutor{startErrors: map[string]error{luna: healthGateServerError()}}
			ctx, processor, db, candidate := responsesOriginGateway(t, executor)
			fee := func(n int64) *objects.ModelCard {
				value := decimal.NewFromInt(n)
				return &objects.ModelCard{Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{
					{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &value}},
				}}}
			}
			candidate.ModelCard = fee(1)
			candidate.Models = []biz.ChannelModelEntry{{RequestModel: luna, ActualModel: luna}, {RequestModel: terra, ActualModel: terra, UseActualModelPrice: true}}
			db.Model.Create().SetModelID(terra).SetName("Terra").SetDeveloper("test").SetIcon("test").SetGroup("test").
				SetStatus("enabled").SetSettings(&objects.ModelSettings{}).SetModelCard(fee(2)).SaveX(ctx)
			if tc.override {
				db.ChannelModelPrice.Create().SetChannelID(candidate.Channel.ID).SetModelID(terra).SetPrice(*fee(3).Price).SetReferenceID("terra-channel").SaveX(ctx)
				processor.ChannelService.PreloadModelPricesForTest(ctx, candidate.Channel)
			}
			projectID, _ := contexts.GetProjectID(ctx)
			key := db.APIKey.Create().SetProjectID(projectID).SetUserID(1).SetName("pricing").SetKey("fixture-pricing").SaveX(ctx)
			ctx = contexts.WithAPIKey(ctx, key)
			account := budget.NewState()
			account.Policy.StartedAt = time.Now().Add(-time.Second)
			account.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 1000000000}}
			db.BudgetAccount.Create().SetProjectID(projectID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(account).SaveX(ctx)
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.Enabled = true
			policy.MaxSingleChannelRetries, policy.MaxChannelRetries, policy.RetryDelayMs = 1, 0, 0
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"test"}`)})
			require.NoError(t, err)
			for result.ChatCompletionStream.Next() {
				result.ChatCompletionStream.Current()
			}
			require.NoError(t, result.ChatCompletionStream.Err())
			result.ChatCompletionStream.(streams.DeliveryAcknowledger).AcknowledgeDelivery()
			require.NoError(t, result.ChatCompletionStream.Close())
			require.Equal(t, []string{luna, terra}, executor.models)
			charge := db.BudgetCharge.Query().Where(budgetcharge.StatusEQ(budgetcharge.StatusSettled)).OnlyX(ctx)
			require.EqualValues(t, tc.want*100000000, charge.Estimate)
			require.EqualValues(t, charge.Estimate, *charge.Actual)
			require.EqualValues(t, charge.Estimate, db.BudgetPosting.Query().OnlyX(ctx).Amount)
			row := db.UsageLog.Query().OnlyX(ctx)
			require.Equal(t, terra, row.ModelID)
			require.EqualValues(t, tc.want, *row.TotalCost)
		})
	}
}
