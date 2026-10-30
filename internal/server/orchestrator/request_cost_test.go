//nolint:exhaustruct_v5 // Test fixtures only populate fields under assertion.
package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestImageCostPersistsWithoutProviderUsage(t *testing.T) {
	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIImageGeneration), "image-proxy")
	state := middleware.inbound.state
	client := ent.FromContext(ctx)
	_, state.RequestService, _, _ = setupTestServices(t, client)
	state.RequestExec = client.RequestExecution.Create().SetRequestID(state.Request.ID).
		SetProjectID(state.Request.ProjectID).SetChannelID(state.RequestExec.ChannelID).
		SetModelID(state.RequestExec.ModelID).SetFormat(state.Request.Format).
		SetStatus(requestexecution.StatusProcessing).SetRequestBody([]byte(`{}`)).SaveX(ctx)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeImageFixed, Image: &objects.ImagePricing{Automatic: true, Rules: []objects.ImagePriceRule{{Name: "2K", Fee: decimal.RequireFromString("0.04"), Match: "all"}}}}}}}
	state.RequestExec.CostSnapshot = &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "image-ref", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	state.RawProviderRequest = &httpclient.Request{Body: []byte(`{"resolution":"2k"}`)}
	// Empty URL avoids any network call; metadata still proves two final outputs.
	raw := &httpclient.Response{StatusCode: 200, Body: []byte(`{"data":[{"b64_json":"invalid","size":"2048x2048"},{"b64_json":"invalid","size":"2048x2048"}]}`)}
	executionMiddleware := &persistRequestExecutionMiddleware{outbound: &PersistentOutboundTransformer{state: state}}
	_, err := executionMiddleware.OnOutboundRawResponse(ctx, raw)
	require.NoError(t, err)
	response := &llm.Response{ID: "images", RequestType: llm.RequestTypeImage}
	_, err = middleware.OnOutboundLlmResponse(ctx, response)
	require.NoError(t, err)
	require.Nil(t, response.Usage, "must not invent provider token usage")
	require.Equal(t, 200, *client.RequestExecution.GetX(ctx, state.RequestExec.ID).ResponseStatusCode)
	log, err := client.UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, log.TotalCost)
	require.InDelta(t, 0.08, *log.TotalCost, 1e-10)
	require.Len(t, log.CostItems, 2)
	require.Len(t, log.CostSnapshot.Images, 2)
	require.Zero(t, log.TotalTokens)
}

func TestUnknownImageIsNotRecordedAsFree(t *testing.T) {
	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIImageGeneration), "image-proxy")
	state := middleware.inbound.state
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeImageFixed, Image: &objects.ImagePricing{Automatic: true, Rules: []objects.ImagePriceRule{{Name: "1K", Fee: decimal.Zero, Match: "all"}}}}}}}
	state.RequestExec.CostSnapshot = &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "image-ref", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1)}
	_, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{ID: "unknown-image", RequestType: llm.RequestTypeImage})
	require.NoError(t, err)
	log, err := ent.FromContext(ctx).UsageLog.Query().Only(ctx)
	require.NoError(t, err)
	require.Nil(t, log.TotalCost)
	require.True(t, log.CostItems[0].Unpriced)
}

func setupUsageCostMiddleware(t *testing.T, format, modelID string) (*persistRequestMiddleware, context.Context, int) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	ctx = authz.WithTestBypass(ctx)
	ctx = ent.NewContext(ctx, client)

	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenaiFake).
		SetName("c1").
		SetSupportedModels([]string{modelID}).
		SetDefaultTestModel(modelID).
		SetStatus(channel.StatusEnabled).
		SetCredentials(objects.ChannelCredentials{}).
		Save(ctx)
	require.NoError(t, err)

	promptUnit := decimal.NewFromFloat(0.01)
	completionUnit := decimal.NewFromFloat(0.02)
	_, err = client.ChannelModelPrice.Create().
		SetChannelID(ch.ID).
		SetModelID(modelID).
		SetPrice(objects.ModelPrice{
			Items: []objects.ModelPriceItem{
				{
					ItemCode: objects.PriceItemCodeUsage,
					Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &promptUnit},
				},
				{
					ItemCode: objects.PriceItemCodeCompletion,
					Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &completionUnit},
				},
			},
		}).
		SetReferenceID("ref-1").
		Save(ctx)
	require.NoError(t, err)

	systemService := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	channelService := biz.NewChannelServiceForTest(client)
	built, err := channelService.GetChannel(ctx, ch.ID)
	require.NoError(t, err)
	channelService.PreloadModelPricesForTest(ctx, built)
	channelService.SetEnabledChannelsForTest([]*biz.Channel{built})
	require.NoError(t, systemService.SetInjectUsageCostEnabled(ctx, true))

	state := &PersistenceState{
		Request: &ent.Request{
			ID:        1,
			ProjectID: 1,
			APIKeyID:  1,
			Source:    "test",
			Format:    format,
			ModelID:   modelID,
		},
		RequestExec: &ent.RequestExecution{
			ID:        1,
			ChannelID: ch.ID,
			ModelID:   modelID,
		},
		UsageLogService: biz.NewUsageLogService(client, systemService, channelService),
		SystemService:   systemService,
	}

	return &persistRequestMiddleware{
		inbound: &PersistentInboundTransformer{state: state},
	}, ctx, ch.ID
}

func TestPersistRequestMiddleware_InjectsChatUsageCost(t *testing.T) {
	t.Parallel()

	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIChatCompletion), "m1")

	result, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{
		ID: "resp-1",
		Usage: &llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.NotNil(t, result.Usage.Cost)
	require.InDelta(t, 0.000005, *result.Usage.Cost, 1e-12)
}

func TestPersistRequestMiddleware_InjectsEmbeddingUsageCost(t *testing.T) {
	t.Parallel()

	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIEmbedding), "m1")

	result, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{
		ID: "resp-1",
		Usage: &llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.NotNil(t, result.Usage.Cost)
	require.InDelta(t, 0.000005, *result.Usage.Cost, 1e-12)
}

func TestPersistRequestMiddleware_SkipsInjectionWhenDisabled(t *testing.T) {
	t.Parallel()

	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIChatCompletion), "m1")
	require.NoError(t, middleware.inbound.state.SystemService.SetInjectUsageCostEnabled(ctx, false))

	result, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{
		ID: "resp-1",
		Usage: &llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
			Cost:             lo.ToPtr(9.99),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.NotNil(t, result.Usage.Cost)
	require.InDelta(t, 9.99, *result.Usage.Cost, 1e-12)
}

func TestPersistRequestMiddleware_InjectsStreamUsageCost(t *testing.T) {
	t.Parallel()

	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAICompletion), "m1")

	stream, err := middleware.OnOutboundLlmStream(ctx, streams.SliceStream([]*llm.Response{
		{
			ID: "cmpl-1",
			Completion: &llm.CompletionResponse{
				Choices: []llm.CompletionChoice{{Text: "Hello"}},
			},
		},
		{
			ID: "cmpl-1",
			Usage: &llm.Usage{
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
		},
	}))
	require.NoError(t, err)

	require.True(t, stream.Next())
	first := stream.Current()
	require.Nil(t, first.Usage)

	require.True(t, stream.Next())
	second := stream.Current()
	require.NotNil(t, second.Usage)
	require.NotNil(t, second.Usage.Cost)
	require.InDelta(t, 0.000005, *second.Usage.Cost, 1e-12)
	require.False(t, stream.Next())
}

func TestPersistRequestMiddleware_OmitsCostWithoutPrice(t *testing.T) {
	t.Parallel()

	middleware, ctx, _ := setupUsageCostMiddleware(t, string(llm.APIFormatOpenAIChatCompletion), "m1")
	middleware.inbound.state.RequestExec.ModelID = "unknown-model"

	result, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{
		ID: "resp-1",
		Usage: &llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
			Cost:             lo.ToPtr(9.99),
		},
	})
	require.NoError(t, err)
	require.Nil(t, result.Usage.Cost)
}
