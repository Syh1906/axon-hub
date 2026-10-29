package objects

import (
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

type PricingMode string

const (
	PricingModeImageFixed PricingMode = "image_fixed"
	// PricingModeFlatFee means the request is charged a fixed fee.
	PricingModeFlatFee PricingMode = "flat_fee"

	// PricingModeUsagePerUnit means the request is charged a fee bases on the token usage.
	// e.g. $0.01 per token, if the usage is 1,500 then the fee is $0.01 x 1,500 = $15.00.
	PricingModeUsagePerUnit PricingMode = "usage_per_unit"

	// PricingModeTiered means the request is charged a fee based on the token usage tiers.
	// Each tier segment is billed separately at its own rate.
	// e.g. tiers are [{upTo: 1000, pricePerUnit: $0.01}, {upTo: nil, pricePerUnit: $0.02}],
	// if usage is 1,500 then the fee is (1000/1e6)*$0.01 + (500/1e6)*$0.02.
	PricingModeTiered PricingMode = "usage_tiered"

	// PricingModeVolume means the request is charged a fee based on the token usage volume tiers.
	// The tier matched by the total token count determines the unit price for ALL tokens.
	// e.g. tiers are [{upTo: 1000, pricePerUnit: $0.01}, {upTo: nil, pricePerUnit: $0.02}],
	// if usage is 1,500 then all tokens are billed at $0.02 => (1500/1e6)*$0.02.
	PricingModeVolume PricingMode = "usage_volume"
)

type Pricing struct {
	Mode  PricingMode   `json:"mode"`
	Image *ImagePricing `json:"image,omitempty"`

	// FlatFee is the fixed fee for the pricing.
	FlatFee *decimal.Decimal `json:"flatFee,omitempty"`

	// UsagePerUnit is the price per token for the pricing.
	UsagePerUnit *decimal.Decimal `json:"usagePerUnit,omitempty"`

	// UsageTiered is the tiered pricing for the pricing.
	// Used by both UsageTiered and UsageVolume modes — they share the same data structure
	// but differ in calculation logic.
	UsageTiered *TieredPricing `json:"usageTiered,omitempty"`
}

func (p *Pricing) Equals(other *Pricing) bool {
	if p == nil || other == nil {
		return p == other
	}

	if p.Mode != other.Mode {
		return false
	}

	switch p.Mode {
	case PricingModeImageFixed:
		return p.Image.Equals(other.Image)
	case PricingModeFlatFee:
		if (p.FlatFee == nil) != (other.FlatFee == nil) {
			return false
		}

		if p.FlatFee != nil && !p.FlatFee.Equal(*other.FlatFee) {
			return false
		}
	case PricingModeUsagePerUnit:
		if (p.UsagePerUnit == nil) != (other.UsagePerUnit == nil) {
			return false
		}

		if p.UsagePerUnit != nil && !p.UsagePerUnit.Equal(*other.UsagePerUnit) {
			return false
		}
	case PricingModeTiered, PricingModeVolume:
		return p.UsageTiered.Equals(other.UsageTiered)
	}

	return true
}

func (p *Pricing) Validate() error {
	if p == nil {
		return fmt.Errorf("pricing is nil")
	}

	switch p.Mode {
	case PricingModeImageFixed:
		return p.Image.Validate()
	case PricingModeFlatFee:
		if p.FlatFee == nil {
			return fmt.Errorf("flatFee is required")
		}

		if p.FlatFee.IsNegative() {
			return fmt.Errorf("flatFee must be non-negative")
		}
	case PricingModeUsagePerUnit:
		if p.UsagePerUnit == nil {
			return fmt.Errorf("usagePerUnit is required")
		}

		if p.UsagePerUnit.IsNegative() {
			return fmt.Errorf("usagePerUnit must be non-negative")
		}
	case PricingModeTiered, PricingModeVolume:
		if p.UsageTiered == nil {
			return fmt.Errorf("usageTiered is required")
		}

		if err := p.UsageTiered.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown pricing mode: %s", p.Mode)
	}

	return nil
}

func (p *TieredPricing) Validate() error {
	if p == nil {
		return fmt.Errorf("usageTiered is nil")
	}

	if len(p.Tiers) == 0 {
		return fmt.Errorf("tiers is required")
	}

	lastIdx := len(p.Tiers) - 1
	for i := range p.Tiers {
		tier := p.Tiers[i]
		if tier.PricePerUnit.IsNegative() {
			return fmt.Errorf("tiers[%d].pricePerUnit must be non-negative", i)
		}

		if i == lastIdx {
			if tier.UpTo != nil {
				return fmt.Errorf("tiers[%d].upTo must be null", i)
			}

			continue
		}

		if tier.UpTo == nil {
			return fmt.Errorf("tiers[%d].upTo is required", i)
		}
	}

	return nil
}

func (p *PromptWriteCacheVariant) Validate() error {
	if p == nil {
		return fmt.Errorf("promptWriteCacheVariant is nil")
	}
	if p.Pricing.Mode == PricingModeImageFixed {
		return fmt.Errorf("image pricing is not a cache-write variant")
	}

	if err := p.Pricing.Validate(); err != nil {
		return fmt.Errorf("pricing: %w", err)
	}

	return nil
}

func (i *ModelPriceItem) Validate() error {
	if i == nil {
		return fmt.Errorf("modelPriceItem is nil")
	}

	if err := i.Pricing.Validate(); err != nil {
		return fmt.Errorf("pricing: %w", err)
	}

	for idx := range i.PromptWriteCacheVariants {
		if err := i.PromptWriteCacheVariants[idx].Validate(); err != nil {
			return fmt.Errorf("promptWriteCacheVariants[%d]: %w", idx, err)
		}
	}

	return nil
}

func (p *ModelPrice) Validate() error {
	if p == nil {
		return fmt.Errorf("modelPrice is nil")
	}

	if p.Multiplier != nil && p.Multiplier.IsNegative() {
		return fmt.Errorf("multiplier must be non-negative")
	}

	if len(p.VolumeTiers) > 0 {
		if err := p.validateVolumeTiers(); err != nil {
			return err
		}
	} else {
		for idx := range p.Items {
			if err := p.Items[idx].Validate(); err != nil {
				return fmt.Errorf("items[%d]: %w", idx, err)
			}
		}
	}

	if p.Schedule != nil {
		if err := p.Schedule.Validate(); err != nil {
			return fmt.Errorf("schedule: %w", err)
		}
	}

	return nil
}

func (p *ModelPrice) validateVolumeTiers() error {
	if err := validateVolumePriceItems(p.Items); err != nil {
		return err
	}

	for idx := range p.VolumeTiers {
		tier := &p.VolumeTiers[idx]
		if tier.Above < 0 {
			return fmt.Errorf("volumeTiers[%d].above must be non-negative", idx)
		}

		if idx > 0 && tier.Above <= p.VolumeTiers[idx-1].Above {
			return fmt.Errorf("volumeTiers[%d].above must be strictly increasing", idx)
		}

		if len(tier.Items) != len(p.Items) {
			return fmt.Errorf("volumeTiers[%d].items must match the base item set", idx)
		}

		if err := validateVolumePriceItems(tier.Items); err != nil {
			return fmt.Errorf("volumeTiers[%d]: %w", idx, err)
		}

		for itemIdx := range tier.Items {
			item := &tier.Items[itemIdx]
			baseIdx := slices.IndexFunc(p.Items, func(base ModelPriceItem) bool {
				return base.ItemCode == item.ItemCode
			})
			if baseIdx < 0 {
				return fmt.Errorf("volumeTiers[%d].items[%d].itemCode is not in the base item set", idx, itemIdx)
			}

			baseVariants := p.Items[baseIdx].PromptWriteCacheVariants
			if len(item.PromptWriteCacheVariants) != len(baseVariants) {
				return fmt.Errorf("volumeTiers[%d].items[%d].promptWriteCacheVariants must match the base variant set", idx, itemIdx)
			}

			for _, variant := range item.PromptWriteCacheVariants {
				if !slices.ContainsFunc(baseVariants, func(base PromptWriteCacheVariant) bool {
					return base.VariantCode == variant.VariantCode
				}) {
					return fmt.Errorf("volumeTiers[%d].items[%d].promptWriteCacheVariants contains a variant outside the base variant set", idx, itemIdx)
				}
			}
		}
	}

	return nil
}

func validateVolumePriceItems(items []ModelPriceItem) error {
	if len(items) == 0 {
		return fmt.Errorf("items is required for volume pricing")
	}

	for idx := range items {
		item := &items[idx]
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", idx, err)
		}

		if item.Pricing.Mode != PricingModeUsagePerUnit {
			return fmt.Errorf("items[%d].pricing.mode must be usage_per_unit for volume pricing", idx)
		}

		if slices.ContainsFunc(items[:idx], func(previous ModelPriceItem) bool {
			return previous.ItemCode == item.ItemCode
		}) {
			return fmt.Errorf("items[%d].itemCode must be unique", idx)
		}

		if len(item.PromptWriteCacheVariants) > 0 && item.ItemCode != PriceItemCodeWriteCachedTokens {
			return fmt.Errorf("items[%d].promptWriteCacheVariants requires prompt_write_cached_tokens", idx)
		}

		for variantIdx, variant := range item.PromptWriteCacheVariants {
			if variant.Pricing.Mode != PricingModeUsagePerUnit {
				return fmt.Errorf("items[%d].promptWriteCacheVariants[%d].pricing.mode must be usage_per_unit for volume pricing", idx, variantIdx)
			}

			if slices.ContainsFunc(item.PromptWriteCacheVariants[:variantIdx], func(previous PromptWriteCacheVariant) bool {
				return previous.VariantCode == variant.VariantCode
			}) {
				return fmt.Errorf("items[%d].promptWriteCacheVariants[%d].variantCode must be unique", idx, variantIdx)
			}
		}
	}

	return nil
}

type TieredPricing struct {
	Tiers []PriceTier `json:"tiers"`
}

func (p *TieredPricing) Equals(other *TieredPricing) bool {
	if p == nil || other == nil {
		return p == other
	}

	if len(p.Tiers) != len(other.Tiers) {
		return false
	}

	for i := range p.Tiers {
		if !p.Tiers[i].Equals(&other.Tiers[i]) {
			return false
		}
	}

	return true
}

// PriceTier is the price tier for the tiered pricing.
type PriceTier struct {
	// UpTo is the upper bound of the token usage for the price tier.
	// If the upper bound is nil, it means no upper bound, it must be the last price tier.
	UpTo *int64 `json:"upTo,omitempty"`

	// PricePerUnit is the price per token for the price tier.
	PricePerUnit decimal.Decimal `json:"pricePerUnit"`
}

func (p *PriceTier) Equals(other *PriceTier) bool {
	if p == nil || other == nil {
		return p == other
	}

	if (p.UpTo == nil) != (other.UpTo == nil) {
		return false
	}

	if p.UpTo != nil && *p.UpTo != *other.UpTo {
		return false
	}

	return p.PricePerUnit.Equal(other.PricePerUnit)
}

type PriceItemCode string

const (
	// PriceItemCodeUsage is the price item code for the token usage.
	PriceItemCodeUsage PriceItemCode = "prompt_tokens"

	// PriceItemCodeCompletion is the price item code for the token completion.
	PriceItemCodeCompletion PriceItemCode = "completion_tokens"

	// PriceItemCodePromptCachedToken is the price item code for the cached token usage.
	PriceItemCodePromptCachedToken PriceItemCode = "prompt_cached_tokens"

	// PriceItemCodeWriteCachedTokens is the price item code for the cached token write.
	//nolint:gosec // not token.
	PriceItemCodeWriteCachedTokens PriceItemCode = "prompt_write_cached_tokens"
)

type PromptWriteCacheVariantCode string

const (
	// PromptWriteCacheVariantCode5Min is the variant code for cached token write in 5 minutes.
	PromptWriteCacheVariantCode5Min PromptWriteCacheVariantCode = "five_min"

	// PromptWriteCacheVariantCode1Hour is the variant code for cached token write in 1 hour.
	PromptWriteCacheVariantCode1Hour PromptWriteCacheVariantCode = "one_hour"
)

// PromptWriteCacheVariant is the variant for cached token write.
type PromptWriteCacheVariant struct {
	// VariantCode is the code of the variant.
	VariantCode PromptWriteCacheVariantCode `json:"variantCode"`

	// Pricing is the pricing for the variant.
	Pricing Pricing `json:"pricing"`
}

func (p *PromptWriteCacheVariant) Equals(other *PromptWriteCacheVariant) bool {
	if p == nil || other == nil {
		return p == other
	}

	if p.VariantCode != other.VariantCode {
		return false
	}

	return p.Pricing.Equals(&other.Pricing)
}

// FindPromptWriteCacheVariantPricing finds the variant pricing for the item prompt write cached tokens.
// If the variant pricing is not found, it will return the item pricing.
func (i *ModelPriceItem) FindPromptWriteCacheVariantPricing(variantCode PromptWriteCacheVariantCode) Pricing {
	for _, v := range i.PromptWriteCacheVariants {
		if v.VariantCode == variantCode {
			return v.Pricing
		}
	}

	return i.Pricing
}

type ModelPriceItem struct {
	// ItemCode is the code of the item.
	ItemCode PriceItemCode `json:"itemCode"`

	// Pricing is the pricing for the item.
	Pricing Pricing `json:"pricing"`

	// PromptWriteCacheVariants is the list of variants for the item prompt write cached tokens.
	// If the variants present, it will find the variant price first, if not hit, it will use the item pricing.
	PromptWriteCacheVariants []PromptWriteCacheVariant `json:"promptWriteCacheVariants,omitempty"`
}

func (i *ModelPriceItem) Equals(other *ModelPriceItem) bool {
	if i == nil || other == nil {
		return i == other
	}

	if i.ItemCode != other.ItemCode {
		return false
	}

	if !i.Pricing.Equals(&other.Pricing) {
		return false
	}

	if len(i.PromptWriteCacheVariants) != len(other.PromptWriteCacheVariants) {
		return false
	}

	for idx := range i.PromptWriteCacheVariants {
		if !i.PromptWriteCacheVariants[idx].Equals(&other.PromptWriteCacheVariants[idx]) {
			return false
		}
	}

	return true
}

// ModelPrice is the price for the thing.
type ModelPrice struct {
	// Multiplier is the channel multiplier captured with this price version.
	// Standard model prices and legacy snapshots omit it and use one.
	Multiplier *decimal.Decimal `json:"multiplier,omitempty"`

	// Items is the list of price items for the price.
	Items []ModelPriceItem `json:"items"`

	// VolumeTiers replaces all item rates when total prompt tokens strictly exceed Above.
	VolumeTiers []ModelPriceVolumeTier `json:"volumeTiers,omitempty"`

	// Schedule is the optional time-based price override configuration.
	Schedule *PriceSchedule `json:"schedule,omitempty"`
}

// ModelPriceVolumeTier applies one complete set of per-unit rates to all usage.
type ModelPriceVolumeTier struct {
	Above int64            `json:"above"`
	Items []ModelPriceItem `json:"items"`
}

func (t *ModelPriceVolumeTier) Equals(other *ModelPriceVolumeTier) bool {
	if t == nil || other == nil {
		return t == other
	}

	if t.Above != other.Above || len(t.Items) != len(other.Items) {
		return false
	}

	for i := range t.Items {
		if !t.Items[i].Equals(&other.Items[i]) {
			return false
		}
	}

	return true
}

// EffectiveMultiplier preserves legacy snapshots while allowing explicit zero.
func (p *ModelPrice) EffectiveMultiplier() decimal.Decimal {
	if p.Multiplier == nil {
		return decimal.NewFromInt(1)
	}

	return *p.Multiplier
}

func (p *ModelPrice) Equals(other ModelPrice) bool {
	if !p.EffectiveMultiplier().Equal(other.EffectiveMultiplier()) {
		return false
	}

	if len(p.Items) != len(other.Items) {
		return false
	}

	for i := range p.Items {
		if !p.Items[i].Equals(&other.Items[i]) {
			return false
		}
	}

	if len(p.VolumeTiers) != len(other.VolumeTiers) {
		return false
	}

	for i := range p.VolumeTiers {
		if !p.VolumeTiers[i].Equals(&other.VolumeTiers[i]) {
			return false
		}
	}

	if (p.Schedule == nil) != (other.Schedule == nil) {
		return false
	}

	if p.Schedule != nil && !p.Schedule.Equals(other.Schedule) {
		return false
	}

	return true
}

// PriceSchedule defines time-based price override configuration.
type PriceSchedule struct {
	// Timezone is the IANA timezone name, e.g. "Asia/Shanghai".
	Timezone string `json:"timezone"`

	// Overrides is the list of time-based price override rules.
	Overrides []PriceOverride `json:"overrides"`
}

func (s *PriceSchedule) Equals(other *PriceSchedule) bool {
	if s == nil || other == nil {
		return s == other
	}

	if s.Timezone != other.Timezone {
		return false
	}

	if len(s.Overrides) != len(other.Overrides) {
		return false
	}

	for i := range s.Overrides {
		if !s.Overrides[i].Equals(&other.Overrides[i]) {
			return false
		}
	}

	return true
}

func (s *PriceSchedule) Validate() error {
	if s == nil {
		return nil
	}

	if s.Timezone == "" {
		return fmt.Errorf("schedule timezone is required")
	}

	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid timezone: %s", s.Timezone)
	}

	if len(s.Overrides) == 0 {
		return fmt.Errorf("schedule overrides is required")
	}

	for i, o := range s.Overrides {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("overrides[%d]: %w", i, err)
		}
	}

	return nil
}

// PriceOverride defines a single time-based price override rule.
type PriceOverride struct {
	// Name is the human-readable name for this override, e.g. "Night Discount".
	Name string `json:"name"`

	// Priority determines the order of evaluation. Lower values have higher priority.
	Priority int `json:"priority"`

	// When defines the conditions for this override to take effect.
	When OverrideWhen `json:"when"`

	// Items is the price configuration to use when this override is active.
	Items []ModelPriceItem `json:"items"`
}

func (o *PriceOverride) Equals(other *PriceOverride) bool {
	if o == nil || other == nil {
		return o == other
	}

	if o.Name != other.Name {
		return false
	}

	if o.Priority != other.Priority {
		return false
	}

	if !o.When.Equals(&other.When) {
		return false
	}

	if len(o.Items) != len(other.Items) {
		return false
	}

	for i := range o.Items {
		if !o.Items[i].Equals(&other.Items[i]) {
			return false
		}
	}

	return true
}

func (o *PriceOverride) Validate() error {
	if o == nil {
		return nil
	}

	if err := o.When.Validate(); err != nil {
		return fmt.Errorf("when: %w", err)
	}

	if len(o.Items) == 0 {
		return fmt.Errorf("items is required")
	}

	for j, item := range o.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", j, err)
		}
	}

	return nil
}

// OverrideWhen defines the conditions for a price override to take effect.
type OverrideWhen struct {
	// DailyTime is the daily time range with structured start/end times.
	DailyTime *DailyTimeRange `json:"dailyTime,omitempty"`

	// Weekdays is the list of weekdays (1=Monday, 7=Sunday) when the override is active.
	Weekdays []int `json:"weekdays,omitempty"`

	// DateRange is the date range when the override is active.
	DateRange *DateRange `json:"dateRange,omitempty"`
}

func (w *OverrideWhen) Equals(other *OverrideWhen) bool {
	if w == nil || other == nil {
		return w == other
	}

	if (w.DailyTime == nil) != (other.DailyTime == nil) {
		return false
	}

	if w.DailyTime != nil && !w.DailyTime.Equals(other.DailyTime) {
		return false
	}

	if len(w.Weekdays) != len(other.Weekdays) {
		return false
	}

	for i := range w.Weekdays {
		if w.Weekdays[i] != other.Weekdays[i] {
			return false
		}
	}

	if (w.DateRange == nil) != (other.DateRange == nil) {
		return false
	}

	if w.DateRange != nil && !w.DateRange.Equals(other.DateRange) {
		return false
	}

	return true
}

func (w *OverrideWhen) Validate() error {
	if w == nil {
		return fmt.Errorf("when is required")
	}

	if w.DailyTime == nil && len(w.Weekdays) == 0 && w.DateRange == nil {
		return fmt.Errorf("at least one condition (dailyTime, weekdays, or dateRange) is required")
	}

	if w.DailyTime != nil {
		if err := w.DailyTime.Validate(); err != nil {
			return fmt.Errorf("dailyTime: %w", err)
		}
	}

	for i, d := range w.Weekdays {
		if d < 1 || d > 7 {
			return fmt.Errorf("weekdays[%d] must be between 1 and 7, got %d", i, d)
		}
	}

	if w.DateRange != nil {
		if err := w.DateRange.Validate(); err != nil {
			return fmt.Errorf("dateRange: %w", err)
		}
	}

	return nil
}

// DailyTimeRange defines a daily time range.
// Start and End are in "HH:mm" format (e.g. "03:00", "18:30").
type DailyTimeRange struct {
	// Start is the start time in "HH:mm" format.
	Start string `json:"start"`

	// End is the end time in "HH:mm" format.
	End string `json:"end"`
}

func (d *DailyTimeRange) Equals(other *DailyTimeRange) bool {
	if d == nil || other == nil {
		return d == other
	}

	return d.Start == other.Start && d.End == other.End
}

func (d *DailyTimeRange) Validate() error {
	if d == nil {
		return nil
	}

	if err := validateDailyTime(d.Start); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	if err := validateDailyTime(d.End); err != nil {
		return fmt.Errorf("end: %w", err)
	}

	return nil
}

// validateDailyTime checks that a string is in "HH:mm" format with valid hour (0-23) and minute (0-59).
func validateDailyTime(s string) error {
	if len(s) != 5 || s[2] != ':' {
		return fmt.Errorf("invalid time format %q, expected \"HH:mm\"", s)
	}

	hour := int(s[0]-'0')*10 + int(s[1]-'0')
	minute := int(s[3]-'0')*10 + int(s[4]-'0')

	if hour < 0 || hour > 23 {
		return fmt.Errorf("hour must be between 0 and 23, got %d", hour)
	}

	if minute < 0 || minute > 59 {
		return fmt.Errorf("minute must be between 0 and 59, got %d", minute)
	}

	return nil
}

// DateRange defines a date range.
type DateRange struct {
	// Start is the start date in "YYYY-MM-DD" format (inclusive).
	Start string `json:"start"`

	// End is the end date in "YYYY-MM-DD" format (inclusive).
	End string `json:"end"`
}

func (d *DateRange) Equals(other *DateRange) bool {
	if d == nil || other == nil {
		return d == other
	}

	return d.Start == other.Start && d.End == other.End
}

func (d *DateRange) Validate() error {
	if d == nil {
		return nil
	}

	if d.Start == "" {
		return fmt.Errorf("start is required")
	}

	if d.End == "" {
		return fmt.Errorf("end is required")
	}

	start, err := time.Parse("2006-01-02", d.Start)
	if err != nil {
		return fmt.Errorf("invalid start date format: %s", d.Start)
	}

	end, err := time.Parse("2006-01-02", d.End)
	if err != nil {
		return fmt.Errorf("invalid end date format: %s", d.End)
	}

	if end.Before(start) {
		return fmt.Errorf("end date must be after or equal to start date")
	}

	return nil
}
