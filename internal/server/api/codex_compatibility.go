package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
)

type CodexCompatibilityHandlers struct {
	System *biz.SystemService
}

type codexAccountCheckResponse struct {
	Accounts         []codexAccountCheckEntry `json:"accounts"`
	AccountOrdering  []string                 `json:"account_ordering"`
	DefaultAccountID string                   `json:"default_account_id"`
}

type codexAccountCheckEntry struct {
	ID                     string `json:"id"`
	PlanType               string `json:"plan_type"`
	WorkspaceBackendOrigin string `json:"workspace_backend_origin"`
	AccountRoutingOverride string `json:"account_routing_override"`
	Name                   string `json:"name"`
	Structure              string `json:"structure"`
}

func NewCodexCompatibilityHandlers(system *biz.SystemService) *CodexCompatibilityHandlers {
	return &CodexCompatibilityHandlers{System: system}
}

func (h *CodexCompatibilityHandlers) Enabled(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := authz.RunWithSystemBypass(c.Request.Context(), "codex-compatibility-switch", func(bypassCtx context.Context) (*biz.CodexCompatibilitySettings, error) {
		return h.System.CodexCompatibilitySettings(bypassCtx)
	})
	if err != nil {
		middleware.AbortWithError(c, 500, errors.New("failed to read Codex compatibility settings"))
		return
	}
	if !settings.Enabled {
		middleware.AbortWithError(c, 404, errors.New("Codex compatibility is disabled"))
		return
	}
	c.Next()
}

func (h *CodexCompatibilityHandlers) Whoami(c *gin.Context) {
	key, ok := contexts.GetAPIKey(c.Request.Context())
	if !ok || key == nil {
		middleware.AbortWithError(c, 401, errors.New("Invalid API key"))
		return
	}
	id := fmt.Sprintf("axonhub-key-%d", key.ID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"email": key.Name, "chatgpt_user_id": id, "chatgpt_account_id": id, "chatgpt_plan_type": "promax", "chatgpt_account_is_fedramp": false})
}

func (h *CodexCompatibilityHandlers) AccountsCheck(c *gin.Context) {
	key, ok := contexts.GetAPIKey(c.Request.Context())
	if !ok || key == nil {
		middleware.AbortWithError(c, http.StatusUnauthorized, errors.New("Invalid API key"))
		return
	}

	id := fmt.Sprintf("axonhub-key-%d", key.ID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, codexAccountCheckResponse{
		Accounts: []codexAccountCheckEntry{{
			ID:                     id,
			PlanType:               "promax",
			WorkspaceBackendOrigin: "NO_CONSTRAINT",
			AccountRoutingOverride: "NO_CONSTRAINT",
			Name:                   key.Name,
			Structure:              "personal",
		}},
		AccountOrdering:  []string{id},
		DefaultAccountID: id,
	})
}
