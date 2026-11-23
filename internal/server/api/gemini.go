package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/contexts"
	entprivacy "github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/internal/server/plugin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/gemini"
)

type GeminiHandlersParams struct {
	fx.In

	ChannelService              *biz.ChannelService
	ModelService                *biz.ModelService
	DefaultSelector             *orchestrator.DefaultSelector
	RequestService              *biz.RequestService
	SystemService               *biz.SystemService
	UsageLogService             *biz.UsageLogService
	PromptService               *biz.PromptService
	PromptProtectionRuleService *biz.PromptProtectionRuleService
	QuotaService                *biz.QuotaService
	HttpClient                  *httpclient.HttpClient
	LiveStreamRegistry          *biz.LiveStreamRegistry
	ChannelLimiterManager       *orchestrator.ChannelLimiterManager
	ProviderQuotaStatusProvider orchestrator.ProviderQuotaStatusProvider
	PluginManager               *plugin.Manager
}

type GeminiHandlers struct {
	cacheConfig            GeminiCacheConfig
	bodyBudget             *httpclient.BodyBudget
	selector               *orchestrator.DefaultSelector
	client                 *httpclient.HttpClient
	ChannelService         *biz.ChannelService
	ModelService           *biz.ModelService
	ChatCompletionHandlers *ChatCompletionHandlers
}

func NewGeminiHandlers(params GeminiHandlersParams) *GeminiHandlers {
	var bodyBudget *httpclient.BodyBudget
	if params.RequestService != nil && params.RequestService.PayloadService != nil {
		bodyBudget = params.RequestService.PayloadService.BodyBudget
	}
	return &GeminiHandlers{
		bodyBudget: bodyBudget,
		selector:   params.DefaultSelector, client: params.HttpClient,
		ChatCompletionHandlers: NewChatCompletionHandlers(
			orchestrator.NewChatCompletionOrchestrator(
				params.ChannelService,
				params.DefaultSelector,
				params.RequestService,
				params.HttpClient,
				gemini.NewInboundTransformer(),
				params.SystemService,
				params.UsageLogService,
				params.PromptService,
				params.QuotaService,
				params.PromptProtectionRuleService,
				params.LiveStreamRegistry,
				params.ChannelLimiterManager,
				params.ProviderQuotaStatusProvider,
				orchestratorPlugins(params.PluginManager)),
		),
		ChannelService: params.ChannelService,
		ModelService:   params.ModelService,
	}
}

func (handlers *GeminiHandlers) GenerateContent(c *gin.Context) {
	c.Request = c.Request.WithContext(contexts.WithStreamFormat(c.Request.Context(), llm.APIFormatGeminiContents))
	raw, err := httpclient.ReadHTTPRequest(c.Request)
	if err != nil {
		httpErr := handlers.ChatCompletionHandlers.ChatCompletionOrchestrator.Inbound.TransformError(c.Request.Context(), err)
		c.Data(httpErr.StatusCode, "application/json", httpErr.Body)
		return
	}
	if !handlers.bindCacheReference(c, raw.Body) {
		return
	}
	alt := c.Query("alt")
	switch alt {
	case "sse":
		handlers.ChatCompletionHandlers.WithStreamWriter(WriteSSEStream).ChatCompletionWithRequest(c, raw)
	default:
		handlers.ChatCompletionHandlers.WithStreamWriter(WriteGeminiStream).ChatCompletionWithRequest(c, raw)
	}
}

func WriteGeminiStream(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent]) {
	ctx := c.Request.Context()
	clientDisconnected := false

	defer func() {
		if clientDisconnected {
			log.Warn(ctx, "Client disconnected")
		}
	}()

	c.Header("Content-Type", "application/json; charset=UTF-8")

	_, _ = c.Writer.Write([]byte("["))

	first := true
	terminalSeen := false
	terminalTracker := orchestrator.NewStreamTerminalTracker(streamExpectedChoices(stream))

	for {
		select {
		case <-ctx.Done():
			clientDisconnected = true

			log.Warn(ctx, "Client disconnected, stop streaming")

			return
		default:
			if stream.Next() {
				cur := stream.Current()

				if err := writeStreamDelivery(ctx, stream, func() (bool, error) {
					if !first {
						if n, err := io.WriteString(c.Writer, ","); err != nil {
							return false, err
						} else if n != 1 {
							return false, io.ErrShortWrite
						}
					}
					if n, err := c.Writer.Write(cur.Data); err != nil {
						return false, err
					} else if n != len(cur.Data) {
						return false, io.ErrShortWrite
					}
					if err := flushResponseWriter(c.Writer); err != nil {
						return false, err
					}
					contexts.GetRequestClock(ctx).WroteStreamContent(llm.APIFormatGeminiContents, cur)
					first = false
					terminal := terminalTracker.Observe(cur)
					if terminal {
						terminalSeen = true
					}
					return false, nil
				}); err != nil {
					clientDisconnected = true
					log.Warn(ctx, "Failed to write Gemini stream event", log.Cause(err))
					return
				}

				log.Debug(ctx, "write stream event", log.Any("event", cur))
			} else {
				if err := stream.Err(); err != nil {
					log.Error(ctx, "Error in stream", log.Cause(err))
				}

				if err := writeStreamDelivery(ctx, stream, func() (bool, error) {
					if n, err := io.WriteString(c.Writer, "]"); err != nil {
						return false, err
					} else if n != 1 {
						return false, io.ErrShortWrite
					}
					if err := flushResponseWriter(c.Writer); err != nil {
						return false, err
					}
					return terminalSeen, nil
				}); err != nil {
					clientDisconnected = true
					log.Warn(ctx, "Failed to close Gemini stream JSON array", log.Cause(err))
				}

				return
			}
		}
	}
}

// GeminiModel represents a model in the list models response.
type GeminiModel struct {
	Name                       string   `json:"name"`
	BaseModelID                string   `json:"baseModelId"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

// ListModels returns all available Gemini models.
// This endpoint is compatible with Gemini's /v1/models API.
// Only enabled registered models and aliases resolving to them are exposed.
func (handlers *GeminiHandlers) ListModels(c *gin.Context) {
	models, ok := handlers.visibleModels(c)
	if !ok {
		return
	}

	geminiModels := make([]GeminiModel, 0, len(models))
	for i, model := range models {
		geminiModels = append(geminiModels, geminiModel(model, i))
	}
	c.JSON(http.StatusOK, gin.H{"models": geminiModels})
}

func (handlers *GeminiHandlers) RetrieveModel(c *gin.Context) {
	models, ok := handlers.visibleModels(c)
	if !ok {
		return
	}
	id := c.Param("model") + c.Param("suffix")
	for i, model := range models {
		if model.ID == id {
			c.JSON(http.StatusOK, geminiModel(model, i))
			return
		}
	}
	c.JSON(http.StatusNotFound, gemini.GeminiError{Error: gemini.ErrorDetail{
		Code: http.StatusNotFound, Status: "NOT_FOUND", Message: "Model not found",
	}})
}

func (handlers *GeminiHandlers) visibleModels(c *gin.Context) ([]biz.ModelFacade, bool) {
	models, err := handlers.ModelService.ListEnabledModels(c.Request.Context())
	if err != nil {
		status := http.StatusInternalServerError
		errorStatus := "internal_server_error"
		if errors.Is(err, entprivacy.Deny) {
			status = http.StatusForbidden
			errorStatus = "permission_error"
		}
		_ = c.Error(err)
		c.JSON(status, gemini.GeminiError{
			Error: gemini.ErrorDetail{
				Message: err.Error(),
				Code:    status,
				Status:  errorStatus,
			},
		})

		return nil, false
	}
	return models, true
}

func geminiModel(model biz.ModelFacade, index int) GeminiModel {
	return GeminiModel{
		Name:                       "models/" + model.ID,
		BaseModelID:                model.ID,
		Version:                    fmt.Sprintf("%s-%d", model.ID, index),
		DisplayName:                model.DisplayName,
		Description:                model.DisplayName,
		SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
	}
}
