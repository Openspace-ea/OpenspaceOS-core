package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/openspace-os/openspace-os-core/internal/core"
)

// claimsContextKey 是 context 中存储 Claims 的键类型。
type claimsContextKey struct{}

// WithClaims 将 Claims 存入 context。
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// ClaimsFromContext 从 context 中提取 Claims，不存在则返回 nil。
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

// AuthMiddleware 从 Authorization header 提取 JWT 并验证，
// 验证通过后将 Claims 存入 request context。
//
// header 格式：Authorization: Bearer <token>
// 缺失或无效 token 返回 401。
func AuthMiddleware(tm *TokenManager, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				writeAuthError(w, http.StatusUnauthorized, "缺少认证 token")
				return
			}
			claims, err := tm.Parse(tokenStr)
			if err != nil {
				logger.Warn("token 解析失败", "error", err)
				writeAuthError(w, http.StatusUnauthorized, "token 无效或已过期")
				return
			}
			ctx := WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// APIAuthMiddleware 同时支持两种机器/用户接入方式：
//
//  1. Authorization: Bearer <JWT> —— 解析并验证人(SubjectTypeUser)或机(SubjectTypeClient) token；
//  2. Authorization: Bearer <apiKey> 或 X-API-Key: <apiKey> —— 直接携带机器接入方 API Key，
//     通过 ClientService.ExchangeAPIKey 换取并填入 Claims。
//
// clientSvc 为 nil 时退化为仅支持 JWT（等同 AuthMiddleware）。
func APIAuthMiddleware(tm *TokenManager, clientSvc *ClientService, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1) 尝试从 Authorization Bearer 解析 JWT
			if tokenStr := extractBearerToken(r); tokenStr != "" {
				if claims, err := tm.Parse(tokenStr); err == nil {
					next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
					return
				}
			}
			// 2) 尝试 API Key（Bearer 或 X-API-Key）
			if clientSvc != nil {
				apiKey := extractBearerToken(r)
				if apiKey == "" {
					apiKey = strings.TrimSpace(r.Header.Get("X-API-Key"))
				}
				if apiKey != "" && strings.HasPrefix(apiKey, apiKeyPrefix) {
					token, client, err := clientSvc.ExchangeAPIKey(r.Context(), apiKey)
					if err != nil {
						logger.Warn("API Key 校验失败", "error", err)
						writeAuthError(w, http.StatusUnauthorized, "API Key 无效或已吊销")
						return
					}
					claims, perr := tm.Parse(token)
					if perr != nil {
						logger.Warn("API Key token 解析失败", "error", perr)
						writeAuthError(w, http.StatusUnauthorized, "认证失败")
						return
					}
					_ = client
					next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
					return
				}
			}
			logger.Warn("认证失败：缺少有效的 Bearer token 或 API Key")
			writeAuthError(w, http.StatusUnauthorized, "缺少认证 token 或 API Key")
		})
	}
}

// RequirePermission 返回一个中间件，检查当前用户是否拥有指定权限。
//
// 需配合 AuthMiddleware 使用。未认证（context 无 Claims）返回 401，
// 权限不足返回 403。
func RequirePermission(permission string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				writeAuthError(w, http.StatusUnauthorized, "未认证")
				return
			}
			if !HasPermission(claims.Roles, permission) {
				logger.Warn("权限不足",
					"userId", claims.UserID,
					"username", claims.Username,
					"roles", claims.Roles,
					"requiredPermission", permission,
				)
				writeAuthError(w, http.StatusForbidden, "权限不足")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireNodeAccess 检查用户是否有权访问 URL 中的 nodeId 对应的 Node。
//
// 规则：
//   - admin 角色可访问所有 Node
//   - 其他角色只能访问与自己 CommunityID 相同的 Node
//
// 需配合 AuthMiddleware 使用。未认证返回 401；Node 不存在返回 404；
// 跨社区访问返回 403。
func RequireNodeAccess(kg *core.KGService, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				writeAuthError(w, http.StatusUnauthorized, "未认证")
				return
			}

			// admin 角色直接放行
			if containsString(claims.Roles, RoleAdmin) {
				next.ServeHTTP(w, r)
				return
			}

			nodeID := chi.URLParam(r, "nodeId")
			if nodeID == "" {
				// 路由中没有 nodeId 参数，跳过 Node 级校验
				next.ServeHTTP(w, r)
				return
			}

			node, err := kg.GetNode(r.Context(), nodeID)
			if err != nil {
				if errors.Is(err, core.ErrNotFound) {
					writeAuthError(w, http.StatusNotFound, "节点不存在")
					return
				}
				logger.Error("查询节点失败", "nodeId", nodeID, "error", err)
				writeAuthError(w, http.StatusInternalServerError, "查询节点失败")
				return
			}

			if claims.CommunityID == "" || claims.CommunityID != node.OwnerCommunityID {
				logger.Warn("跨社区访问被拒绝",
					"userId", claims.UserID,
					"userCommunity", claims.CommunityID,
					"nodeId", nodeID,
					"nodeCommunity", node.OwnerCommunityID,
				)
				writeAuthError(w, http.StatusForbidden, "无权访问该节点的社区")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractBearerToken 从 Authorization header 提取 Bearer token。
//
// 格式：Authorization: Bearer <token>
// 缺失或格式不正确时返回空字符串。
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
}

// authErrorResponse 是认证/授权错误的响应体。
type authErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeAuthError 写入认证/授权错误响应。
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authErrorResponse{
		Error:   http.StatusText(status),
		Message: msg,
	})
}

// containsString 判断切片中是否包含指定字符串。
func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
