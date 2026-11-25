package objects

import (
	"errors"

	"github.com/shopspring/decimal"
)

type KeyChannelRate struct {
	ChannelID  int
	Multiplier decimal.Decimal
}
type KeyChannelRates struct {
	Rules, Inherited                []*KeyChannelRate
	GlobalRate, InheritedGlobalRate *decimal.Decimal
	Revision                        string
}
type KeyGlobalRateInput struct {
	Multiplier *decimal.Decimal
}

func ValidateGlobalRate(rate *decimal.Decimal) error {
	if rate != nil && rate.IsNegative() {
		return errors.New("global rate must be nonnegative")
	}
	return nil
}

type KeyRateChannel struct {
	ChannelID      int
	Disabled       bool
	Name           string
	Tags, Profiles []string
}
type KeyRateChannelScope struct {
	ActiveProfile string
	Profiles      []string
	Channels      []*KeyRateChannel
}

type GroupCostRow struct {
	KeyID                                    int
	KeyName                                  string
	ChannelID                                *int
	ChannelName                              *string
	Requests                                 int
	ChannelCost, BilledCost                  *float64
	MissingChannel, MissingBilled, Estimated int
}
type GroupCostComparison struct {
	Total          *GroupCostRow
	Keys, Channels []*GroupCostRow
}

// Billing freezes the caller identity even when no budget account is active.
type ExecutionBilling struct {
	GroupID   *int             `json:"groupId,omitempty"`
	GroupName string           `json:"groupName,omitempty"`
	Rate      *decimal.Decimal `json:"rate,omitempty"`
	Source    string           `json:"source,omitempty"`
}

// BilledMultiplier returns the caller rate frozen for this execution.
func (s *RequestCostSnapshot) BilledMultiplier() decimal.Decimal {
	if s.Billing != nil && s.Billing.Rate != nil {
		return *s.Billing.Rate
	}
	return s.Applied.Value
}

// BilledPrice yields a private copy for caller-facing pricing. The channel price stays immutable.
func (s *RequestCostSnapshot) BilledPrice() *RequestCostSnapshot {
	if s == nil || s.Billing == nil || s.Billing.Rate == nil {
		return s
	}
	cp := *s
	cp.Applied.Value = *s.Billing.Rate
	cp.ManualMultiplier = *s.Billing.Rate
	return &cp
}

type GroupCostEntry struct {
	UsageID, RequestID      int
	BilledCost, ChannelCost *float64
	GroupBillingEvidence
	Archived bool
}

// GroupBillingEvidence contains only the accounting facts needed after log retention expires.
type GroupBillingEvidence struct {
	Multiplier *decimal.Decimal    `json:"multiplier,omitempty"`
	Source     string              `json:"source,omitempty"`
	GroupName  string              `json:"groupName,omitempty"`
	Mixed      bool                `json:"mixed,omitempty"`
	Items      []*GroupBillingItem `json:"items,omitempty"`
}
type GroupBillingItem struct {
	Code          string           `json:"code"`
	Variant       string           `json:"variant,omitempty"`
	ChannelRate   *decimal.Decimal `json:"channelRate,omitempty"`
	ChannelSource string           `json:"channelSource,omitempty"`
	ChannelAmount *decimal.Decimal `json:"channelAmount,omitempty"`
	BilledRate    *decimal.Decimal `json:"billedRate,omitempty"`
	BilledSource  string           `json:"billedSource,omitempty"`
	BilledAmount  *decimal.Decimal `json:"billedAmount,omitempty"`
}
