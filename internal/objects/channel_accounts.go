package objects

import "time"

type ChannelKeySettings struct {
	KeyRef     string `json:"keyRef"`
	Remark     string `json:"remark"`
	AccountRef string `json:"accountRef"`
	Priority   int    `json:"priority"`
}

// ChannelKeySettingsInput keeps the priority optional for existing API clients.
type ChannelKeySettingsInput struct {
	KeyRef     string `json:"keyRef"`
	Remark     string `json:"remark"`
	AccountRef string `json:"accountRef"`
	Priority   *int   `json:"priority,omitempty"`
}

type UpstreamAccountCredentials struct {
	AutoCreated  bool             `json:"autoCreated,omitempty"`
	Ref          string           `json:"ref"`
	Name         string           `json:"name"`
	Provider     UpstreamProvider `json:"provider"`
	AccountToken string           `json:"accountToken,omitempty"`
	AccountID    int              `json:"accountId,omitempty"`
	VerifiedAt   *time.Time       `json:"verifiedAt,omitempty"`
}

func (c *ChannelUpstreamCredentials) Account(ref string) *UpstreamAccountCredentials {
	if c != nil {
		for i := range c.Accounts {
			if c.Accounts[i].Ref == ref {
				return &c.Accounts[i]
			}
		}
	}
	return nil
}

func (s *ChannelSettings) KeySettingsFor(ref string) ChannelKeySettings {
	if s != nil {
		for _, item := range s.KeySettings {
			if item.KeyRef == ref {
				return item
			}
		}
	}
	return ChannelKeySettings{KeyRef: ref}
}

// UpgradeAccountCredentials 将旧账号转换为独立记录，不猜测 Key 归属。
// 备份没有查询快照时保留待关联状态，正常调用不依赖这份管理认证。
func UpgradeAccountCredentials(c *ChannelUpstreamCredentials) *ChannelUpstreamCredentials {
	if c == nil {
		return nil
	}
	result := *c
	result.Accounts = append([]UpstreamAccountCredentials{}, c.Accounts...)
	if c.AccountToken != "" && len(result.Accounts) == 0 {
		result.Accounts = append(result.Accounts, UpstreamAccountCredentials{Ref: "legacy-account", Name: "账号 1", Provider: UpstreamProviderNewAPI, AccountToken: c.AccountToken, AccountID: c.AccountID})
	}
	result.AccountToken, result.AccountID = "", 0
	return &result
}

type ChannelUpstreamAccountState struct {
	BalanceErrorCode string            `json:"balanceErrorCode"`
	AutoCreated      bool              `json:"autoCreated"`
	Balances         []UpstreamBalance `json:"balances"`
	Ref              string            `json:"ref"`
	Name             string            `json:"name"`
	Provider         UpstreamProvider  `json:"provider"`
	AccountID        *int              `json:"accountId,omitempty"`
	HasToken         bool              `json:"hasToken"`
	VerifiedAt       *time.Time        `json:"verifiedAt,omitempty"`
	KeyCount         int               `json:"keyCount"`
	Balance          *UpstreamBalance  `json:"balance,omitempty"`
	UpdatedAt        *time.Time        `json:"updatedAt,omitempty"`
	Complete         bool              `json:"complete"`
}

type ChannelBalanceSummary struct {
	Balances           []UpstreamBalance `json:"balances"`
	AccountCount       int               `json:"accountCount"`
	KnownAccountCount  int               `json:"knownAccountCount"`
	UnassignedKeyCount int               `json:"unassignedKeyCount"`
	Complete           bool              `json:"complete"`
}

type SaveChannelUpstreamAccountInput struct {
	Ref          *string          `json:"ref,omitempty"`
	Name         string           `json:"name"`
	Provider     UpstreamProvider `json:"provider"`
	AccountToken *string          `json:"accountToken,omitempty"`
	AccountID    *int             `json:"accountId,omitempty"`
}

type ChannelUpstreamAddress struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

type ChannelAddressProbe struct {
	Reachable  bool   `json:"reachable"`
	LatencyMs  int    `json:"latencyMs"`
	StatusCode int    `json:"statusCode"`
	ErrorCode  string `json:"errorCode"`
}
