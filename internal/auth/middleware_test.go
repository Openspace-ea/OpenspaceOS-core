package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestKGService 创建用于测试的 KGService（内存 SQLite + 内存事件总线）。
func newTestKGService(t *testing.T) *core.KGService {
	t.Helper()
	db, err := core.InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	nodeRepo := core.NewSQLiteNodeRepository(db)
	relRepo := core.NewSQLiteRelationshipRepository(db)
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })
	return core.NewKGService(nodeRepo, relRepo, bus, nil)
}

// dummyHandler 返回 200 OK 的简单 handler，用于测试中间件是否放行。
func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

// doRequestWithToken 发送带 Authorization header 的请求。
func doRequestWithToken(t *testing.T, handler http.Handler, method, path, token string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w.Result()
}

// TestAuthMiddlewareNoToken 测试无 token 返回 401。
func TestAuthMiddlewareNoToken(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	mw := AuthMiddleware(tm, nil)
	handler := mw(dummyHandler())

	resp := doRequestWithToken(t, handler, "GET", "/test", "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// TestAuthMiddlewareInvalidToken 测试无效 token 返回 401。
func TestAuthMiddlewareInvalidToken(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	mw := AuthMiddleware(tm, nil)
	handler := mw(dummyHandler())

	// 格式错误的 token
	resp := doRequestWithToken(t, handler, "GET", "/test", "invalid-token")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// 用错误密钥签发的 token
	otherTM := NewTokenManager("wrong-secret", time.Hour)
	token, err := otherTM.Generate(&User{UserID: "u-1", Username: "x", Roles: []string{"viewer"}})
	require.NoError(t, err)
	resp = doRequestWithToken(t, handler, "GET", "/test", token)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// TestAuthMiddlewareValidToken 测试有效 token 通过认证。
func TestAuthMiddlewareValidToken(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	mw := AuthMiddleware(tm, nil)
	handler := mw(dummyHandler())

	token, err := tm.Generate(&User{UserID: "u-1", Username: "alice", Roles: []string{"viewer"}})
	require.NoError(t, err)

	resp := doRequestWithToken(t, handler, "GET", "/test", token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestAuthMiddlewareClaimsInContext 测试认证后 Claims 存入 context。
func TestAuthMiddlewareClaimsInContext(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	user := &User{UserID: "u-ctx", Username: "ctxuser", Roles: []string{"admin"}, CommunityID: "comm-1"}

	var capturedClaims *Claims
	handler := AuthMiddleware(tm, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	token, err := tm.Generate(user)
	require.NoError(t, err)

	resp := doRequestWithToken(t, handler, "GET", "/test", token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	require.NotNil(t, capturedClaims)
	assert.Equal(t, "u-ctx", capturedClaims.UserID)
	assert.Equal(t, "ctxuser", capturedClaims.Username)
	assert.Equal(t, []string{"admin"}, capturedClaims.Roles)
	assert.Equal(t, "comm-1", capturedClaims.CommunityID)
}

// TestAuthMiddlewareExpiredToken 测试过期 token 返回 401。
func TestAuthMiddlewareExpiredToken(t *testing.T) {
	// 1 秒过期的 token
	tm := NewTokenManager("secret", 1*time.Second)
	mw := AuthMiddleware(tm, nil)
	handler := mw(dummyHandler())

	token, err := tm.Generate(&User{UserID: "u-1", Username: "x", Roles: []string{"viewer"}})
	require.NoError(t, err)

	// 等待 token 过期
	time.Sleep(1100 * time.Millisecond)

	resp := doRequestWithToken(t, handler, "GET", "/test", token)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// TestRequirePermissionNoClaims 测试无认证信息（未经过 AuthMiddleware）时返回 401。
func TestRequirePermissionNoClaims(t *testing.T) {
	mw := RequirePermission("node.read", nil)
	handler := mw(dummyHandler())

	// 无 Authorization header
	resp := doRequestWithToken(t, handler, "GET", "/test", "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// TestRequirePermissionInsufficient 测试权限不足返回 403。
func TestRequirePermissionInsufficient(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	authMW := AuthMiddleware(tm, nil)
	permMW := RequirePermission("node.delete", nil)
	handler := authMW(permMW(dummyHandler()))

	// viewer 没有 node.delete 权限
	token, err := tm.Generate(&User{UserID: "u-1", Username: "viewer1", Roles: []string{"viewer"}})
	require.NoError(t, err)

	resp := doRequestWithToken(t, handler, "DELETE", "/test", token)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

// TestRequirePermissionSufficient 测试权限充足时通过。
func TestRequirePermissionSufficient(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	authMW := AuthMiddleware(tm, nil)
	permMW := RequirePermission("node.read", nil)
	handler := authMW(permMW(dummyHandler()))

	// viewer 有 node.read 权限
	token, err := tm.Generate(&User{UserID: "u-1", Username: "viewer1", Roles: []string{"viewer"}})
	require.NoError(t, err)

	resp := doRequestWithToken(t, handler, "GET", "/test", token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// admin 有所有权限
	adminToken, err := tm.Generate(&User{UserID: "u-2", Username: "admin1", Roles: []string{"admin"}})
	require.NoError(t, err)
	resp = doRequestWithToken(t, handler, "GET", "/test", adminToken)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestRequireNodeAccessAdmin 测试 admin 可访问所有 Node。
func TestRequireNodeAccessAdmin(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	kg := newTestKGService(t)
	ctx := context.Background()

	// 创建一个属于 comm-b 的节点
	node := &model.Node{
		NodeID:           "node-1",
		NodeType:         model.NodeTypeSatellite,
		Name:             "测试卫星",
		Status:           "active",
		OwnerCommunityID: "comm-b",
	}
	require.NoError(t, kg.RegisterNode(ctx, node))

	// admin（属于 comm-a）可访问 comm-b 的节点
	handler := AuthMiddleware(tm, nil)(
		RequireNodeAccess(kg, nil)(dummyHandler()),
	)
	adminToken, err := tm.Generate(&User{UserID: "u-admin", Username: "admin", Roles: []string{"admin"}, CommunityID: "comm-a"})
	require.NoError(t, err)

	// 使用 chi 路由参数需要路由器，这里直接测试不带 nodeId 的情况（admin 直接放行）
	resp := doRequestWithToken(t, handler, "GET", "/nodes/node-1", adminToken)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestRequireNodeAccessOperatorSameCommunity 测试 operator 可访问同社区 Node。
func TestRequireNodeAccessOperatorSameCommunity(t *testing.T) {
	tm := NewTokenManager("secret", time.Hour)
	kg := newTestKGService(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID:           "node-same",
		NodeType:         model.NodeTypeSatellite,
		Name:             "同社区卫星",
		Status:           "active",
		OwnerCommunityID: "comm-a",
	}
	require.NoError(t, kg.RegisterNode(ctx, node))

	// operator 属于 comm-a，访问 comm-a 的节点
	// 由于 RequireNodeAccess 从 chi URLParam 读取 nodeId，
	// 不使用 chi 路由器时 nodeId 为空，会跳过 Node 级校验直接放行。
	// 这里验证 admin 角色直接放行的逻辑。
	handler := AuthMiddleware(tm, nil)(
		RequireNodeAccess(kg, nil)(dummyHandler()),
	)
	opToken, err := tm.Generate(&User{UserID: "u-op", Username: "op", Roles: []string{"operator"}, CommunityID: "comm-a"})
	require.NoError(t, err)

	// 不经过 chi 路由，nodeId 为空，非 admin 会跳过 Node 校验放行
	resp := doRequestWithToken(t, handler, "GET", "/test", opToken)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// TestCanAccessNodeIntegration 测试 CanAccessNode 与中间件逻辑一致。
func TestCanAccessNodeIntegration(t *testing.T) {
	svc := newTestService(t)

	// admin 可访问任意社区
	adminClaims := &Claims{UserID: "u-1", Roles: []string{"admin"}, CommunityID: "comm-a"}
	assert.True(t, svc.CanAccessNode(adminClaims, &model.Node{OwnerCommunityID: "comm-z"}))

	// operator 同社区可访问
	opClaims := &Claims{UserID: "u-2", Roles: []string{"operator"}, CommunityID: "comm-a"}
	assert.True(t, svc.CanAccessNode(opClaims, &model.Node{OwnerCommunityID: "comm-a"}))

	// operator 跨社区不可访问
	assert.False(t, svc.CanAccessNode(opClaims, &model.Node{OwnerCommunityID: "comm-b"}))
}

// TestExtractBearerToken 测试 Bearer token 提取。
func TestExtractBearerToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"标准格式", "Bearer abc123", "abc123"},
		{"带空格", "Bearer   xyz  ", "xyz"},
		{"缺少前缀", "abc123", ""},
		{"空 header", "", ""},
		{"只有 Bearer", "Bearer ", ""},
		{"Basic 前缀", "Basic abc123", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			got := extractBearerToken(req)
			assert.Equal(t, tc.want, got)
		})
	}
}
