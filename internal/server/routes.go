package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/gql"
	"github.com/looplj/axonhub/internal/server/gql/openapi"
	"github.com/looplj/axonhub/internal/server/gql/selfusage"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/server/plugin"
	"github.com/looplj/axonhub/internal/server/static"
)

type Handlers struct {
	fx.In

	Graphql            *gql.GraphqlHandler
	OpenAPIGraphql     *openapi.GraphqlHandler
	OpenAI             *api.OpenAIHandlers
	Doubao             *api.DoubaoHandlers
	Anthropic          *api.AnthropicHandlers
	Gemini             *api.GeminiHandlers
	AiSDK              *api.AiSDKHandlers
	Playground         *api.PlaygroundHandlers
	System             *api.SystemHandlers
	Auth               *api.AuthHandlers
	Invitation         *api.InvitationHandlers
	Jina               *api.JinaHandlers
	TypeSafe           *api.TypeSafeHandlers
	Codex              *api.CodexHandlers
	CodexCompatibility *api.CodexCompatibilityHandlers
	XAI                *api.XAIHandlers
	ClaudeCode         *api.ClaudeCodeHandlers
	Antigravity        *api.AntigravityHandlers
	Copilot            *api.CopilotHandlers
	RequestContent     *api.RequestContentHandlers
	OIDC               *api.OIDCHandlers
	RequestPreview     *api.RequestPreviewHandlers
	SelfUsageGraphql   *selfusage.GraphqlHandler
	DatabaseTransfer   *api.DatabaseTransferHandlers
}

type Services struct {
	fx.In

	TraceService   *biz.TraceService
	ThreadService  *biz.ThreadService
	AuthService    *biz.AuthService
	SystemService  *biz.SystemService
	RequestService *biz.RequestService
	PluginManager  *plugin.Manager
}

func SetupRoutes(server *Server, handlers Handlers, client *ent.Client, services Services, ipAccessControl *middleware.IPAccessControlConfig) {
	// IP 访问控制 - 全局最优先，拦截所有请求包括静态文件
	server.Use(middleware.WithIPAccessControl(ipAccessControl))

	// Serve static frontend files
	server.NoRoute(static.Handler())

	server.Use(middleware.AccessLog())
	server.Use(middleware.WithEntClient(client))
	server.Use(middleware.WithLoggingTracing(server.Config.Trace))
	server.Use(middleware.WithMetrics())

	// Setup CORS middleware at server level if enabled
	if server.Config.CORS.Enabled {
		corsConfig := cors.DefaultConfig()
		corsConfig.AllowOrigins = server.Config.CORS.AllowedOrigins
		corsConfig.AllowMethods = server.Config.CORS.AllowedMethods
		corsConfig.AllowHeaders = server.Config.CORS.AllowedHeaders
		corsConfig.ExposeHeaders = server.Config.CORS.ExposedHeaders
		corsConfig.AllowCredentials = server.Config.CORS.AllowCredentials
		corsConfig.MaxAge = server.Config.CORS.MaxAge

		corsHandler := cors.New(corsConfig)
		server.Use(corsHandler)
		server.OPTIONS("*any", corsHandler)
	}

	publicGroup := server.Group("", middleware.WithTimeout(server.Config.RequestTimeout))
	{
		// Favicon API - DO NOT AUTH
		publicGroup.GET("/favicon", handlers.System.GetFavicon)
		// Health check endpoint - no authentication required
		publicGroup.GET("/health", handlers.System.Health)
		publicGroup.GET("/auth/invitations/:token", handlers.Invitation.Get)
		publicGroup.POST("/auth/invitations/:token/register", handlers.Invitation.Register)
	}

	unSecureAdminGroup := server.Group("/admin", middleware.WithTimeout(server.Config.RequestTimeout))
	{
		// System Status and Initialize - DO NOT AUTH
		unSecureAdminGroup.GET("/system/status", handlers.System.GetSystemStatus)
		unSecureAdminGroup.POST("/system/initialize", handlers.System.InitializeSystem)
		// User Login - DO NOT AUTH
		unSecureAdminGroup.POST("/auth/signin", handlers.Auth.SignIn)
		unSecureAdminGroup.POST("/auth/refresh", handlers.Auth.Refresh)
		// The UI token is the credential and only GET/HEAD resources are routed.
		// Other methods fall through to the default 404 handler; OPTIONS
		// preflight is handled by the global CORS middleware.
		registerPluginUIRoutes(unSecureAdminGroup, services.PluginManager, services.SystemService)
	}

	oauthGroup := server.Group("/oauth", middleware.WithTimeout(server.Config.RequestTimeout))
	{
		handlers.OIDC.RegisterRoutes(oauthGroup)
	}

	adminGroup := server.Group("/admin", middleware.WithJWTAuth(services.AuthService), middleware.WithProjectID())
	handlers.DatabaseTransfer.RegisterRoutes(server.Group("/admin/database-transfer", middleware.WithJWTAuth(services.AuthService)))
	server.GET("/admin/database-transfer/files/:id", handlers.DatabaseTransfer.Download)
	server.HEAD("/admin/database-transfer/files/:id", handlers.DatabaseTransfer.Download)
	// 管理员路由 - 使用 JWT 认证
	{
		adminGroup.GET("/playground", middleware.WithTimeout(server.Config.RequestTimeout), func(c *gin.Context) {
			handlers.Graphql.Playground.ServeHTTP(c.Writer, c.Request)
		})
		adminGroup.POST("/invitations", handlers.Invitation.Create)

		adminGroup.POST("/codex/oauth/start", handlers.Codex.StartOAuth)
		adminGroup.POST("/codex/public-catalog/preview", handlers.Codex.ImportCatalog)
		adminGroup.POST("/codex/public-catalog/apply", handlers.Codex.ImportCatalog)
		adminGroup.GET("/codex/public-catalog", handlers.Codex.PublicCatalog)
		adminGroup.PUT("/codex/public-catalog", handlers.Codex.PublicCatalog)
		adminGroup.POST("/codex/public-catalog/check", handlers.Codex.PublicCatalog)
		adminGroup.POST("/codex/oauth/exchange", handlers.Codex.Exchange)
		adminGroup.POST("/codex/auth/decode", handlers.Codex.DecodeAuthJSON)
		adminGroup.POST("/xai/oauth/start", handlers.XAI.StartOAuth)
		adminGroup.POST("/xai/oauth/exchange", handlers.XAI.Exchange)
		adminGroup.POST("/xai/oauth/sso", handlers.XAI.DecodeSSO)

		adminGroup.POST("/claudecode/oauth/start", handlers.ClaudeCode.StartOAuth)
		adminGroup.POST("/claudecode/oauth/exchange", handlers.ClaudeCode.Exchange)

		adminGroup.POST("/antigravity/oauth/start", handlers.Antigravity.StartOAuth)
		adminGroup.POST("/antigravity/oauth/exchange", handlers.Antigravity.Exchange)

		adminGroup.POST("/copilot/oauth/start", handlers.Copilot.StartOAuth)
		adminGroup.POST("/copilot/oauth/poll", handlers.Copilot.PollOAuth)

		// OIDC Manual Linking
		adminGroup.GET("/oidc/link/:provider", handlers.OIDC.GetLinkAuthorizeURL)

		// Playground API with channel specification support
		adminGroup.POST(
			"/playground/chat",
			middleware.WithTimeout(server.Config.LLMRequestTimeout),
			middleware.WithSource(request.SourcePlayground),
			middleware.WithResponseHeaders(services.RequestService),
			handlers.Playground.ChatCompletion,
		)

		adminGroup.GET(
			"/requests/:request_id/content",
			middleware.WithTimeout(server.Config.RequestTimeout),
			handlers.RequestContent.DownloadRequestContent,
		)
		adminGroup.GET(
			"/requests/:request_id/preview",
			middleware.WithTimeout(server.Config.RequestTimeout),
			handlers.RequestPreview.PreviewRequest,
		)
	}

	// Admin GraphQL accepts both the admin UI's JWT and service_account API keys.
	// Service account principals are read-only; see middleware.WithAdminGraphqlAuth
	// and gql.apiKeyReadOnly.
	adminGraphqlGroup := server.Group(
		"/admin",
		middleware.WithAdminGraphqlAuth(services.AuthService),
		middleware.WithProjectID(),
	)
	{
		adminGraphqlHandler := gql.WithRequestTimeout(handlers.Graphql.Graphql, server.Config.RequestTimeout, server.Config.ReadTimeout,
			max(server.Config.RequestTimeout, server.Config.LLMRequestTimeout, server.Config.ChannelTestTimeout), server.Config.ChannelTestTimeout)
		adminGraphqlGroup.POST("/graphql", func(c *gin.Context) {
			adminGraphqlHandler.ServeHTTP(c.Writer, c.Request)
		})
	}

	// Page routes accept a user JWT only. Service-account keys stay on
	// /admin/graphql. GraphQL keeps its 10s budget. Fetch uses the 15s
	// bridge budget. Neither route has a rate limiter.
	pluginGroup := server.Group(
		"/admin/plugins",
		middleware.WithJWTAuth(services.AuthService),
		middleware.WithProjectID(),
	)
	{
		pluginGroup.POST("/:pluginId/graphql", middleware.WithTimeout(gql.PluginGraphqlTimeout), func(c *gin.Context) {
			handlers.Graphql.PluginGraphql.ServeHTTP(c.Writer, c.Request)
		})
		pluginGroup.POST("/:pluginId/fetch", middleware.WithTimeout(gql.PluginFetchTimeout), func(c *gin.Context) {
			handlers.Graphql.PluginFetch.ServeHTTP(c.Writer, c.Request)
		})
	}

	openAPIGroup := server.Group(
		"/openapi",
		middleware.WithIPBlocklist(services.SystemService),
		middleware.WithOpenAPIAuth(services.AuthService),
		middleware.WithTimeout(server.Config.RequestTimeout),
	)
	{
		openAPIGroup.POST("/v1/graphql", func(c *gin.Context) {
			handlers.OpenAPIGraphql.Graphql.ServeHTTP(c.Writer, c.Request)
		})
		openAPIGroup.GET("/v1/playground", func(c *gin.Context) {
			handlers.OpenAPIGraphql.Playground.ServeHTTP(c.Writer, c.Request)
		})

		openAPIGroup.POST("/webhook/echo", handlers.System.WebhookEcho)
	}

	selfServiceGroup := server.Group(
		"/self-service",
		middleware.WithIPBlocklist(services.SystemService),
		middleware.WithSelfServiceAuthRateLimit(),
		middleware.WithSelfServiceAPIKeyAuth(services.AuthService),
		middleware.WithSelfServiceQueryRateLimit(),
		middleware.WithTimeout(server.Config.RequestTimeout),
	)
	selfServiceGroup.POST("/v1/graphql", func(c *gin.Context) {
		handlers.SelfUsageGraphql.Graphql.ServeHTTP(c.Writer, c.Request)
	})

	registerCodexRoutes(server, handlers, services)

	apiMiddlewares := []gin.HandlerFunc{
		middleware.WithIPBlocklist(services.SystemService),
		middleware.WithAPIKeyConfig(services.AuthService, nil),
		middleware.WithSource(request.SourceAPI),
	}
	apiGroup := server.Group("/", append([]gin.HandlerFunc{
		middleware.WithTimeout(server.Config.LLMRequestTimeout),
	}, apiMiddlewares...)...)

	// WebSocket mode owns a long-lived connection and applies its processing
	// timeout per response.create event, so it must not inherit the ordinary
	// single-request timeout from apiGroup.
	responsesWebSocketGroup := server.Group("/", apiMiddlewares...)
	inference := append([]gin.HandlerFunc{middleware.RequireInferenceScope}, requestTracking(server, services)...)
	responsesWebSocketGroup.Use(inference...)
	responsesWebSocketGroup.GET("/v1/responses", handlers.OpenAI.CreateResponseWebSocket(server.Config.LLMRequestTimeout))

	catalog := apiGroup.Group("", requestTracking(server, services)...)
	catalog.GET("/codex/models", handlers.OpenAI.ListCodexModels)
	catalog.GET("/v1/models", handlers.OpenAI.ListModels)
	catalog.GET("/v1/models/*model", handlers.OpenAI.RetrieveModel)
	catalog.GET("/anthropic/v1/models", handlers.Anthropic.ListModels)
	catalog.GET("/anthropic/v1/models/*model", handlers.Anthropic.RetrieveModel)
	inferenceGroup := apiGroup.Group("", inference...)
	{
		openaiGroup := inferenceGroup.Group("/v1")
		openaiGroup.POST("/chat/completions", handlers.OpenAI.ChatCompletion)
		openaiGroup.POST("/completions", handlers.OpenAI.Completion)
		openaiGroup.POST("/responses/compact", handlers.OpenAI.CompactResponse)
		openaiGroup.POST("/responses", handlers.OpenAI.CreateResponse)
		openaiGroup.POST("/embeddings", handlers.OpenAI.CreateEmbedding)
		openaiGroup.POST("/moderations", handlers.OpenAI.CreateModeration)
		openaiGroup.POST("/alpha/search", handlers.OpenAI.CreateAlphaSearch)
		openaiGroup.POST("/decisions", handlers.OpenAI.CreateDecisions)
		openaiGroup.POST("/images/generations", handlers.OpenAI.CreateImage)
		openaiGroup.POST("/images/edits", handlers.OpenAI.CreateImageEdit)
		openaiGroup.POST("/videos", handlers.OpenAI.CreateVideo)
		openaiGroup.GET("/videos/:id", handlers.OpenAI.GetVideo)
		openaiGroup.DELETE("/videos/:id", handlers.OpenAI.DeleteVideo)
		openaiGroup.POST("/audio/speech", handlers.OpenAI.CreateSpeech)
		openaiGroup.POST("/audio/transcriptions", handlers.OpenAI.CreateTranscription)
		openaiGroup.POST("/audio/translations", handlers.OpenAI.CreateTranslation)
		// DO NOT SUPPORT IMAGE VARIATION
		// openaiGroup.POST("/images/variations", handlers.OpenAI.CreateImageVariation)

		// OpenAI-compatible Anthropic endpoint
		openaiGroup.POST("/messages", handlers.Anthropic.CreateMessage)
		openaiGroup.POST("/messages/count_tokens", handlers.Anthropic.CountTokens)

		// Compatible with OpenAI API
		openaiGroup.POST("/rerank", handlers.Jina.Rerank)

		// Native System One endpoint
		openaiGroup.POST("/systemone", handlers.TypeSafe.SystemOne)
	}

	{
		jinaGroup := inferenceGroup.Group("/jina/v1")
		jinaGroup.POST("/embeddings", handlers.Jina.CreateEmbedding)
		jinaGroup.POST("/rerank", handlers.Jina.Rerank)
	}

	{
		typesafeGroup := inferenceGroup.Group("/typesafe/v1")
		typesafeGroup.POST("/systemone", handlers.TypeSafe.SystemOne)
	}

	{
		anthropicGroup := inferenceGroup.Group("/anthropic/v1")
		anthropicGroup.POST("/messages", handlers.Anthropic.CreateMessage)
		anthropicGroup.POST("/messages/count_tokens", handlers.Anthropic.CountTokens)
	}

	{
		doubaoGroup := inferenceGroup.Group("/doubao/v3")
		doubaoGroup.POST("/contents/generations/tasks", handlers.Doubao.CreateTask)
		doubaoGroup.GET("/contents/generations/tasks/:id", handlers.Doubao.GetTask)
		doubaoGroup.DELETE("/contents/generations/tasks/:id", handlers.Doubao.DeleteTask)
	}

	registerGeminiRoutes(server, handlers.Gemini, services)
}

func registerGeminiRoutes(server *Server, handlers *api.GeminiHandlers, services Services) {
	handlers = handlers.WithCacheConfig(server.Config.GeminiExplicitCache)
	for _, prefix := range []string{"/gemini/:gemini-api-version", "/v1beta"} {
		group := server.Group(prefix,
			middleware.WithTimeout(server.Config.LLMRequestTimeout),
			middleware.WithIPBlocklist(services.SystemService),
			middleware.WithGeminiKeyAuth(services.AuthService),
			middleware.WithSource(request.SourceAPI),
		)

		group.Group("", requestTracking(server, services)...).GET("/models", handlers.ListModels)
		// Require a model name so /models/ keeps Gin's redirect to the list.
		group.GET("/models/:model", handlers.RetrieveModel)
		group.GET("/models/:model/*suffix", handlers.RetrieveModel)
		group.Use(middleware.RequireInferenceScope)
		group.POST("/cachedContents", handlers.CacheResource)
		group.GET("/cachedContents", handlers.CacheResource)
		group.GET("/cachedContents/:id", handlers.CacheResource)
		group.PATCH("/cachedContents/:id", handlers.CacheResource)
		group.DELETE("/cachedContents/:id", handlers.CacheResource)
		group.Use(requestTracking(server, services)...)
		group.POST("/models/*action", handlers.GenerateContent)
	}
}

func requestTracking(server *Server, services Services) []gin.HandlerFunc {
	return []gin.HandlerFunc{
		middleware.WithResponseHeaders(services.RequestService),
		middleware.WithThread(server.Config.Trace, services.ThreadService),
		middleware.WithTrace(server.Config.Trace, services.TraceService),
	}
}

// registerPluginUIRoutes serves token-scoped plugin UI files without a user JWT.
// A nil manager does not panic here; the handler answers 503.
func registerPluginUIRoutes(group *gin.RouterGroup, manager *plugin.Manager, system *biz.SystemService) {
	handler := func(c *gin.Context) {
		request := plugin.PluginUIRequest{
			Manager: manager,
			Token:   c.Param("token"),
			Path:    c.Param("path"),
		}
		if system == nil {
			request.SecretErr = errors.New("system secret is unavailable")
		} else {
			secret, err := authz.RunWithSystemBypass(c.Request.Context(), "plugin-ui-secret", func(ctx context.Context) (string, error) {
				return system.SecretKey(ctx)
			})
			if err != nil {
				request.SecretErr = err
			} else {
				request.Secret = secret
			}
		}
		plugin.ServePluginUI(c.Writer, c.Request, request)
	}
	// Only GET/HEAD are routed: other methods must not be registered here,
	// because a root-level OPTIONS("*any") catch-all exists when CORS is
	// enabled and Gin panics when a second wildcard is added to that tree.
	group.Handle(http.MethodGet, "/plugin-ui/:token/*path", handler)
	group.Handle(http.MethodHead, "/plugin-ui/:token/*path", handler)
}
