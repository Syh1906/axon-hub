package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	openaitypes "github.com/looplj/axonhub/llm/transformer/openai"
)

func setupOpenAIRetrieveTest(t *testing.T) (*ent.Client, *biz.ChannelService, *biz.SystemService, *gin.Engine, context.Context) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	channelSvc := biz.NewChannelServiceForTest(client)
	systemSvc := biz.NewSystemService(biz.SystemServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	modelSvc := biz.NewModelService(biz.ModelServiceParams{
		ChannelService: channelSvc,
		SystemService:  systemSvc,
		Ent:            client,
	})

	handlers := &OpenAIHandlers{
		ModelService:  modelSvc,
		SystemService: systemSvc,
		EntClient:     client,
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := ent.NewContext(c.Request.Context(), client)
		ctx = authz.WithTestBypass(ctx)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/v1/models", handlers.ListModels)
	router.GET("/v1/models/*model", handlers.RetrieveModel)

	ctx := ent.NewContext(context.Background(), client)
	ctx = authz.WithTestBypass(ctx)

	return client, channelSvc, systemSvc, router, ctx
}

func TestOpenAIHandlers_RetrieveModel_SupportsSlashModelIDs(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)

	createdAt := time.Unix(1712345678, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("DeepSeek Channel").
		SetBaseURL("https://api.deepseek.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"deepseek-chat"}).
		SetDefaultTestModel("deepseek-chat").
		SetSettings(&objects.ChannelSettings{ExtraModelPrefix: "deepseek"}).
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})
	_, err = client.Model.Create().
		SetDeveloper("deepseek").
		SetModelID("deepseek/deepseek-chat").
		SetName("DeepSeek Chat").
		SetType(model.TypeChat).
		SetGroup("deepseek").
		SetIcon("deepseek").
		SetModelCard(&objects.ModelCard{}).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{
			Type:         "channel_model",
			ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "deepseek-chat"},
		}}}).
		SetStatus(model.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/deepseek/deepseek-chat", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got OpenAIModel
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "deepseek/deepseek-chat", got.ID)
	require.Equal(t, "model", got.Object)
	require.Equal(t, createdAt.Unix(), got.Created)
	require.Equal(t, "configured", got.OwnedBy)
}

func TestOpenAIHandlers_RetrieveModel_RejectsChannelOnlyModel(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)

	createdAt := time.Unix(1712345688, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4o-mini"}).
		SetDefaultTestModel("gpt-4o-mini").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})

	for _, query := range []string{"", "?include=all"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o-mini"+query, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Code)
		var got openaitypes.OpenAIError
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		require.Equal(t, "model_not_found", got.Detail.Code)
	}
}

func TestOpenAIHandlers_RetrieveModel_ReturnsExtendedConfiguredModel(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)

	channelCreatedAt := time.Unix(1712345698, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(channelCreatedAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})

	remark := "GPT-4.1 reasoning model"
	modelCreatedAt := time.Unix(1712345708, 0)
	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetRemark(remark).
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall:  true,
			Reasoning: objects.ModelCardReasoning{Supported: true},
			Limit:     objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}, {ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.5))}}, {ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}}, Modalities: objects.ModelCardModalities{Input: []string{"text", "image"}, Output: []string{"text"}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{
				{
					Type: "channel_model",
					ChannelModel: &objects.ChannelModelAssociation{
						ChannelID: ch.ID,
						ModelID:   "gpt-4.1",
					},
				},
			},
		}).
		SetStatus(model.StatusEnabled).
		SetCreatedAt(modelCreatedAt).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4.1?include=all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got OpenAIModel
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "gpt-4.1", got.ID)
	require.Equal(t, "model", got.Object)
	require.Equal(t, modelCreatedAt.Unix(), got.Created)
	require.Equal(t, "openai", got.OwnedBy)
	require.Equal(t, "GPT-4.1", got.Name)
	require.Equal(t, remark, got.Description)
	require.Equal(t, "chat", got.Type)
	require.NotNil(t, got.Capabilities)
	require.True(t, got.Capabilities.Vision)
	require.True(t, got.Capabilities.ToolCall)
	require.True(t, got.Capabilities.Reasoning)
	require.Equal(t, 200000, got.ContextLength)
	require.Equal(t, 8192, got.MaxOutputTokens)
	require.NotNil(t, got.Pricing)
	require.Equal(t, 2.0, *got.Pricing.Input)
	require.Equal(t, 8.0, got.Pricing.Output)
	require.Equal(t, 0.5, *got.Pricing.CacheRead)
	require.Equal(t, 1.0, got.Pricing.CacheWrite)
	require.NotNil(t, got.Modalities)
	require.Equal(t, []string{"text", "image"}, got.Modalities.Input)
	require.Equal(t, []string{"text"}, got.Modalities.Output)
}

func TestOpenAIHandlers_RetrieveModel_ReturnsEmptyModalitiesWhenZeroValue(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)

	channelCreatedAt := time.Unix(1712345698, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(channelCreatedAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})

	modelCreatedAt := time.Unix(1712345708, 0)
	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall: true,
			Limit:    objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{
				{
					Type: "channel_model",
					ChannelModel: &objects.ChannelModelAssociation{
						ChannelID: ch.ID,
						ModelID:   "gpt-4.1",
					},
				},
			},
		}).
		SetStatus(model.StatusEnabled).
		SetCreatedAt(modelCreatedAt).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4.1?include=all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got OpenAIModel
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "gpt-4.1", got.ID)
	require.NotNil(t, got.Modalities, "modalities should be non-nil even when ModelCard.Modalities is zero value")
	require.NotNil(t, got.Modalities.Input, "modalities.input should be [] not null")
	require.NotNil(t, got.Modalities.Output, "modalities.output should be [] not null")
	require.Empty(t, got.Modalities.Input)
	require.Empty(t, got.Modalities.Output)
}

func TestOpenAIHandlers_RetrieveModel_ReturnsNotFound(t *testing.T) {
	_, _, _, router, _ := setupOpenAIRetrieveTest(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/missing-model", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)

	var got openaitypes.OpenAIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, "model_not_found", got.Detail.Code)
	require.Equal(t, "invalid_request_error", got.Detail.Type)
	require.Equal(t, "model", got.Detail.Param)
	require.Contains(t, got.Detail.Message, "missing-model")
}

func TestOpenAIHandlers_ListModels_UsesBasicFieldsByDefault(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)

	createdAt := time.Unix(1712345698, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})

	remark := "GPT-4.1 reasoning model"
	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetRemark(remark).
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall:  true,
			Reasoning: objects.ModelCardReasoning{Supported: true},
			Limit:     objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}, {ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.5))}}, {ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}}, Modalities: objects.ModelCardModalities{Input: []string{"text", "image"}, Output: []string{"text"}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{
				{
					Type: "channel_model",
					ChannelModel: &objects.ChannelModelAssociation{
						ChannelID: ch.ID,
						ModelID:   "gpt-4.1",
					},
				},
			},
		}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Len(t, got.Data, 1)
	require.Equal(t, "gpt-4.1", got.Data[0].ID)
	require.Empty(t, got.Data[0].Name)
	require.Nil(t, got.Data[0].Capabilities)
	require.Nil(t, got.Data[0].Pricing)
	require.Nil(t, got.Data[0].Modalities)
}

func TestOpenAIHandlers_ListModels_UsesExtendedFieldsWhenConfiguredAsDefault(t *testing.T) {
	client, channelSvc, systemSvc, router, ctx := setupOpenAIRetrieveTest(t)

	err := systemSvc.SetModelSettings(ctx, biz.SystemModelSettings{
		DefaultModelAPIIncludeAll: true,
	})
	require.NoError(t, err)

	createdAt := time.Unix(1712345698, 0)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})

	remark := "GPT-4.1 reasoning model"
	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetRemark(remark).
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall:  true,
			Reasoning: objects.ModelCardReasoning{Supported: true},
			Limit:     objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}, {ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.5))}}, {ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(1))}}}}, Modalities: objects.ModelCardModalities{Input: []string{"text", "image"}, Output: []string{"text"}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{
				{
					Type: "channel_model",
					ChannelModel: &objects.ChannelModelAssociation{
						ChannelID: ch.ID,
						ModelID:   "gpt-4.1",
					},
				},
			},
		}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Len(t, got.Data, 1)
	require.Equal(t, "gpt-4.1", got.Data[0].ID)
	require.Equal(t, "GPT-4.1", got.Data[0].Name)
	require.Equal(t, remark, got.Data[0].Description)
	require.NotNil(t, got.Data[0].Capabilities)
	require.NotNil(t, got.Data[0].Pricing)
	require.NotNil(t, got.Data[0].Modalities)
	require.Equal(t, []string{"text", "image"}, got.Data[0].Modalities.Input)
	require.Equal(t, []string{"text"}, got.Data[0].Modalities.Output)
}

func TestOpenAIHandlers_ListModels_ExtendedModeRespectsAPIKeyProfile(t *testing.T) {
	client, channelSvc, systemSvc, _, ctx := setupOpenAIRetrieveTest(t)

	err := systemSvc.SetModelSettings(ctx, biz.SystemModelSettings{
		DefaultModelAPIIncludeAll: true,
	})
	require.NoError(t, err)

	createdAt := time.Unix(1712345698, 0)

	openaiCh, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	anthropicCh, err := client.Channel.Create().
		SetType(channel.TypeAnthropic).
		SetName("Anthropic Channel").
		SetBaseURL("https://api.anthropic.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"claude-3-opus-20240229"}).
		SetDefaultTestModel("claude-3-opus-20240229").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: openaiCh}, {Channel: anthropicCh}})

	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall: true,
			Limit:    objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{{
				Type: "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{
					ChannelID: openaiCh.ID,
					ModelID:   "gpt-4.1",
				},
			}},
		}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Model.Create().
		SetDeveloper("anthropic").
		SetModelID("claude-3-opus-20240229").
		SetName("Claude 3 Opus").
		SetType(model.TypeChat).
		SetGroup("claude").
		SetIcon("anthropic").
		SetModelCard(&objects.ModelCard{Vision: true,
			ToolCall: true,
			Limit:    objects.ModelCardLimit{Context: 200000, Output: 4096}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(15))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(75))}}}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{{
				Type: "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{
					ChannelID: anthropicCh.ID,
					ModelID:   "claude-3-opus-20240229",
				},
			}},
		}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	apiKey := &ent.APIKey{
		ID:   99,
		Name: "restricted-key",
		Profiles: &objects.APIKeyProfiles{
			ActiveProfile: "limited",
			Profiles: []objects.APIKeyProfile{{
				Name:     "limited",
				ModelIDs: []string{"gpt-4.1"},
			}},
		},
	}

	restrictedRouter := gin.New()
	restrictedRouter.Use(func(c *gin.Context) {
		reqCtx := ent.NewContext(c.Request.Context(), client)
		reqCtx = authz.WithTestBypass(reqCtx)
		reqCtx = contexts.WithAPIKey(reqCtx, apiKey)
		c.Request = c.Request.WithContext(reqCtx)
		c.Next()
	})

	handlers := &OpenAIHandlers{
		ModelService: biz.NewModelService(biz.ModelServiceParams{
			ChannelService: channelSvc,
			SystemService:  systemSvc,
			Ent:            client,
		}),
		SystemService: systemSvc,
		EntClient:     client,
	}
	restrictedRouter.GET("/v1/models", handlers.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	restrictedRouter.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))

	require.Len(t, got.Data, 1, "extended mode should only return models the API key has access to")
	require.Equal(t, "gpt-4.1", got.Data[0].ID)
	require.Equal(t, "GPT-4.1", got.Data[0].Name)
	require.NotNil(t, got.Data[0].Capabilities)
	require.NotNil(t, got.Data[0].Pricing)
}

func TestOpenAIHandlers_ListModels_ExtendedModeExcludesChannelOnlyModels(t *testing.T) {
	client, channelSvc, systemSvc, _, ctx := setupOpenAIRetrieveTest(t)

	err := systemSvc.SetModelSettings(ctx, biz.SystemModelSettings{
		DefaultModelAPIIncludeAll: true,
	})
	require.NoError(t, err)

	createdAt := time.Unix(1712345698, 0)

	openaiCh, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1", "gpt-4.1-mini"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: openaiCh}})

	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetModelCard(&objects.ModelCard{Vision: true, ToolCall: true,
			Limit: objects.ModelCardLimit{Context: 200000, Output: 8192}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(2))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(8))}}}}}).
		SetSettings(&objects.ModelSettings{
			Associations: []*objects.ModelAssociation{{
				Type:         "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{ChannelID: openaiCh.ID, ModelID: "gpt-4.1"},
			}},
		}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	apiKey := &ent.APIKey{
		ID:   100,
		Name: "registered-only-key",
		Profiles: &objects.APIKeyProfiles{
			ActiveProfile: "limited",
			Profiles: []objects.APIKeyProfile{{
				Name:     "limited",
				ModelIDs: []string{"gpt-4.1", "gpt-4.1-mini"},
			}},
		},
	}

	restrictedRouter := gin.New()
	restrictedRouter.Use(func(c *gin.Context) {
		reqCtx := ent.NewContext(c.Request.Context(), client)
		reqCtx = authz.WithTestBypass(reqCtx)
		reqCtx = contexts.WithAPIKey(reqCtx, apiKey)
		c.Request = c.Request.WithContext(reqCtx)
		c.Next()
	})

	handlers := &OpenAIHandlers{
		ModelService:  biz.NewModelService(biz.ModelServiceParams{ChannelService: channelSvc, SystemService: systemSvc, Ent: client}),
		SystemService: systemSvc,
		EntClient:     client,
	}
	restrictedRouter.GET("/v1/models", handlers.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	restrictedRouter.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))

	require.Len(t, got.Data, 1)

	resultMap := make(map[string]OpenAIModel)
	for _, m := range got.Data {
		resultMap[m.ID] = m
	}

	gpt41, ok := resultMap["gpt-4.1"]
	require.True(t, ok, "gpt-4.1 should be present")
	require.NotNil(t, gpt41.Capabilities, "gpt-4.1 has a DB entry so should have extended fields")

	require.NotContains(t, resultMap, "gpt-4.1-mini")
}

func TestOpenAIHandlers_ListModels_ExtendedModeWithZeroAllowedModelsReturnsEmpty(t *testing.T) {
	client, channelSvc, systemSvc, _, ctx := setupOpenAIRetrieveTest(t)

	err := systemSvc.SetModelSettings(ctx, biz.SystemModelSettings{
		DefaultModelAPIIncludeAll: true,
	})
	require.NoError(t, err)

	createdAt := time.Unix(1712345698, 0)

	openaiCh, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("OpenAI Channel").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"gpt-4.1"}).
		SetDefaultTestModel("gpt-4.1").
		SetStatus(channel.StatusEnabled).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)

	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: openaiCh}})
	_, err = client.Model.Create().
		SetDeveloper("openai").
		SetModelID("gpt-4.1").
		SetName("GPT-4.1").
		SetType(model.TypeChat).
		SetGroup("gpt").
		SetIcon("openai").
		SetModelCard(&objects.ModelCard{}).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{
			Type:         "channel_model",
			ChannelModel: &objects.ChannelModelAssociation{ChannelID: openaiCh.ID, ModelID: "gpt-4.1"},
		}}}).
		SetStatus(model.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	apiKey := &ent.APIKey{
		ID:   101,
		Name: "zero-models-key",
		Profiles: &objects.APIKeyProfiles{
			ActiveProfile: "none",
			Profiles: []objects.APIKeyProfile{{
				Name:     "none",
				ModelIDs: []string{"nonexistent-model-xyz"},
			}},
		},
	}

	restrictedRouter := gin.New()
	restrictedRouter.Use(func(c *gin.Context) {
		reqCtx := ent.NewContext(c.Request.Context(), client)
		reqCtx = authz.WithTestBypass(reqCtx)
		reqCtx = contexts.WithAPIKey(reqCtx, apiKey)
		c.Request = c.Request.WithContext(reqCtx)
		c.Next()
	})

	handlers := &OpenAIHandlers{
		ModelService:  biz.NewModelService(biz.ModelServiceParams{ChannelService: channelSvc, SystemService: systemSvc, Ent: client}),
		SystemService: systemSvc,
		EntClient:     client,
	}
	restrictedRouter.GET("/v1/models", handlers.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	restrictedRouter.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Empty(t, got.Data, "API key with no matching models should return empty list")
}

func TestPublicModelAPIs_MappedTargetsRequireEnabledRegistration(t *testing.T) {
	client, channelSvc, systemSvc, _, ctx := setupOpenAIRetrieveTest(t)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Model API Channel").
		SetBaseURL("https://api.example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "key"}).
		SetSupportedModels([]string{"target", "shadow", "disabled", "archived", "channel-only"}).
		SetDefaultTestModel("target").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)
	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})
	createdAt := time.Unix(1712345708, 0)
	for _, fixture := range []struct {
		id     string
		status model.Status
	}{
		{id: "target", status: model.StatusEnabled},
		{id: "shadow", status: model.StatusEnabled},
		{id: "disabled", status: model.StatusDisabled},
		{id: "archived", status: model.StatusArchived},
	} {
		_, err = client.Model.Create().
			SetDeveloper("openai").
			SetModelID(fixture.id).
			SetName("Metadata for " + fixture.id).
			SetType(model.TypeChat).
			SetGroup("gpt").
			SetIcon("openai").
			SetCreatedAt(createdAt).
			SetModelCard(&objects.ModelCard{Limit: objects.ModelCardLimit{Context: 12345}}).
			SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{
				Type:         "channel_model",
				ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: fixture.id},
			}}}).
			SetStatus(fixture.status).
			Save(ctx)
		require.NoError(t, err)
	}

	apiKey := &ent.APIKey{Profiles: &objects.APIKeyProfiles{
		ActiveProfile: "mapped",
		Profiles: []objects.APIKeyProfile{{
			Name:     "mapped",
			ModelIDs: []string{"public-alias", "shadow", "disabled-alias", "archived-alias", "channel-alias", "missing-alias"},
			ModelMappings: []objects.ModelMapping{
				{From: "public-alias", To: "target"},
				{From: "shadow", To: "target"},
				{From: "disabled-alias", To: "disabled"},
				{From: "archived-alias", To: "archived"},
				{From: "channel-alias", To: "channel-only"},
				{From: "missing-alias", To: "missing"},
			},
		}},
	}}
	modelSvc := biz.NewModelService(biz.ModelServiceParams{ChannelService: channelSvc, SystemService: systemSvc, Ent: client})
	openaiHandlers := &OpenAIHandlers{ModelService: modelSvc, SystemService: systemSvc, EntClient: client}
	anthropicHandlers := &AnthropicHandlers{ModelService: modelSvc}
	geminiHandlers := &GeminiHandlers{ModelService: modelSvc}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		requestCtx := authz.WithTestBypass(ent.NewContext(c.Request.Context(), client))
		c.Request = c.Request.WithContext(contexts.WithAPIKey(requestCtx, apiKey))
		c.Next()
	})
	router.GET("/openai/models", openaiHandlers.ListModels)
	router.GET("/openai/models/*model", openaiHandlers.RetrieveModel)
	router.GET("/anthropic/models", anthropicHandlers.ListModels)
	router.GET("/gemini/models", geminiHandlers.ListModels)

	for _, tc := range []struct {
		path      string
		listField string
		idField   string
		want      []string
	}{
		{path: "/openai/models", listField: "data", idField: "id", want: []string{"public-alias", "shadow"}},
		{path: "/openai/models?include=all", listField: "data", idField: "id", want: []string{"public-alias", "shadow"}},
		{path: "/anthropic/models", listField: "data", idField: "id", want: []string{"public-alias", "shadow"}},
		{path: "/gemini/models", listField: "models", idField: "name", want: []string{"models/public-alias", "models/shadow"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, w.Code)
			var body map[string]json.RawMessage
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
			var models []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body[tc.listField], &models))
			ids := make([]string, 0, len(models))
			for _, m := range models {
				var id string
				require.NoError(t, json.Unmarshal(m[tc.idField], &id))
				ids = append(ids, id)
				if tc.path == "/openai/models?include=all" {
					var name string
					require.NoError(t, json.Unmarshal(m["name"], &name))
					require.Equal(t, "Metadata for target", name)
				}
			}
			require.ElementsMatch(t, tc.want, ids)
		})
	}

	for _, id := range []string{"public-alias", "shadow"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/models/"+id+"?include=all", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var got OpenAIModel
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		require.Equal(t, id, got.ID)
		require.Equal(t, "Metadata for target", got.Name)
		require.Equal(t, 12345, got.ContextLength)
		require.Equal(t, createdAt.Unix(), got.Created)
	}
	for _, id := range []string{"target", "disabled-alias", "archived-alias", "channel-alias", "missing-alias"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/models/"+id+"?include=all", nil))
		require.Equal(t, http.StatusNotFound, w.Code)
	}

	// A previously valid alias disappears as soon as its registered target is disabled.
	_, err = client.Model.Update().Where(model.ModelID("target")).SetStatus(model.StatusDisabled).Save(ctx)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/models/public-alias?include=all", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/models?include=all", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Data []OpenAIModel `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Empty(t, got.Data)
}
