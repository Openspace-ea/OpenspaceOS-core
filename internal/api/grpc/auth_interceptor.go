package grpc

import (
	"context"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/peer"

	"github.com/openspace-os/openspace-os-core/internal/auth"
)

// authorizationKey 是 gRPC metadata 中承载凭证的键（小写）。
const authorizationKey = "authorization"

// bearerPrefix 是 Bearer 前缀。
const bearerPrefix = "Bearer "

// AuthInterceptor 封装一元与流式两种服务端鉴权拦截器。
//
// 与 HTTP 侧 APIAuthMiddleware 对齐：同时支持
//  1. Authorization: Bearer <JWT>（人 SubjectTypeUser / 机 SubjectTypeClient）
//  2. Authorization: Bearer <apiKey> 或 <apiKey>（机器接入方 API Key，前缀 aos_）
//
// 鉴权通过后，将 Claims 写入请求 context，供后续 handler / 其它 interceptor 使用。
type AuthInterceptor struct {
	tm        *auth.TokenManager
	clientSvc *auth.ClientService
	logger    *slog.Logger
}

// NewAuthInterceptor 创建 AuthInterceptor。
//
// tm 不能为 nil；clientSvc 为 nil 时仅支持 JWT（API Key 自动禁用）。
func NewAuthInterceptor(tm *auth.TokenManager, clientSvc *auth.ClientService, logger *slog.Logger) *AuthInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthInterceptor{tm: tm, clientSvc: clientSvc, logger: logger}
}

// Unary 返回一元 RPC 服务端拦截器。
func (a *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		newCtx, err := a.authenticate(ctx)
		if err != nil {
			return nil, err
		}
		return handler(newCtx, req)
	}
}

// Stream 返回流式 RPC 服务端拦截器。
//
// 事件订阅（SubscribeEvents）为服务端流，同样需要鉴权。
func (a *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		newCtx, err := a.authenticate(ss.Context())
		if err != nil {
			return err
		}
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: newCtx})
	}
}

// authenticate 从请求 context 的 metadata 中提取凭证并完成鉴权，
// 返回携带 Claims 的新 context。
func (a *AuthInterceptor) authenticate(ctx context.Context) (context.Context, error) {
	cred := extractCredential(ctx)
	if a.tm == nil {
		// 未配置 TokenManager，视为未启用鉴权，直接放行
		return ctx, nil
	}

	// 1) 尝试解析 JWT
	if cred != "" {
		if claims, err := a.tm.Parse(cred); err == nil {
			return auth.WithClaims(ctx, claims), nil
		}
	}

	// 2) 尝试机器接入方 API Key
	if a.clientSvc != nil && strings.HasPrefix(cred, "aos_") {
		token, _, err := a.clientSvc.ExchangeAPIKey(ctx, cred)
		if err != nil {
			a.logger.Warn("gRPC API Key 校验失败", "error", err, "peer", peerFrom(ctx))
			return nil, status.Error(codes.Unauthenticated, "API Key 无效或已吊销")
		}
		claims, err := a.tm.Parse(token)
		if err != nil {
			a.logger.Warn("gRPC API Key token 解析失败", "error", err)
			return nil, status.Error(codes.Unauthenticated, "认证失败")
		}
		return auth.WithClaims(ctx, claims), nil
	}

	a.logger.Warn("gRPC 认证失败：缺少有效的 JWT 或 API Key", "peer", peerFrom(ctx))
	return nil, status.Error(codes.Unauthenticated, "缺少认证 token 或 API Key")
}

// extractCredential 从 gRPC metadata 中提取 Bearer 凭证或裸 API Key。
func extractCredential(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(authorizationKey)
	if len(vals) == 0 {
		return ""
	}
	v := strings.TrimSpace(vals[0])
	v = strings.TrimPrefix(v, bearerPrefix)
	return strings.TrimSpace(v)
}

// peerFrom 返回对端地址，用于日志审计。
func peerFrom(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

// wrappedStream 覆盖 grpc.ServerStream 的 Context，以返回带 Claims 的 context。
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}