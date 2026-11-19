package api

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/internal/server/plugin"
	"github.com/looplj/axonhub/internal/server/safeguards"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

type AnthropicHandlersParams struct {
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
	SSEKeepAliveConfig          SSEKeepAliveConfig
	SafeguardsConfig            safeguards.Config `optional:"true"`
}

type AnthropicHandlers struct {
	ChannelService         *biz.ChannelService
	ModelService           *biz.ModelService
	SystemService          *biz.SystemService
	ChatCompletionHandlers *ChatCompletionHandlers
	TokenCountHandlers     *ChatCompletionHandlers
}

func NewAnthropicHandlers(params AnthropicHandlersParams) *AnthropicHandlers {
	handlers := &AnthropicHandlers{
		ChatCompletionHandlers: &ChatCompletionHandlers{
			ChatCompletionOrchestrator: orchestrator.NewChatCompletionOrchestrator(
				params.ChannelService,
				params.DefaultSelector,
				params.RequestService,
				params.HttpClient,
				anthropic.NewInboundTransformer(),
				params.SystemService,
				params.UsageLogService,
				params.PromptService,
				params.QuotaService,
				params.PromptProtectionRuleService,
				params.LiveStreamRegistry,
				params.ChannelLimiterManager,
				params.ProviderQuotaStatusProvider,
				orchestratorPlugins(params.PluginManager)),
			sseKeepAlive:             params.SSEKeepAliveConfig,
			sseHeartbeatFormat:       sseHeartbeatAnthropic,
			safeguardsConfig:         params.SafeguardsConfig,
			safeguardsConfigProvider: params.SystemService.ClaudeCodeSafeguardsConfig,
			inputUsageCache:          newInputUsageCache(),
		},
		ChannelService: params.ChannelService,
		ModelService:   params.ModelService,
		SystemService:  params.SystemService,
	}
	handlers.ChatCompletionHandlers.reviewer = newSafeguardsClassifier(handlers.ChatCompletionHandlers.ChatCompletionOrchestrator)
	countOrchestrator := *handlers.ChatCompletionHandlers.ChatCompletionOrchestrator
	countOrchestrator.Inbound = anthropic.NewTokenCountInboundTransformer()
	handlers.TokenCountHandlers = &ChatCompletionHandlers{ChatCompletionOrchestrator: &countOrchestrator}
	return handlers
}

func (handlers *AnthropicHandlers) CreateMessage(c *gin.Context) {
	c.Request = c.Request.WithContext(contexts.WithStreamFormat(c.Request.Context(), llm.APIFormatAnthropicMessage))
	handlers.ChatCompletionHandlers.ChatCompletion(c)
}

func (handlers *AnthropicHandlers) CountTokens(c *gin.Context) {
	handlers.TokenCountHandlers.ChatCompletion(c)
}
