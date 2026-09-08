package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/openspace-os/openspace-os-core/internal/auth"
)

// NewRouter 创建 chi 路由器并挂载所有 REST API 路由。
//
// 路由前缀为 /api/v1，同时保留 /healthz 健康检查端点。
// 当 Handler 启用了认证（SetAuth 时 enabled=true）时，
// 会为需要认证的路由添加 AuthMiddleware 与权限校验中间件；
// 关闭认证时所有路由均可匿名访问（MVP 阶段便于测试与演示）。
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()

	// 中间件
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// 保留原有的 /healthz 端点
	r.Get("/healthz", h.Health)

	// Prometheus 格式的 metrics 暴露端点（无需认证，供 Prometheus 抓取）
	r.Get("/metrics", h.Metrics)

	// 判断是否启用认证
	authEnabled := h.authEnabled && h.tokenMgr != nil

	// 认证中间件实例（未启用认证时为透传中间件）
	// 支持：Bearer JWT（人/机）或 API Key（机器接入方）
	var authMW func(http.Handler) http.Handler
	if authEnabled {
		authMW = auth.APIAuthMiddleware(h.tokenMgr, h.clientSvc, h.log)
	} else {
		authMW = func(next http.Handler) http.Handler { return next }
	}

	// permMW 返回一个中间件：启用认证时校验权限，否则透传
	permMW := func(perm string) func(http.Handler) http.Handler {
		if !authEnabled {
			return func(next http.Handler) http.Handler { return next }
		}
		return func(next http.Handler) http.Handler {
			return authMW(auth.RequirePermission(perm, h.log)(next))
		}
	}

	// /api/v1 路由组
	r.Route("/api/v1", func(r chi.Router) {
		// 健康检查（与 /healthz 相同），无需认证
		r.Get("/health", h.Health)

		// Swagger UI 与 OpenAPI 规范文件，无需认证
		r.Get("/docs", h.ServeSwaggerUI)
		r.Get("/openapi.yaml", h.ServeOpenAPISpec)

		// 事件 Schema 列表，无需认证
		r.Get("/schemas", h.ListSchemas)

		// 认证相关端点
		r.Route("/auth", func(r chi.Router) {
			// login 无需认证
			r.Post("/login", h.Login)
			// token：API Key 换取 JWT，无需认证
			r.Post("/token", h.ExchangeToken)
			// refresh 与 me 需要认证（但不需要特定权限）
			r.With(authMW).Post("/refresh", h.RefreshToken)
			r.With(authMW).Get("/me", h.GetMe)
		})

		// 机器接入方（Client）管理端点（需 client.manage 权限）
		r.Route("/clients", func(r chi.Router) {
			r.With(permMW("user.manage")).Get("/", h.ListClients)
			r.With(permMW("user.manage")).Post("/", h.CreateClient)
			r.With(permMW("user.manage")).Get("/{clientId}", h.GetClient)
			r.With(permMW("user.manage")).Delete("/{clientId}", h.RevokeClient)
		})

		// 用户管理端点（需要 user.manage 权限，即 admin）
		r.Route("/users", func(r chi.Router) {
			r.With(permMW("user.manage")).Get("/", h.ListUsers)
			r.With(permMW("user.manage")).Post("/", h.CreateUser)
			r.With(permMW("user.manage")).Get("/{userId}", h.GetUser)
			r.With(permMW("user.manage")).Delete("/{userId}", h.DeleteUser)
		})

		// 用量统计查询端点（需要 user.manage 权限，即 admin，billing 预留）
		r.With(permMW("user.manage")).Get("/billing/usage", h.ListUsage)

		// Node CRUD（需要认证 + 对应权限）
		r.With(permMW("node.create")).Post("/nodes", h.CreateNode)
		r.With(permMW("node.read")).Get("/nodes", h.ListNodes)
		r.With(permMW("node.read")).Get("/nodes/{nodeId}", h.GetNode)
		r.With(permMW("node.update")).Put("/nodes/{nodeId}", h.UpdateNode)
		r.With(permMW("node.delete")).Delete("/nodes/{nodeId}", h.DeleteNode)

		// Relationship
		r.With(permMW("relationship.create")).Post("/nodes/{nodeId}/relationships", h.CreateRelationship)
		r.With(permMW("relationship.delete")).Delete("/relationships/{relId}", h.DeleteRelationship)

		// 关系图查询
		r.With(permMW("graph.query")).Get("/nodes/{nodeId}/graph", h.TraverseGraph)

		// 事件订阅与回放
		r.With(permMW("event.subscribe")).Get("/events/subscribe", h.SubscribeEvents)
		r.With(permMW("event.replay")).Post("/events/replay", h.ReplayEvents)

		// 插件管理（需要 plugin.manage 权限，即 admin）
		r.With(permMW("plugin.manage")).Get("/plugins", h.ListPlugins)
		r.With(permMW("plugin.manage")).Post("/plugins/{name}/unload", h.UnloadPlugin)

		// 遥测流水线管理
		r.With(permMW("plugin.manage")).Get("/telemetry/parsers", h.ListTelemetryParsers)
		r.With(permMW("plugin.manage")).Post("/telemetry/parser", h.SetTelemetryParser)
		r.With(permMW("plugin.manage")).Post("/telemetry/send", h.SendTelemetry)

		// 遥测上报端点（带鉴权：任何已认证人/机主体可上报，不要求特定权限）
		r.With(authMW).Post("/telemetry/ingest", h.IngestTelemetry)

		// 指令流水线管理
		r.With(permMW("command.send")).Post("/commands", h.SendCommand)
		r.With(permMW("command.send")).Get("/commands", h.ListCommands)
		r.With(permMW("command.send")).Get("/commands/{commandId}", h.GetCommandStatus)
		r.With(permMW("command.send")).Delete("/commands/{commandId}", h.CancelCommand)
		r.With(permMW("plugin.manage")).Get("/command-adapters", h.ListCommandAdapters)
		r.With(permMW("plugin.manage")).Post("/command-adapter", h.SetCommandAdapter)
	})

	return r
}
