package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
)

var codexClientVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func (h *CodexHandlers) PublicCatalog(c *gin.Context) {
	ctx := c.Request.Context()
	scope := scopes.ScopeReadSettings
	if c.Request.Method != http.MethodGet {
		scope = scopes.ScopeWriteSettings
	}
	if err := authz.RequireScope(ctx, scope); err != nil {
		JSONError(c, http.StatusForbidden, err)
		return
	}
	var err error
	switch c.Request.Method {
	case http.MethodPut:
		var schedule biz.PublicCatalogSchedule
		if err := c.ShouldBindJSON(&schedule); err != nil {
			JSONError(c, http.StatusBadRequest, err)
			return
		}
		err = h.snapshots.SetPublicSchedule(ctx, schedule)
	case http.MethodPost:
		err = h.snapshots.CheckPublicCatalog(ctx)
	}
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, biz.ErrPublicCatalogDisabled):
			code = http.StatusConflict
		case errors.Is(err, biz.ErrPublicCatalogInterval):
			code = http.StatusBadRequest
		case c.Request.Method == http.MethodPost:
			code = http.StatusBadGateway
		}
		JSONError(c, code, err)
		return
	}
	status, err := h.snapshots.PublicStatus(ctx)
	if err != nil {
		JSONError(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, status)
}

func (h *OpenAIHandlers) ListCodexModels(c *gin.Context) {
	version := c.Query("client_version")
	if !codexClientVersion.MatchString(version) || len(version) > 32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_client_version", "message": "client_version must use major.minor.patch"}})
		return
	}
	result, omitted, err := h.CodexCatalogService.Models(c.Request.Context(), version)
	c.Header("X-AxonHub-Catalog-Omitted", strconv.Itoa(omitted))
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, privacy.Deny) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": gin.H{"code": "codex_catalog_unavailable", "message": err.Error()}})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "application/json", result)
}
