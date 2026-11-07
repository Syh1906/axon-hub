package api

import (
	"errors"
	"github.com/looplj/axonhub/internal/ent/privacy"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
)

var codexClientVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
