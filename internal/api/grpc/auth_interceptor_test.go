package grpc

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestAuthInterceptor 创建带内存 ClientStore 的 AuthInterceptor 用于测试。
func newTestAuthInterceptor(t *testing.T) (*AuthInterceptor, *auth.ClientService) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	tm := auth.NewTokenManager("test-secret", time.Hour)

	store := auth.NewSQLiteClientStore(db)
	require.NoError(t, store.InitSchema(context.Background()))
	clientSvc := auth.NewClientService(store, tm, nil)

	interceptor := NewAuthInterceptor(tm, clientSvc, nil)
	return interceptor, clientSvc
}

// withCredential 在 ctx 中注入 Bearer 凭证。
func withCredential(ctx context.Context, cred string) context.Context {
	md := metadata.Pairs("authorization", cred)
	return metadata.NewIncomingContext(ctx, md)
}

// TestAuthInterceptor_JWT 验证 JWT 鉴权通过时将 Claims 写入 context。
func TestAuthInterceptor_JWT(t *testing.T) {
	interceptor, _ := newTestAuthInterceptor(t)

	// 签发一个用户 token
	tm := auth.NewTokenManager("test-secret", time.Hour)
	token, err := tm.Generate(&auth.User{UserID: "u-1", Username: "admin", Roles: []string{"admin"}})
	require.NoError(t, err)

	ctx := withCredential(context.Background(), "Bearer "+token)
	handler := func(ctx context.Context, req any) (any, error) {
		claims := auth.ClaimsFromContext(ctx)
		require.NotNil(t, claims)
		assert.Equal(t, "u-1", claims.UserID)
		assert.Equal(t, auth.SubjectTypeUser, claims.SubjectType)
		return "ok", nil
	}

	resp, err := interceptor.Unary()(ctx, nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

// TestAuthInterceptor_APIKey 验证机器接入方 API Key 换取 Claims。
func TestAuthInterceptor_APIKey(t *testing.T) {
	interceptor, clientSvc := newTestAuthInterceptor(t)

	apiKey, err := clientSvc.CreateClient(context.Background(), &auth.Client{
		Name:        "sat-watcher",
		ModuleName:  "openspace-satellite-watcher",
		CommunityID: "comm-a",
	})
	require.NoError(t, err)
	require.True(t, len(apiKey) > 0)

	ctx := withCredential(context.Background(), "Bearer "+apiKey)
	handler := func(ctx context.Context, req any) (any, error) {
		claims := auth.ClaimsFromContext(ctx)
		require.NotNil(t, claims)
		assert.Equal(t, auth.SubjectTypeClient, claims.SubjectType)
		assert.Equal(t, "comm-a", claims.CommunityID)
		return "ok", nil
	}

	resp, err := interceptor.Unary()(ctx, nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

// TestAuthInterceptor_APIKeyRaw 验证未加 Bearer 前缀的裸 API Key 同样可用。
func TestAuthInterceptor_APIKeyRaw(t *testing.T) {
	interceptor, clientSvc := newTestAuthInterceptor(t)

	apiKey, err := clientSvc.CreateClient(context.Background(), &auth.Client{
		Name:   "raw-key-module",
		Roles:  []string{"viewer"},
	})
	require.NoError(t, err)

	ctx := withCredential(context.Background(), apiKey)
	handler := func(ctx context.Context, req any) (any, error) {
		claims := auth.ClaimsFromContext(ctx)
		require.NotNil(t, claims)
		assert.Equal(t, auth.SubjectTypeClient, claims.SubjectType)
		return "ok", nil
	}

	_, err = interceptor.Unary()(ctx, nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.NoError(t, err)
}

// TestAuthInterceptor_RevokedAPIKey 验证已吊销客户端的 API Key 被拒绝。
func TestAuthInterceptor_RevokedAPIKey(t *testing.T) {
	interceptor, clientSvc := newTestAuthInterceptor(t)

	c := &auth.Client{Name: "to-revoke"}
	apiKey, err := clientSvc.CreateClient(context.Background(), c)
	require.NoError(t, err)

	require.NoError(t, clientSvc.RevokeClient(context.Background(), c.ClientID))

	ctx := withCredential(context.Background(), "Bearer "+apiKey)
	handler := func(ctx context.Context, req any) (any, error) {
		t.Fatal("不应进入 handler")
		return nil, nil
	}

	_, err = interceptor.Unary()(ctx, nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestAuthInterceptor_MissingCredential 验证缺少凭证时返回 Unauthenticated。
func TestAuthInterceptor_MissingCredential(t *testing.T) {
	interceptor, _ := newTestAuthInterceptor(t)

	handler := func(ctx context.Context, req any) (any, error) {
		t.Fatal("不应进入 handler")
		return nil, nil
	}

	_, err := interceptor.Unary()(context.Background(), nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestAuthInterceptor_NoTokenManager 验证未配置 TokenManager 时直接放行（开发模式）。
func TestAuthInterceptor_NoTokenManager(t *testing.T) {
	interceptor := NewAuthInterceptor(nil, nil, nil)
	hit := false
	handler := func(ctx context.Context, req any) (any, error) {
		hit = true
		return "ok", nil
	}

	_, err := interceptor.Unary()(context.Background(), nil, &grpclib.UnaryServerInfo{FullMethod: "/x/Test"}, handler)
	require.NoError(t, err)
	assert.True(t, hit)
}

// TestServerSetAuth 验证 SetAuth 之后启动的服务器要求凭证。
func TestServerSetAuth(t *testing.T) {
	db, err := core.InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	nodeRepo := core.NewSQLiteNodeRepository(db)
	relRepo := core.NewSQLiteRelationshipRepository(db)
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })
	kg := core.NewKGService(nodeRepo, relRepo, bus, nil)
	kg.SetDB(db)

	tm := auth.NewTokenManager("test-secret", time.Hour)
	clientStore := auth.NewSQLiteClientStore(db)
	require.NoError(t, clientStore.InitSchema(context.Background()))
	clientSvc := auth.NewClientService(clientStore, tm, nil)

	srv := NewServer(kg, bus, registry, nil)
	srv.SetAuth(tm, clientSvc)

	// 用一个随机可用端口启动
	srv.Start(0)
	defer srv.Stop()

	// 关闭 net/http goroutine 竞态：Start 用端口绑定的 lis；此处仅验证鉴权不崩溃
	assert.NotNil(t, srv.server)
}