package server

import (
	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/server/middleware"
)

func registerCodexRoutes(server *Server, handlers Handlers, services Services) {
	common := []gin.HandlerFunc{
		middleware.WithIPBlocklist(services.SystemService),
		handlers.CodexCompatibility.Enabled,
		middleware.WithStrictAPIKeyConfig(services.AuthService, nil),
		middleware.WithSource(request.SourceAPI),
	}
	group := server.Group("/codex", common...)
	timedTracking := append(requestTracking(server, services), middleware.WithTimeout(server.Config.LLMRequestTimeout))
	account := group.Group("", timedTracking...)
	account.GET("/v1/user-auth-credential/whoami", handlers.CodexCompatibility.Whoami)
	account.GET("/api/codex/accounts/check", handlers.CodexCompatibility.AccountsCheck)
	group.Use(middleware.RequireInferenceScope)
	httpGroup := group.Group("", timedTracking...)
	httpGroup.POST("/images/generations", handlers.OpenAI.CreateImage)
	httpGroup.POST("/images/edits", handlers.OpenAI.CreateImageEdit)
	httpGroup.POST("/responses", handlers.OpenAI.CreateResponse)
	websocketGroup := group.Group("", requestTracking(server, services)...)
	websocketGroup.GET("/responses", handlers.OpenAI.CreateResponseWebSocket(server.Config.LLMRequestTimeout))
}
