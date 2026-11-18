package api

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/model"
	entprivacy "github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

type AnthropicModel struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	DisplayName    string         `json:"display_name"`
	Description    string         `json:"description,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Created        time.Time      `json:"created"` // Registration time retained for existing callers.
	Lifecycle      string         `json:"lifecycle"`
	MaxInputTokens *int           `json:"max_input_tokens"`
	MaxTokens      *int           `json:"max_tokens"`
	Capabilities   map[string]any `json:"capabilities"`
}

func modelReleaseDate(m biz.ModelFacade) time.Time {
	if card := m.ConfiguredModel.ModelCard; card != nil && card.ReleaseDate != "" {
		for _, layout := range []string{time.DateOnly, time.RFC3339} {
			if date, err := time.Parse(layout, card.ReleaseDate); err == nil {
				return date.UTC()
			}
		}
	}
	return time.Unix(0, 0).UTC()
}

func anthropicModel(ctx context.Context, m biz.ModelFacade) AnthropicModel {
	target := m.ConfiguredModel
	result := AnthropicModel{
		ID: m.ID, Type: "model", Lifecycle: "active", DisplayName: target.Name,
		Created: m.CreatedAt, CreatedAt: modelReleaseDate(m),
	}
	if result.DisplayName == "" {
		result.DisplayName = m.DisplayName
	}
	if target.Remark != nil {
		result.Description = *target.Remark
	}
	if card := target.ModelCard; card != nil && target.Type == model.TypeChat {
		routes := modelDiscoveryRoutes(ctx, m, llm.APIFormatAnthropicMessage)
		if len(routes.channels) > 0 {
			result.MaxInputTokens = card.Limit.Input
			if card.Limit.Output > 0 {
				result.MaxTokens = lo.ToPtr(card.Limit.Output)
			}
			result.Capabilities = anthropicModelCapabilities(card, routes)
		}
	}
	return result
}

func writeAnthropicModelError(c *gin.Context, status int, kind, message string) {
	requestID, _ := contexts.GetRequestID(c.Request.Context())
	c.JSON(status, anthropic.AnthropicError{
		Type: "error", RequestID: requestID, StatusCode: status,
		Error: anthropic.ErrorDetail{Type: kind, Message: message},
	})
}

func (handlers *AnthropicHandlers) visibleModels(c *gin.Context) ([]biz.ModelFacade, bool) {
	models, err := handlers.ModelService.ListEnabledModels(c.Request.Context())
	if err == nil {
		return models, true
	}
	_ = c.Error(err)
	if errors.Is(err, entprivacy.Deny) {
		writeAnthropicModelError(c, http.StatusForbidden, "permission_error", "Insufficient permissions")
	} else {
		writeAnthropicModelError(c, http.StatusInternalServerError, "api_error", "Failed to list models")
	}
	return nil, false
}

func (handlers *AnthropicHandlers) RetrieveModel(c *gin.Context) {
	models, ok := handlers.visibleModels(c)
	if !ok {
		return
	}
	id := strings.TrimPrefix(c.Param("model"), "/")
	for _, model := range models {
		if model.ID == id {
			c.JSON(http.StatusOK, anthropicModel(c.Request.Context(), model))
			return
		}
	}
	writeAnthropicModelError(c, http.StatusNotFound, "not_found_error", "Model not found")
}

func (handlers *AnthropicHandlers) ListModels(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	after, before := c.Query("after_id"), c.Query("before_id")
	if err != nil || limit < 1 || limit > 1000 || (after != "" && before != "") {
		writeAnthropicModelError(c, http.StatusBadRequest, "invalid_request_error", "Use a limit from 1 to 1000 and at most one cursor")
		return
	}
	visible, ok := handlers.visibleModels(c)
	if !ok {
		return
	}
	// Resolve routes and native capabilities only for rows in the returned page.
	dates := make(map[string]time.Time, len(visible))
	for _, m := range visible {
		dates[m.ID] = modelReleaseDate(m)
	}
	slices.SortFunc(visible, func(a, b biz.ModelFacade) int {
		if order := dates[b.ID].Compare(dates[a.ID]); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if cursor := cmp.Or(after, before); cursor != "" {
		index := slices.IndexFunc(visible, func(m biz.ModelFacade) bool { return m.ID == cursor })
		if index < 0 {
			writeAnthropicModelError(c, http.StatusBadRequest, "invalid_request_error", "Invalid model cursor")
			return
		}
		if before != "" {
			visible = visible[:index]
		} else {
			visible = visible[index+1:]
		}
	}
	hasMore := len(visible) > limit
	if hasMore {
		if before != "" {
			visible = visible[len(visible)-limit:]
		} else {
			visible = visible[:limit]
		}
	}
	models := make([]AnthropicModel, 0, len(visible))
	for _, m := range visible {
		models = append(models, anthropicModel(c.Request.Context(), m))
	}
	var firstID, lastID *string
	if len(models) > 0 {
		firstID, lastID = &models[0].ID, &models[len(models)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": models, "has_more": hasMore, "first_id": firstID, "last_id": lastID})
}
