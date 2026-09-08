package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// ingestTestServer 创建带遥测上报流水线的 HTTP 服务器（T5.7 HTTP 入口）。
//
// 注入真实 Pipeline，使 /api/v1/telemetry/ingest 可用；返回 server、bus 与清理函数。
func ingestTestServer(t *testing.T) (*httptest.Server, core.MessageBus) {
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

	kg := core.NewKGService(nodeRepo, relRepo, bus, nil)

	broker := plugin.NewBroker(bus, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	p := telemetry.NewPipeline(mgr, bus, nil)
	t.Cleanup(func() { _ = p.Stop() })

	handler := NewHandler(kg, bus, registry, nil)
	handler.SetTelemetryPipeline(p)
	router := NewRouter(handler)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, bus
}

// TestIngestTelemetry_HTTP 验证 HTTP 批量上报端点能把事件发布给订阅方（T5.7 HTTP 入口）。
//
// 单次请求携带多帧（批量数组），订阅方应收到全部事件的 TelemetryReceived 事件。
func TestIngestTelemetry_HTTP(t *testing.T) {
	server, bus := ingestTestServer(t)

	frames := []map[string]any{
		{"satelliteId": "http-1", "parameters": map[string]any{"voltage": 3.3}, "quality": "good"},
		{"satelliteId": "http-2", "parameters": map[string]any{"voltage": 3.2}, "quality": "good"},
		{"satelliteId": "http-3", "parameters": map[string]any{"voltage": 3.4}, "quality": "good"},
	}

	// 先订阅再上报，确保事件可被捕获
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	resp := doRequest(t, server, "POST", "/api/v1/telemetry/ingest", map[string]any{"frames": frames})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var body map[string]any
	decodeJSON(t, resp, &body)
	assert.Equal(t, "accepted", body["status"])
	assert.Equal(t, float64(3), body["accepted"])
	assert.Equal(t, float64(3), body["total"])

	// 订阅方收到全部 3 个事件
	got := make(map[string]bool)
	deadline := time.After(3 * time.Second)
	for len(got) < 3 {
		select {
		case e := <-ch:
			assert.Equal(t, event.EventTelemetryReceived, e.EventType)
			got[e.SourceNodeID] = true
		case <-deadline:
			t.Fatalf("HTTP 入口事件丢失: 收到 %d/3", len(got))
		}
	}
	assert.True(t, got["http-1"])
	assert.True(t, got["http-2"])
	assert.True(t, got["http-3"])
}

// TestIngestTelemetry_HTTPErrors 验证 HTTP 上报端点的参数校验（T5.7）：
// 空 frames 返回 400，非法 JSON 返回 400。
func TestIngestTelemetry_HTTPErrors(t *testing.T) {
	server, _ := ingestTestServer(t)

	// 空 frames
	resp := doRequest(t, server, "POST", "/api/v1/telemetry/ingest", map[string]any{"frames": []any{}})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 非法 JSON
	resp = doRequestRaw(t, server, "POST", "/api/v1/telemetry/ingest", `{invalid`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestIngestTelemetry_HTTPInvalidFrames 验证包含非法帧的批量上报：
// 只有合法帧被发布，非法帧被丢弃（drop），响应反映实际接受数。
func TestIngestTelemetry_HTTPInvalidFrames(t *testing.T) {
	server, bus := ingestTestServer(t)

	// 第一帧合法，第二帧缺少 SatelliteID（非法）
	frames := []map[string]any{
		{"satelliteId": "valid-1", "parameters": map[string]any{"v": 1}, "quality": "good"},
		{"parameters": map[string]any{"v": 2}, "quality": "good"},
	}

	// 先订阅再上报
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	resp := doRequest(t, server, "POST", "/api/v1/telemetry/ingest", map[string]any{"frames": frames})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var body map[string]any
	decodeJSON(t, resp, &body)
	assert.Equal(t, float64(1), body["accepted"])
	assert.Equal(t, float64(2), body["total"])

	// 订阅方只收到合法帧
	got := make([]string, 0, 2)
	deadline := time.After(3 * time.Second)
	for len(got) < 1 {
		select {
		case e := <-ch:
			assert.Equal(t, event.EventTelemetryReceived, e.EventType)
			got = append(got, e.SourceNodeID)
		case <-deadline:
			t.Fatalf("HTTP 入口事件丢失: 收到 %d/2", len(got))
		}
	}
	assert.Equal(t, []string{"valid-1"}, got)
}

// TestIngestTelemetry_HTTPJSONFieldNames 验证上报端点在缺少时间戳时能用当前时间补齐，
// 且通过 schema 校验后正常入总线（T5.7）。
func TestIngestTelemetry_HTTPJSONFieldNames(t *testing.T) {
	server, bus := ingestTestServer(t)

	// 通过原始 JSON 串验证字段名映射（snake 开头的小写键）
	const payload = `{"frames":[{"satelliteId":"raw-1","quality":"good"}]}`
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	resp := doRequestRaw(t, server, "POST", "/api/v1/telemetry/ingest", payload)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	decodeJSON(t, resp, &map[string]any{})

	select {
	case e := <-ch:
		assert.Equal(t, "raw-1", e.SourceNodeID)
		var p event.TelemetryReceivedPayload
		require.NoError(t, e.GetPayload(&p))
		assert.False(t, p.Timestamp.IsZero())
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP 原始 JSON 上报事件超时")
	}
}