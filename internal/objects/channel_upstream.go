package objects

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

type ChannelCostMode string

const (
	ChannelCostModeManual   ChannelCostMode = "MANUAL"
	ChannelCostModeUpstream ChannelCostMode = "UPSTREAM"
)

func (m ChannelCostMode) IsValid() bool {
	return m == ChannelCostModeManual || m == ChannelCostModeUpstream
}

func (m ChannelCostMode) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(m)))
}

func (m *ChannelCostMode) UnmarshalGQL(value any) error {
	s, ok := value.(string)
	if !ok || !ChannelCostMode(s).IsValid() {
		return fmt.Errorf("invalid channel cost mode")
	}
	*m = ChannelCostMode(s)
	return nil
}

type ChannelUpstreamSettings struct {
	Enabled                bool            `json:"enabled"`
	RefreshIntervalMinutes int             `json:"refreshIntervalMinutes"`
	CostMode               ChannelCostMode `json:"costMode"`
}

func (s *ChannelSettings) UpstreamSettings() ChannelUpstreamSettings {
	settings := ChannelUpstreamSettings{RefreshIntervalMinutes: 10, CostMode: ChannelCostModeManual}
	if s != nil && s.Upstream != nil {
		settings = *s.Upstream
		if settings.RefreshIntervalMinutes == 0 {
			settings.RefreshIntervalMinutes = 10
		}
		if settings.CostMode == "" || !settings.Enabled {
			settings.CostMode = ChannelCostModeManual
		}
	}
	return settings
}

// ChannelUpstreamCredentials 将账户令牌单独保存在敏感字段中，查询 DTO 只返回连接状态和账号 ID。
type ChannelUpstreamCredentials struct {
	AccountToken string `json:"accountToken"`
	AccountID    int    `json:"accountId"`
}

type UpstreamProvider string

const (
	UpstreamProviderUnknown UpstreamProvider = "UNKNOWN"
	UpstreamProviderNewAPI  UpstreamProvider = "NEW_API"
	UpstreamProviderSub2API UpstreamProvider = "SUB2_API"
)

func (p UpstreamProvider) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(p)))
}

type UpstreamQueryState string

const (
	UpstreamQueryNever        UpstreamQueryState = "NEVER"
	UpstreamQuerySuccess      UpstreamQueryState = "SUCCESS"
	UpstreamQueryError        UpstreamQueryState = "ERROR"
	UpstreamQueryUnsupported  UpstreamQueryState = "UNSUPPORTED"
	UpstreamQueryAuthRequired UpstreamQueryState = "AUTH_REQUIRED"
	UpstreamQueryUnmatched    UpstreamQueryState = "UNMATCHED"
)

func (s UpstreamQueryState) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(s)))
}

type UpstreamBalanceKind string

const (
	UpstreamBalanceAccount      UpstreamBalanceKind = "ACCOUNT"
	UpstreamBalanceKey          UpstreamBalanceKind = "KEY"
	UpstreamBalanceSubscription UpstreamBalanceKind = "SUBSCRIPTION"
)

func (k UpstreamBalanceKind) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(k)))
}

type UpstreamBalance struct {
	Kind       UpstreamBalanceKind `json:"kind"`
	Amount     *decimal.Decimal    `json:"amount,omitempty"`
	Used       *decimal.Decimal    `json:"used,omitempty"`
	Limit      *decimal.Decimal    `json:"limit,omitempty"`
	Unlimited  bool                `json:"unlimited"`
	Currency   string              `json:"currency"`
	Unit       string              `json:"unit"`
	AccountRef string              `json:"accountRef,omitempty"`
	ExpiresAt  *time.Time          `json:"expiresAt,omitempty"`
}

type UpstreamAccountSnapshot struct {
	AccountID int             `json:"accountId"`
	Balance   UpstreamBalance `json:"balance"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type UpstreamRateSnapshot struct {
	Multiplier decimal.Decimal `json:"multiplier"`
	Group      string          `json:"group,omitempty"`
	ObservedAt time.Time       `json:"observedAt"`
	SyncedAt   time.Time       `json:"syncedAt"`
	// 跨越上游明确返回的时段边界后重新查询，不推测未返回的分组规则。
	ValidUntil *time.Time `json:"validUntil,omitempty"`
}

type UpstreamKeySnapshot struct {
	Provider           UpstreamProvider         `json:"provider"`
	BalanceState       UpstreamQueryState       `json:"balanceState"`
	RateState          UpstreamQueryState       `json:"rateState"`
	Balances           []UpstreamBalance        `json:"balances"`
	Account            *UpstreamAccountSnapshot `json:"account,omitempty"`
	Rate               *UpstreamRateSnapshot    `json:"rate,omitempty"`
	BalanceUpdatedAt   *time.Time               `json:"balanceUpdatedAt,omitempty"`
	BalanceCheckedAt   *time.Time               `json:"balanceCheckedAt,omitempty"`
	RateCheckedAt      *time.Time               `json:"rateCheckedAt,omitempty"`
	NextBalanceCheckAt *time.Time               `json:"nextBalanceCheckAt,omitempty"`
	NextRateCheckAt    *time.Time               `json:"nextRateCheckAt,omitempty"`
	BalanceErrorCode   string                   `json:"balanceErrorCode,omitempty"`
	RateErrorCode      string                   `json:"rateErrorCode,omitempty"`
	UpstreamTokenID    int                      `json:"upstreamTokenId,omitempty"`
	UpstreamAccountID  int                      `json:"upstreamAccountId,omitempty"`
}

type CostMultiplierSource string

const (
	CostMultiplierManual    CostMultiplierSource = "MANUAL"
	CostMultiplierUpstream  CostMultiplierSource = "UPSTREAM"
	CostMultiplierCached    CostMultiplierSource = "CACHED"
	CostMultiplierEstimated CostMultiplierSource = "ESTIMATED"
)

func (s CostMultiplierSource) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(s)))
}

type AppliedCostMultiplier struct {
	Value      decimal.Decimal      `json:"value"`
	Source     CostMultiplierSource `json:"source"`
	Provider   UpstreamProvider     `json:"provider"`
	ObservedAt *time.Time           `json:"observedAt,omitempty"`
}

// RequestCostSnapshot 在本次请求内复用 Price；持久记录通过不可变的 PriceReferenceID 定位原价格。
type RequestCostSnapshot struct {
	PriceReferenceID string                `json:"priceReferenceId"`
	PricedAt         time.Time             `json:"pricedAt"`
	KeyRef           string                `json:"keyRef,omitempty"`
	KeyLabel         string                `json:"keyLabel,omitempty"`
	Applied          AppliedCostMultiplier `json:"applied"`
	ManualMultiplier decimal.Decimal       `json:"manualMultiplier"`
	Price            *ModelPrice           `json:"-"`
}

// DisplayMultiplier 返回实际费用项目共同使用的倍率；混合倍率不能反推统一原价。
func (s *RequestCostSnapshot) DisplayMultiplier(items []CostItem) *decimal.Decimal {
	var multiplier *decimal.Decimal
	for _, item := range items {
		if item.Multiplier == nil || (item.Quantity == 0 && item.Subtotal.IsZero()) {
			continue
		}
		if multiplier == nil {
			multiplier = item.Multiplier
		} else if !multiplier.Equal(*item.Multiplier) {
			return nil
		}
	}
	if multiplier == nil {
		return &s.Applied.Value
	}
	return multiplier
}

type ChannelUpstreamKeyState struct {
	KeyRef             string                `json:"keyRef"`
	Label              string                `json:"label"`
	Disabled           bool                  `json:"disabled"`
	Snapshot           *UpstreamKeySnapshot  `json:"snapshot"`
	UpstreamMultiplier *decimal.Decimal      `json:"upstreamMultiplier,omitempty"`
	Applied            AppliedCostMultiplier `json:"applied"`
}

type ChannelUpstreamState struct {
	ChannelID        GUID                       `json:"channelId"`
	Settings         ChannelUpstreamSettings    `json:"settings"`
	ManualMultiplier decimal.Decimal            `json:"manualMultiplier"`
	Provider         UpstreamProvider           `json:"provider"`
	AccountConnected bool                       `json:"accountConnected"`
	AccountID        *int                       `json:"accountId,omitempty"`
	Keys             []*ChannelUpstreamKeyState `json:"keys"`
}

type UpdateChannelUpstreamInput struct {
	Enabled                bool            `json:"enabled"`
	RefreshIntervalMinutes int             `json:"refreshIntervalMinutes"`
	CostMode               ChannelCostMode `json:"costMode"`
	ManualMultiplier       decimal.Decimal `json:"manualMultiplier"`
	AccountToken           *string         `json:"accountToken,omitempty"`
	AccountID              *int            `json:"accountId,omitempty"`
	ClearAccount           bool            `json:"clearAccount"`
}

type UpstreamAccountVerification struct {
	Success           bool   `json:"success"`
	AccountID         *int   `json:"accountId,omitempty"`
	RequiresAccountID bool   `json:"requiresAccountId"`
	ErrorCode         string `json:"errorCode"`
}

type RefreshChannelUpstreamsResult struct {
	Channels  []*ChannelUpstreamState `json:"channels"`
	Refreshed int                     `json:"refreshed"`
	Skipped   int                     `json:"skipped"`
	Failed    int                     `json:"failed"`
}
