package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/assert"
)

func TestConvertModelToOpenAIExtended_ModalityPrices(t *testing.T) {
	price := &objects.ModelPrice{}
	for _, entry := range []struct {
		code objects.PriceItemCode
		rate int64
	}{
		{objects.PriceItemCodePromptText, 5}, {objects.PriceItemCodePromptImage, 8}, {objects.PriceItemCodeCompletion, 30},
	} {
		price.Items = append(price.Items, objects.ModelPriceItem{ItemCode: entry.code, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(entry.rate))}})
	}
	result := convertModelToOpenAIExtended(&ent.Model{ModelID: "image-test", ModelCard: &objects.ModelCard{Price: price}}, nil)
	assert.Nil(t, result.Pricing.Input)
	assert.Equal(t, price.Items, result.Pricing.Items)
	assert.Equal(t, 30.0, result.Pricing.Output)
	raw, err := json.Marshal(result)
	assert.NoError(t, err)
	var restored OpenAIModel
	assert.NoError(t, json.Unmarshal(raw, &restored))
	assert.Equal(t, result.Pricing, restored.Pricing)
}

func TestConvertModelToOpenAIExtended_NilModelCard(t *testing.T) {
	remark := "Test description"
	m := &ent.Model{
		ModelID:   "gpt-4",
		Name:      "GPT-4",
		Developer: "openai",
		Type:      model.TypeChat,
		Icon:      "openai",
		Remark:    &remark,
		CreatedAt: time.Unix(1686935002, 0),
		ModelCard: nil,
	}

	result := convertModelToOpenAIExtended(m, nil)

	assert.Equal(t, "gpt-4", result.ID)
	assert.Equal(t, "GPT-4", result.Name)
	assert.Equal(t, "Test description", result.Description)
	assert.Equal(t, "openai", result.OwnedBy)
	assert.Equal(t, "chat", result.Type)
	assert.Equal(t, "openai", result.Icon)
	assert.Equal(t, int64(1686935002), result.Created)
	assert.Nil(t, result.Capabilities)
	assert.Nil(t, result.Pricing)
}

func TestConvertModelToOpenAIExtended_CompleteData(t *testing.T) {
	remark := "GPT-4 is a large multimodal model"
	m := &ent.Model{
		ModelID:   "gpt-4",
		Name:      "GPT-4",
		Developer: "openai",
		Type:      model.TypeChat,
		Icon:      "openai",
		Remark:    &remark,
		CreatedAt: time.Unix(1686935002, 0),
		ModelCard: &objects.ModelCard{Vision: true,
			ToolCall:  true,
			Reasoning: objects.ModelCardReasoning{Supported: true},
			Limit:     objects.ModelCardLimit{Context: 8192, Output: 4096}, Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.03))}}, {ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.06))}}, {ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.015))}}, {ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(0.03))}}}}},
	}

	result := convertModelToOpenAIExtended(m, nil)

	assert.Equal(t, "gpt-4", result.ID)
	assert.Equal(t, "GPT-4", result.Name)
	assert.Equal(t, "GPT-4 is a large multimodal model", result.Description)
	assert.NotNil(t, result.Capabilities)
	assert.True(t, result.Capabilities.Vision)
	assert.True(t, result.Capabilities.ToolCall)
	assert.True(t, result.Capabilities.Reasoning)
	assert.Equal(t, 8192, result.ContextLength)
	assert.Equal(t, 4096, result.MaxOutputTokens)
	assert.NotNil(t, result.Pricing)
	assert.Equal(t, 0.03, *result.Pricing.Input)
	assert.Equal(t, 0.06, result.Pricing.Output)
	assert.Equal(t, 0.015, *result.Pricing.CacheRead)
	assert.Equal(t, 0.03, result.Pricing.CacheWrite)
	assert.Equal(t, "per_1m_tokens", result.Pricing.Unit)
	assert.Equal(t, "USD", result.Pricing.Currency)
}

func TestConvertModelToOpenAIExtended_NilRemark(t *testing.T) {
	m := &ent.Model{
		ModelID:   "gpt-4",
		Name:      "GPT-4",
		Developer: "openai",
		Type:      model.TypeChat,
		Icon:      "openai",
		Remark:    nil,
		CreatedAt: time.Now(),
		ModelCard: nil,
	}

	result := convertModelToOpenAIExtended(m, nil)
	assert.Equal(t, "", result.Description)
	assert.Nil(t, result.Capabilities)
	assert.Nil(t, result.Pricing)
}

func TestConvertModelToOpenAIExtended_PriceAuthority(t *testing.T) {
	card := &objects.ModelCard{
		Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{{
			ItemCode: objects.PriceItemCodeUsage,
			Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.Zero)},
		}}},
		Cost: objects.ModelCardCost{Input: lo.ToPtr(99.0)},
	}
	m := &ent.Model{ModelID: "free", ModelCard: card}
	result := convertModelToOpenAIExtended(m, nil)
	if assert.NotNil(t, result.Pricing) {
		assert.Zero(t, *result.Pricing.Input)
	}
	card.Price = nil
	assert.Nil(t, convertModelToOpenAIExtended(m, nil).Pricing)
	card.Price = &objects.ModelPrice{Items: []objects.ModelPriceItem{}}
	assert.Nil(t, convertModelToOpenAIExtended(m, nil).Pricing)
}
