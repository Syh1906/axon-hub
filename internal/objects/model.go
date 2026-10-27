package objects

import (
	"encoding/json"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type ModelCardReasoning struct {
	Supported bool `json:"supported"`
	Default   bool `json:"default"`
}

type ModelCardModalities struct {
	// "text","image","video"
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type ModelCardCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type ModelCardLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type ModelCard struct {
	Reasoning   ModelCardReasoning  `json:"reasoning"`
	ToolCall    bool                `json:"toolCall"`
	Temperature bool                `json:"temperature"`
	Modalities  ModelCardModalities `json:"modalities"`
	Vision      bool                `json:"vision"`
	// Price is authoritative. Keep null in JSON to distinguish an unpriced card
	// from legacy cost-only persisted data, including old cache entries.
	Price       *ModelPrice    `json:"price"`
	Cost        ModelCardCost  `json:"cost"`
	Limit       ModelCardLimit `json:"limit"`
	Knowledge   string         `json:"knowledge"`
	ReleaseDate string         `json:"releaseDate"`
	LastUpdated string         `json:"lastUpdated"`
}

// UnmarshalJSON upgrades legacy persisted cards without changing database or
// cache envelopes. Explicit price (including null or empty) always wins.
func (c *ModelCard) UnmarshalJSON(data []byte) error {
	type cardJSON ModelCard
	var decoded struct {
		cardJSON
		Price json.RawMessage `json:"price"`
		Cost  *struct {
			Input      *float64 `json:"input"`
			Output     *float64 `json:"output"`
			CacheRead  *float64 `json:"cacheRead"`
			CacheWrite *float64 `json:"cacheWrite"`
		} `json:"cost"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	card := ModelCard(decoded.cardJSON)
	if decoded.Price != nil {
		if err := json.Unmarshal(decoded.Price, &card.Price); err != nil {
			return err
		}
	} else if decoded.Cost != nil {
		items := make([]ModelPriceItem, 0, 4)
		add := func(code PriceItemCode, value *float64) {
			if value != nil && *value >= 0 {
				items = append(items, ModelPriceItem{
					ItemCode: code,
					Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromFloat(*value))},
				})
			}
		}
		add(PriceItemCodeUsage, decoded.Cost.Input)
		add(PriceItemCodeCompletion, decoded.Cost.Output)
		add(PriceItemCodePromptCachedToken, decoded.Cost.CacheRead)
		add(PriceItemCodeWriteCachedTokens, decoded.Cost.CacheWrite)
		if len(items) != 0 {
			card.Price = &ModelPrice{Items: items}
		}
	}
	card.Cost = card.PriceSummary()
	*c = card
	return nil
}

// MarshalJSON never persists Cost as an independent pricing source.
func (c ModelCard) MarshalJSON() ([]byte, error) {
	type cardJSON ModelCard
	c.Cost = c.PriceSummary()
	return json.Marshal(cardJSON(c))
}

// PriceSummary describes base token rates only; volume tiers and schedules
// remain available in Price. Flat request fees have no per-token summary.
func (c *ModelCard) PriceSummary() ModelCardCost {
	var summary ModelCardCost
	if c == nil || c.Price == nil {
		return summary
	}
	for _, item := range c.Price.Items {
		var rate decimal.Decimal
		switch item.Pricing.Mode {
		case PricingModeUsagePerUnit:
			if item.Pricing.UsagePerUnit == nil {
				continue
			}
			rate = *item.Pricing.UsagePerUnit
		case PricingModeTiered, PricingModeVolume:
			if item.Pricing.UsageTiered == nil || len(item.Pricing.UsageTiered.Tiers) == 0 {
				continue
			}
			rate = item.Pricing.UsageTiered.Tiers[0].PricePerUnit
		default:
			continue
		}
		value := rate.InexactFloat64()
		switch item.ItemCode {
		case PriceItemCodeUsage:
			summary.Input = value
		case PriceItemCodeCompletion:
			summary.Output = value
		case PriceItemCodePromptCachedToken:
			summary.CacheRead = value
		case PriceItemCodeWriteCachedTokens:
			summary.CacheWrite = value
		}
	}
	return summary
}

type ModelSettings struct {
	DisableDeveloperSettingsInheritance bool                `json:"disableDeveloperSettingsInheritance"`
	Associations                        []*ModelAssociation `json:"associations"`
	LoadBalancerStrategy                string              `json:"loadBalancerStrategy"`
	TraceStickyMode                     string              `json:"traceStickyMode"`
}

const (
	ModelAssociationConditionFieldPromptTokens        = "prompt_tokens"
	ModelAssociationConditionFieldStream              = "stream"
	ModelAssociationConditionFieldRequestFormat       = "request_format"
	ModelAssociationConditionFieldDailyTime           = "daily_time"
	ModelAssociationConditionFieldHasImage            = "has_image"
	ModelAssociationConditionFieldHasVideo            = "has_video"
	ModelAssociationConditionFieldHasDocument         = "has_document"
	ModelAssociationConditionFieldHasAudio            = "has_audio"
	ModelAssociationConditionFieldRequestHeader       = "request_header"
	ModelAssociationConditionFieldRequestHeaderPrefix = "request_header."
)

type ModelAssociation struct {
	// channel_model: the specified model id in the specified channel
	// channel_regex: the specified pattern in the specified channel
	// regex: the pattern for all channels
	// model: the specified model id
	// channel_tags_model: the specified model id in channels with specified tags (OR logic)
	// channel_tags_regex: the specified pattern in channels with specified tags (OR logic)
	// channel_request: use the incoming model ID in the specified channel
	Type             string                       `json:"type"`
	Priority         int                          `json:"priority"` // Lower value = higher priority, default 0
	Disabled         bool                         `json:"disabled"`
	When             *ModelAssociationWhen        `json:"when,omitempty"`
	ChannelModel     *ChannelModelAssociation     `json:"channelModel"`
	ChannelRegex     *ChannelRegexAssociation     `json:"channelRegex"`
	Regex            *RegexAssociation            `json:"regex"`
	ModelID          *ModelIDAssociation          `json:"modelId"`
	ChannelTagsModel *ChannelTagsModelAssociation `json:"channelTagsModel"`
	ChannelTagsRegex *ChannelTagsRegexAssociation `json:"channelTagsRegex"`
	ChannelRequest   *ChannelRequestAssociation   `json:"channelRequest,omitempty"`
}

type ModelAssociationWhen struct {
	Enabled   bool       `json:"enabled"`
	Condition *Condition `json:"condition,omitempty"`
}

type ExcludeAssociation struct {
	ChannelNamePattern string   `json:"channelNamePattern"`
	ChannelIds         []int    `json:"channelIds"`
	ChannelTags        []string `json:"channelTags"`
}

type ChannelModelAssociation struct {
	ChannelID int    `json:"channelId"`
	ModelID   string `json:"modelId"`
}

type ChannelRequestAssociation struct {
	ChannelID int `json:"channelId"`
}

type ChannelRegexAssociation struct {
	ChannelID int    `json:"channelId"`
	Pattern   string `json:"pattern"`
}

type RegexAssociation struct {
	Pattern string                `json:"pattern"`
	Exclude []*ExcludeAssociation `json:"exclude"`
}

type ModelIDAssociation struct {
	ModelID string                `json:"modelId"`
	Exclude []*ExcludeAssociation `json:"exclude"`
}

type ChannelTagsModelAssociation struct {
	ChannelTags []string `json:"channelTags"`
	ModelID     string   `json:"modelId"`
}

type ChannelTagsRegexAssociation struct {
	ChannelTags []string `json:"channelTags"`
	Pattern     string   `json:"pattern"`
}
