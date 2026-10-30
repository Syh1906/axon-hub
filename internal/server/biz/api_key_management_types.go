package biz

import (
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
)

type KeyBudgetPatch struct {
	Rules     []*BudgetRuleInput `json:"rules"`
	Timezone  *string            `json:"timezone"`
	SetExpiry bool               `json:"setExpiry"`
	ExpiresAt *time.Time         `json:"expiresAt"`
	Revision  *int               `json:"revision"`
}
type ManagedKeyInput struct {
	Name       string          `json:"name"`
	Type       apikey.Type     `json:"type"`
	Scopes     []string        `json:"scopes"`
	AllowedIPs []string        `json:"allowedIPs"`
	TemplateID *int            `json:"templateID"`
	Budget     *KeyBudgetPatch `json:"budget"`
}
type KeyManagementRow struct {
	RowID         string             `json:"rowID"`
	Action        string             `json:"action"`
	TargetID      *int               `json:"targetID"`
	GroupRef      *int               `json:"groupRef"`
	Name          *string            `json:"name"`
	Note          *string            `json:"note"`
	Enabled       *bool              `json:"enabled"`
	Revision      *int               `json:"revision"`
	UpdatedAt     *time.Time         `json:"updatedAt"`
	Budget        *KeyBudgetPatch    `json:"budget"`
	Keys          []*ManagedKeyInput `json:"keys"`
	TemplateID    *int               `json:"templateID"`
	AllowedIPs    []string           `json:"allowedIPs"`
	SetAllowedIPs bool               `json:"setAllowedIPs"`
}
type KeyManagementResult struct {
	RowID   string `json:"rowID"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
	GroupID int    `json:"groupID"`
	KeyIDs  []int  `json:"keyIDs"`
}
type ManagedKeyGroup struct {
	ID        int         `json:"id"`
	Name      string      `json:"name"`
	Note      string      `json:"note"`
	Enabled   bool        `json:"enabled"`
	Revision  int         `json:"revision"`
	KeyCount  int         `json:"keyCount"`
	CreatedAt time.Time   `json:"createdAt"`
	Budget    *BudgetView `json:"budget"`
}
type ManagedKeyGroups struct {
	Items          []*ManagedKeyGroup `json:"items"`
	Total          int                `json:"total"`
	UngroupedCount int                `json:"ungroupedCount"`
}
type ManagedKeyPage struct {
	Items []*ent.APIKey `json:"items"`
	Total int           `json:"total"`
}
