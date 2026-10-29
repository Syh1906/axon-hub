package objects

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/shopspring/decimal"
)

// ImagePricing charges each completed output separately. Conditions are ordered;
// only the first matching rule contributes a fee for an image.
type ImagePricing struct {
	Automatic bool             `json:"automatic"`
	Rules     []ImagePriceRule `json:"rules"`
	// SavedRules retains the other mode's configuration without using it for billing.
	// Preserve [] separately from nil: an empty draft must not be reinitialized.
	SavedRules []ImagePriceRuleDraft `json:"savedRules"`
}

// ImagePriceRuleDraft preserves unfinished values in the inactive editor.
// Drafts never participate in matching or cost calculation.
type ImagePriceRuleDraft struct {
	Name       string                `json:"name"`
	Fee        string                `json:"fee"`
	Match      string                `json:"match"`
	Conditions []ImageConditionDraft `json:"conditions,omitempty"`
}

type ImageConditionDraft struct {
	Dimension string  `json:"dimension"`
	Operator  string  `json:"operator"`
	Value     float64 `json:"value"`
}

type ImagePriceRule struct {
	Name       string           `json:"name"`
	Fee        decimal.Decimal  `json:"fee"`
	Match      string           `json:"match"`
	Conditions []ImageCondition `json:"conditions,omitempty"`
}

type ImageCondition struct {
	Dimension string `json:"dimension"`
	Operator  string `json:"operator"`
	Value     int    `json:"value"`
}

// ImageOutput contains billing evidence only, never image bytes or signed URLs.
type ImageOutput struct {
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Tier       string `json:"tier,omitempty"`
	TierSource string `json:"tierSource,omitempty"`
	Source     string `json:"source"`
}

func (p *ImagePricing) Equals(other *ImagePricing) bool {
	if p == nil || other == nil {
		return p == other
	}
	equalRule := func(a, b ImagePriceRule) bool {
		return a.Name == b.Name && a.Fee.Equal(b.Fee) && a.Match == b.Match && reflect.DeepEqual(a.Conditions, b.Conditions)
	}
	return p.Automatic == other.Automatic && slices.EqualFunc(p.Rules, other.Rules, equalRule) && reflect.DeepEqual(p.SavedRules, other.SavedRules)
}

func (p *ImagePricing) Validate() error {
	if p == nil || len(p.Rules) == 0 {
		return fmt.Errorf("image pricing rules are required")
	}
	seen := map[string]bool{}
	for i, r := range p.Rules {
		name := r.Name
		if strings.TrimSpace(name) == "" || r.Fee.IsNegative() {
			return fmt.Errorf("image rule %d requires a name and non-negative fee", i+1)
		}
		if p.Automatic {
			if name != "1K" && name != "2K" && name != "4K" {
				return fmt.Errorf("automatic image tier must be 1K, 2K or 4K")
			}
			if seen[name] {
				return fmt.Errorf("duplicate image tier: %s", name)
			}
			seen[name] = true
			continue
		} else if len(r.Conditions) == 0 {
			return fmt.Errorf("image rule %d requires conditions", i+1)
		}
		if r.Match != "any" && r.Match != "all" {
			return fmt.Errorf("image rule match must be any or all")
		}
		for _, c := range r.Conditions {
			if c.Dimension != "width" && c.Dimension != "height" {
				return fmt.Errorf("invalid image dimension")
			}
			if c.Value <= 0 || c.Value > 1000000 {
				return fmt.Errorf("image dimension must be between 1 and 1000000")
			}
			switch c.Operator {
			case "gt", "gte", "eq", "lte", "lt":
			default:
				return fmt.Errorf("invalid image comparison")
			}
		}
	}
	return nil
}

func (p *ImagePricing) MatchImage(image ImageOutput) *ImagePriceRule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if p.Automatic {
			if image.Tier != "" && image.Tier == r.Name {
				return r
			}
			continue
		}
		if image.Width <= 0 || image.Height <= 0 || len(r.Conditions) == 0 {
			continue
		}
		matched := r.Match == "all"
		for _, c := range r.Conditions {
			value := image.Width
			if c.Dimension == "height" {
				value = image.Height
			}
			ok := false
			switch c.Operator {
			case "gt":
				ok = value > c.Value
			case "gte":
				ok = value >= c.Value
			case "eq":
				ok = value == c.Value
			case "lte":
				ok = value <= c.Value
			case "lt":
				ok = value < c.Value
			}
			if r.Match == "all" {
				matched = matched && ok
			} else {
				matched = matched || ok
			}
		}
		if matched {
			return r
		}
	}
	return nil
}
