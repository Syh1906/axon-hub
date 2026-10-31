package objects

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/shopspring/decimal"
)

type ServiceTierBasis string

const (
	ServiceTierBasisUpstream ServiceTierBasis = "upstream"
	ServiceTierBasisClient   ServiceTierBasis = "client"
)

type ServiceTierPriceSource string

const (
	ServiceTierPriceOfficial ServiceTierPriceSource = "official"
	ServiceTierPriceCustom   ServiceTierPriceSource = "custom"
	ServiceTierPriceModel    ServiceTierPriceSource = "model"
)

type ServiceTierSettings struct {
	Enabled bool             `json:"enabled"`
	Basis   ServiceTierBasis `json:"basis"`
}

func (s *ServiceTierSettings) Validate() error {
	if s != nil && s.Basis != ServiceTierBasisUpstream && s.Basis != ServiceTierBasisClient {
		return fmt.Errorf("invalid service tier billing basis")
	}
	return nil
}

type ServiceTierPrice struct {
	Items          []ServiceTierItemRule  `json:"items,omitempty"`
	Unpriced       bool                   `json:"unpriced,omitempty"`
	Application    string                 `json:"application,omitempty"`
	ContextAbove   *int64                 `json:"contextAbove,omitempty"`
	ContextUpTo    *int64                 `json:"contextUpTo,omitempty"`
	SourceURL      string                 `json:"sourceURL,omitempty"`
	VerifiedAt     string                 `json:"verifiedAt,omitempty"`
	Currency       string                 `json:"currency,omitempty"`
	Unit           string                 `json:"unit,omitempty"`
	Source         ServiceTierPriceSource `json:"source"`
	Multiplier     *decimal.Decimal       `json:"multiplier,omitempty"`
	ReferenceModel string                 `json:"referenceModel,omitempty"`
	Version        string                 `json:"version,omitempty"`
}
type ServiceTierPrices struct {
	Fast      *ServiceTierPrice `json:"fast,omitempty"`
	Ultrafast *ServiceTierPrice `json:"ultrafast,omitempty"`
}

func (p *ServiceTierPrices) Validate() error {
	if p == nil {
		return nil
	}
	for mode, rate := range map[string]*ServiceTierPrice{"fast": p.Fast, "ultrafast": p.Ultrafast} {
		if rate == nil {
			continue
		}
		if rate.Source != ServiceTierPriceOfficial && rate.Source != ServiceTierPriceCustom && rate.Source != ServiceTierPriceModel {
			return fmt.Errorf("invalid %s price source", mode)
		}
		if !rate.Unpriced && rate.Multiplier == nil && len(rate.Items) == 0 {
			return fmt.Errorf("%s requires a default multiplier or item rules", mode)
		}
		if rate.Multiplier != nil && rate.Multiplier.IsNegative() {
			return fmt.Errorf("%s multiplier must be non-negative", mode)
		}
		if rate.Unpriced && (rate.Multiplier != nil || len(rate.Items) > 0) {
			return fmt.Errorf("unpriced mode cannot contain prices")
		}
		if rate.ContextAbove != nil && *rate.ContextAbove < 0 || rate.ContextUpTo != nil && *rate.ContextUpTo < 0 {
			return fmt.Errorf("invalid official context range")
		}
		if rate.ContextAbove != nil && rate.ContextUpTo != nil && *rate.ContextAbove >= *rate.ContextUpTo {
			return fmt.Errorf("empty official context range")
		}
		seen := map[string]bool{}
		for _, item := range rate.Items {
			if err := item.Validate(); err != nil {
				return err
			}
			key := string(item.ItemCode) + ":" + string(item.VariantCode)
			if seen[key] {
				return fmt.Errorf("duplicate service tier item")
			}
			seen[key] = true
		}
		if rate.Source == ServiceTierPriceOfficial && !matchesOfficialServiceTierPrice(mode, rate) {
			return fmt.Errorf("%s official price does not match its reference model/version", mode)
		}
	}
	return nil
}
func (p *ServiceTierPrices) Equals(other *ServiceTierPrices) bool {
	if p == nil || other == nil {
		return p == other
	}
	return p.Fast.equals(other.Fast) && p.Ultrafast.equals(other.Ultrafast)
}

func (p *ServiceTierPrice) equals(other *ServiceTierPrice) bool {
	if p == nil || other == nil {
		return p == other
	}
	// Decimal JSON is canonical; include every rule and reference field.
	left, _ := json.Marshal(p)
	right, _ := json.Marshal(other)
	return reflect.DeepEqual(left, right)
}

// ServiceTierEvidence is persisted with settlement, not exposed as provider usage.
type ServiceTierEvidence struct {
	Settings  ServiceTierSettings `json:"settings"`
	Client    string              `json:"client"`
	Outbound  string              `json:"outbound"`
	Upstream  string              `json:"upstream"`
	Observed  bool                `json:"observed"`
	Conflict  bool                `json:"conflict"`
	Terminal  bool                `json:"terminal,omitempty"`
	LastValid string              `json:"lastValid,omitempty"`
}

func ServiceTierValue(body []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return ""
	}
	value := fields["service_tier"]
	if len(value) == 0 {
		return ""
	}
	var tier string
	if json.Unmarshal(value, &tier) != nil {
		return "[invalid]"
	}
	if len(tier) > 128 {
		return "[invalid]"
	}
	return strings.TrimSpace(tier)
}
func NormalizeServiceTier(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "default", "standard":
		return "standard", true
	case "fast", "priority":
		return "fast", true
	case "ultrafast":
		return "ultrafast", true
	case "flex", "scale":
		return strings.ToLower(value), true
	default:
		return "", false
	}
}
func (e *ServiceTierEvidence) Observe(body []byte) {
	if e == nil {
		return
	}
	value := ServiceTierValue(body)
	if value == "" {
		return
	}
	if current, valid := NormalizeServiceTier(value); valid {
		if e.LastValid != "" && e.LastValid != current {
			e.Conflict = true
		}
		e.LastValid = current
	}
	e.Upstream = value
	e.Observed = true
}

type ServiceTierDecision struct {
	Mode           string `json:"mode"`
	Basis          string `json:"basis"`
	Client         string `json:"client"`
	Outbound       string `json:"outbound"`
	Upstream       string `json:"upstream"`
	Confirmed      bool   `json:"confirmed"`
	Reason         string `json:"reason"`
	Source         string `json:"source"`
	ReferenceModel string `json:"referenceModel"`
	Version        string `json:"version"`
}

func (e *ServiceTierEvidence) Decision() ServiceTierDecision {
	d := ServiceTierDecision{Mode: "standard", Basis: "disabled", Reason: "disabled"}
	if e == nil {
		d.Reason = "legacy"
		return d
	}
	d.Client, d.Outbound, d.Upstream = e.Client, e.Outbound, e.Upstream
	if !e.Settings.Enabled {
		return d
	}
	d.Basis = "client"
	d.Reason = "missing_upstream"
	mode, ok := NormalizeServiceTier(e.Client)
	if ok {
		d.Mode = mode
	}
	if !ok {
		d.Reason = "unspecified_client"
	}
	if e.Settings.Basis == ServiceTierBasisClient {
		d.Reason = "channel_client"
	} else if mode, valid := NormalizeServiceTier(e.Upstream); valid && !e.Conflict {
		d.Mode = mode
		d.Basis = "upstream"
		d.Confirmed = true
		d.Reason = "upstream"
	} else if e.Conflict {
		d.Reason = "conflicting_upstream"
	} else if e.Observed {
		d.Reason = "invalid_upstream"
	}
	if d.Mode != "fast" && d.Mode != "ultrafast" {
		return d
	}
	return d
}

func (e *ServiceTierEvidence) Resolve(prices *ServiceTierPrices) (ServiceTierDecision, *decimal.Decimal) {
	d := e.Decision()
	if d.Mode != "fast" && d.Mode != "ultrafast" {
		one := decimal.NewFromInt(1)
		return d, &one
	}
	var rate *ServiceTierPrice
	if prices != nil {
		rate = prices.Fast
		if d.Mode == "ultrafast" {
			rate = prices.Ultrafast
		}
	}
	if rate == nil || rate.Unpriced || (rate.Multiplier == nil && len(rate.Items) == 0) {
		d.Reason = "unpriced_mode"
		return d, nil
	}
	d.Source = string(rate.Source)
	d.ReferenceModel = rate.ReferenceModel
	d.Version = rate.Version
	return d, rate.Multiplier
}

type ContextPricing struct {
	Kind        string `json:"kind"`
	InputTokens int64  `json:"inputTokens"`
	Threshold   *int64 `json:"threshold,omitempty"`
	LongContext bool   `json:"longContext"`
	Name        string `json:"name"`
}
type CostPricingEvidence struct {
	ItemMethod        *ServiceTierItemMethod `json:"itemMethod,omitempty"`
	RuleSource        string                 `json:"ruleSource,omitempty"`
	BaseUnitPrice     *decimal.Decimal       `json:"baseUnitPrice,omitempty"`
	FinalUnitPrice    *decimal.Decimal       `json:"finalUnitPrice,omitempty"`
	ModeSubtotal      *decimal.Decimal       `json:"modeSubtotal,omitempty"`
	ModeTierSubtotals []decimal.Decimal      `json:"modeTierSubtotals,omitempty"`
	Reference         *ServiceTierPrice      `json:"reference,omitempty"`
	ServiceTier       ServiceTierDecision    `json:"serviceTier"`
	Context           ContextPricing         `json:"context"`
	BaseSubtotal      decimal.Decimal        `json:"baseSubtotal"`
	BaseTierSubtotals []decimal.Decimal      `json:"baseTierSubtotals,omitempty"`
	ModeMultiplier    *decimal.Decimal       `json:"modeMultiplier,omitempty"`
	CostMultiplier    decimal.Decimal        `json:"costMultiplier"`
}
