package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// failingBus 是一个 Ping 恒失败的 MessageBus，用于验证健康检查的 degraded 分支（T6.3）。
type failingBus struct{}

func (b *failingBus) Publish(context.Context, *event.Event) error                  { return nil }
func (b *failingBus) Subscribe(core.SubscribeOptions) (<-chan *event.Event, func()) { return nil, func() {} }
func (b *failingBus) Replay(context.Context, core.ReplayOptions) ([]*event.Event, error) {
	return nil, nil
}
func (b *failingBus) Close() error            { return nil }
func (b *failingBus) Ping(context.Context) error { return errors.New("bus down") }

// newRawHandler 构造一个 Handler（不经过完整 server 组装），便于直接注入依赖。
func newRawHandler(t *testing.T, bus core.MessageBus) (*Handler, *httptest.Server) {
	t.Helper()
	registry := event.NewSchemaRegistry()
	h := NewHandler(nil, bus, registry, nil)
	router := NewRouter(h)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return h, server
}

// TestHealthWithDeps 验证健康检查在注入数据库与总线后返回连通性信息（T6.3）。
func TestHealthWithDeps(t *testing.T) {
	db, err := core.InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })

	h := NewHandler(nil, bus, registry, nil)
	h.SetDB(db)
	router := NewRouter(h)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	resp := doRequest(t, server, "GET", "/healthz", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	decodeJSON(t, resp, &body)
	assert.Equal(t, "ok", body["status"])

	deps, ok := body["deps"].(map[string]any)
	require.True(t, ok, "healthz 应返回 deps 字段")
	assert.Equal(t, "up", deps["database"].(map[string]any)["status"])
	assert.Equal(t, "up", deps["bus"].(map[string]any)["status"])
}

// TestHealthDegraded 验证总线不可达时健康检查返回 503 且 status=degraded（T6.3）。
func TestHealthDegraded(t *testing.T) {
	h, server := newRawHandler(t, &failingBus{})

	// 注入一个已关闭（不可 PING）的数据库连接
	db, err := core.InitDB(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Close()) // 关闭后 Ping 会失败
	h.SetDB(db)

	resp := doRequest(t, server, "GET", "/api/v1/health", nil)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	var body map[string]any
	decodeJSON(t, resp, &body)
	assert.Equal(t, "degraded", body["status"])

	deps := body["deps"].(map[string]any)
	assert.Equal(t, "down", deps["database"].(map[string]any)["status"])
	assert.Equal(t, "down", deps["bus"].(map[string]any)["status"])
}