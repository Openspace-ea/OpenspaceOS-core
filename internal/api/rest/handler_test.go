package rest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// newTestServer 创建用于测试的 HTTP 服务器，使用内存 SQLite 和 MemoryEventStore。
func newTestServer(t *testing.T) *httptest.Server {
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
	handler := NewHandler(kg, bus, registry, nil)
	router := NewRouter(handler)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

// doRequest 发送 HTTP 请求并返回响应。body 为 nil 时发送无 body 请求。
func doRequest(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, server.URL+path, reqBody)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// doRequestRaw 发送原始 JSON 字符串的 HTTP 请求。
func doRequestRaw(t *testing.T, server *httptest.Server, method, path, jsonBody string) *http.Response {
	t.Helper()
	var reqBody io.Reader
	if jsonBody != "" {
		reqBody = bytes.NewReader([]byte(jsonBody))
	}
	req, err := http.NewRequest(method, server.URL+path, reqBody)
	require.NoError(t, err)
	if jsonBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// decodeJSON 解码响应体到目标结构体。
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	err := json.NewDecoder(resp.Body).Decode(v)
	require.NoError(t, err)
}

// createNodeViaAPI 通过 REST API 创建 Node，返回创建的 Node。
func createNodeViaAPI(t *testing.T, server *httptest.Server, nodeID, nodeType, name, status, communityID string) *model.Node {
	t.Helper()
	body := fmt.Sprintf(`{"nodeId":"%s","nodeType":"%s","name":"%s","status":"%s","ownerCommunityId":"%s"}`,
		nodeID, nodeType, name, status, communityID)
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "创建节点应返回 201")
	var node model.Node
	decodeJSON(t, resp, &node)
	return &node
}

// TestNodeCRUD 测试 Node 的完整 CRUD 流程：创建 → 查询 → 更新 → 列表 → 删除。
func TestNodeCRUD(t *testing.T) {
	server := newTestServer(t)

	// 1. 创建 Node
	created := createNodeViaAPI(t, server, "sat-crud-1", "Satellite", "测试卫星", "active", "comm-1")
	assert.Equal(t, "sat-crud-1", created.NodeID)
	assert.Equal(t, model.NodeTypeSatellite, created.NodeType)
	assert.Equal(t, "测试卫星", created.Name)
	assert.Equal(t, "active", created.Status)
	assert.Equal(t, "comm-1", created.OwnerCommunityID)
	assert.False(t, created.CreatedAt.IsZero())
	assert.False(t, created.UpdatedAt.IsZero())

	// 2. 查询 Node
	resp := doRequest(t, server, "GET", "/api/v1/nodes/sat-crud-1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got model.Node
	decodeJSON(t, resp, &got)
	assert.Equal(t, "sat-crud-1", got.NodeID)
	assert.Equal(t, "测试卫星", got.Name)

	// 3. 更新 Node
	resp = doRequestRaw(t, server, "PUT", "/api/v1/nodes/sat-crud-1", `{"status":"standby","name":"更新卫星"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updated model.Node
	decodeJSON(t, resp, &updated)
	assert.Equal(t, "standby", updated.Status)
	assert.Equal(t, "更新卫星", updated.Name)

	// 4. 列表查询
	resp = doRequest(t, server, "GET", "/api/v1/nodes?limit=10", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var nodes []*model.Node
	decodeJSON(t, resp, &nodes)
	require.Len(t, nodes, 1)
	assert.Equal(t, "sat-crud-1", nodes[0].NodeID)

	// 5. 删除 Node
	resp = doRequest(t, server, "DELETE", "/api/v1/nodes/sat-crud-1", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// 6. 删除后查询应返回 404
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-crud-1", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// TestNodeNotFound 测试对不存在的 nodeId 执行 GET/PUT/DELETE 返回 404。
func TestNodeNotFound(t *testing.T) {
	server := newTestServer(t)

	// GET 不存在的节点
	resp := doRequest(t, server, "GET", "/api/v1/nodes/nonexistent", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// PUT 不存在的节点
	resp = doRequestRaw(t, server, "PUT", "/api/v1/nodes/nonexistent", `{"status":"active"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// DELETE 不存在的节点
	resp = doRequest(t, server, "DELETE", "/api/v1/nodes/nonexistent", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// TestCreateNodeValidation 测试创建 Node 缺少必填字段时返回 400。
func TestCreateNodeValidation(t *testing.T) {
	server := newTestServer(t)

	// 缺少 nodeId
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeType":"Satellite","name":"测试","status":"active","ownerCommunityId":"comm-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 缺少 nodeType
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeId":"sat-v-1","name":"测试","status":"active","ownerCommunityId":"comm-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 缺少 name
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeId":"sat-v-2","nodeType":"Satellite","status":"active","ownerCommunityId":"comm-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 缺少 status
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeId":"sat-v-3","nodeType":"Satellite","name":"测试","ownerCommunityId":"comm-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 缺少 ownerCommunityId
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeId":"sat-v-4","nodeType":"Satellite","name":"测试","status":"active"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 无效的 nodeType
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{"nodeId":"sat-v-5","nodeType":"InvalidType","name":"测试","status":"active","ownerCommunityId":"comm-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 无效的 JSON
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", `{invalid json}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestRelationship 测试关系管理：创建两个 Node → 建立关系 → 查询图 → 删除关系。
func TestRelationship(t *testing.T) {
	server := newTestServer(t)

	// 创建两个 Node
	createNodeViaAPI(t, server, "sat-rel-1", "Satellite", "测试卫星", "active", "comm-rel-1")
	createNodeViaAPI(t, server, "comm-rel-1", "Community", "测试社区", "active", "comm-rel-1")

	// 建立关系
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-rel-1/relationships",
		`{"toNodeId":"comm-rel-1","relType":"belongsTo"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var rel model.Relationship
	decodeJSON(t, resp, &rel)
	assert.NotEmpty(t, rel.RelID)
	assert.Equal(t, "sat-rel-1", rel.FromNodeID)
	assert.Equal(t, "comm-rel-1", rel.ToNodeID)
	assert.Equal(t, model.RelBelongsTo, rel.RelType)
	assert.False(t, rel.CreatedAt.IsZero())

	// 查询关系图
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-rel-1/graph?direction=out&depth=1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rels []*model.Relationship
	decodeJSON(t, resp, &rels)
	require.Len(t, rels, 1)
	assert.Equal(t, rel.RelID, rels[0].RelID)

	// 删除关系
	resp = doRequest(t, server, "DELETE", "/api/v1/relationships/"+rel.RelID, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// 删除后查询图应为空
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-rel-1/graph?direction=out&depth=1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &rels)
	assert.Empty(t, rels)
}

// TestRelationshipValidation 测试创建关系时缺少必填字段返回 400。
func TestRelationshipValidation(t *testing.T) {
	server := newTestServer(t)
	createNodeViaAPI(t, server, "sat-rv-1", "Satellite", "测试卫星", "active", "comm-rv-1")
	createNodeViaAPI(t, server, "comm-rv-1", "Community", "测试社区", "active", "comm-rv-1")

	// 缺少 toNodeId
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-rv-1/relationships", `{"relType":"belongsTo"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 缺少 relType
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-rv-1/relationships", `{"toNodeId":"comm-rv-1"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 无效的 relType
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-rv-1/relationships", `{"toNodeId":"comm-rv-1","relType":"invalidType"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestSSESubscribe 测试 SSE 事件订阅：订阅 → 创建 Node → 验证收到 NodeRegistered 事件。
func TestSSESubscribe(t *testing.T) {
	server := newTestServer(t)

	// 开始 SSE 订阅
	resp, err := http.Get(server.URL + "/api/v1/events/subscribe?types=NodeRegistered")
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// 等待连接确认
	waitForSSELine(t, scanner, "connected", 5*time.Second)

	// 创建 Node 触发 NodeRegistered 事件
	createNodeViaAPI(t, server, "sat-sse-1", "Satellite", "SSE测试卫星", "active", "comm-sse-1")

	// 读取并验证 SSE 事件
	e := readSSEEvent(t, scanner, 5*time.Second)
	assert.Equal(t, event.EventNodeRegistered, e.EventType)
	assert.Equal(t, "sat-sse-1", e.SourceNodeID)
}

// TestSSESubscribeAll 测试不带类型过滤的 SSE 订阅能收到所有事件。
func TestSSESubscribeAll(t *testing.T) {
	server := newTestServer(t)

	// 不带 types 参数订阅所有事件
	resp, err := http.Get(server.URL + "/api/v1/events/subscribe")
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// 等待连接确认
	waitForSSELine(t, scanner, "connected", 5*time.Second)

	// 创建 Node 会触发 NodeRegistered 和 StateUpdated 两个事件
	createNodeViaAPI(t, server, "sat-sse-all-1", "Satellite", "全量订阅测试", "active", "comm-sse-all")

	// 读取第一个事件（NodeRegistered）
	e1 := readSSEEvent(t, scanner, 5*time.Second)
	assert.Equal(t, event.EventNodeRegistered, e1.EventType)

	// 读取第二个事件（StateUpdated）
	e2 := readSSEEvent(t, scanner, 5*time.Second)
	assert.Equal(t, event.EventStateUpdated, e2.EventType)
}

// TestEventReplay 测试事件回放：创建 Node → 回放事件 → 验证返回历史事件。
func TestEventReplay(t *testing.T) {
	server := newTestServer(t)

	// 创建 Node 触发事件发布
	createNodeViaAPI(t, server, "sat-rep-1", "Satellite", "回放测试卫星", "active", "comm-rep-1")

	// 回放所有事件
	resp := doRequestRaw(t, server, "POST", "/api/v1/events/replay", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var events []*event.Event
	decodeJSON(t, resp, &events)

	// 创建一个 Node 会产生 NodeRegistered 和 StateUpdated 两个事件
	require.Len(t, events, 2)

	// 验证事件类型
	types := map[event.EventType]bool{}
	for _, e := range events {
		types[e.EventType] = true
		assert.Equal(t, "sat-rep-1", e.SourceNodeID)
	}
	assert.True(t, types[event.EventNodeRegistered])
	assert.True(t, types[event.EventStateUpdated])
}

// TestEventReplayWithFilter 测试按事件类型过滤回放。
func TestEventReplayWithFilter(t *testing.T) {
	server := newTestServer(t)

	createNodeViaAPI(t, server, "sat-rep-f-1", "Satellite", "过滤回放测试", "active", "comm-rep-f")

	// 只回放 NodeRegistered 事件
	resp := doRequestRaw(t, server, "POST", "/api/v1/events/replay", `{"eventTypes":["NodeRegistered"]}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var events []*event.Event
	decodeJSON(t, resp, &events)

	require.Len(t, events, 1)
	assert.Equal(t, event.EventNodeRegistered, events[0].EventType)
	assert.Equal(t, "sat-rep-f-1", events[0].SourceNodeID)
}

// TestEventReplayBySourceNode 测试按来源节点过滤回放。
func TestEventReplayBySourceNode(t *testing.T) {
	server := newTestServer(t)

	createNodeViaAPI(t, server, "sat-rep-n-1", "Satellite", "节点过滤1", "active", "comm-rep-n")
	createNodeViaAPI(t, server, "sat-rep-n-2", "Satellite", "节点过滤2", "active", "comm-rep-n")

	// 只回放 sat-rep-n-1 的事件
	resp := doRequestRaw(t, server, "POST", "/api/v1/events/replay", `{"sourceNodeId":"sat-rep-n-1"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var events []*event.Event
	decodeJSON(t, resp, &events)

	// sat-rep-n-1 创建时产生 2 个事件
	require.Len(t, events, 2)
	for _, e := range events {
		assert.Equal(t, "sat-rep-n-1", e.SourceNodeID)
	}
}

// TestListSchemas 测试 GET /api/v1/schemas 返回 14 个 Schema。
func TestListSchemas(t *testing.T) {
	server := newTestServer(t)

	resp := doRequest(t, server, "GET", "/api/v1/schemas", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var schemas []event.Schema
	decodeJSON(t, resp, &schemas)

	require.Len(t, schemas, 14, "应返回 14 个事件 Schema")

	// 验证每个 Schema 有必要字段
	for _, s := range schemas {
		assert.NotEmpty(t, s.EventType)
		assert.NotEmpty(t, s.Version)
		assert.NotNil(t, s.Fields)
	}
}

// TestHealth 测试健康检查端点。
func TestHealth(t *testing.T) {
	server := newTestServer(t)

	// 测试 /healthz
	resp := doRequest(t, server, "GET", "/healthz", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "ok", result["status"])

	// 测试 /api/v1/health
	resp = doRequest(t, server, "GET", "/api/v1/health", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &result)
	assert.Equal(t, "ok", result["status"])
	// 验证新增的版本与运行时间字段
	assert.Contains(t, result, "version")
	assert.Contains(t, result, "uptime")
}

// TestListNodesWithFilter 测试带过滤条件的列表查询。
func TestListNodesWithFilter(t *testing.T) {
	server := newTestServer(t)

	createNodeViaAPI(t, server, "sat-lf-1", "Satellite", "卫星1", "active", "comm-lf-a")
	createNodeViaAPI(t, server, "sat-lf-2", "Satellite", "卫星2", "standby", "comm-lf-a")
	createNodeViaAPI(t, server, "gs-lf-1", "GroundStation", "地面站1", "active", "comm-lf-b")

	// 按 nodeType 过滤
	resp := doRequest(t, server, "GET", "/api/v1/nodes?nodeType=Satellite", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var nodes []*model.Node
	decodeJSON(t, resp, &nodes)
	require.Len(t, nodes, 2)

	// 按 communityId 过滤
	resp = doRequest(t, server, "GET", "/api/v1/nodes?communityId=comm-lf-a", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &nodes)
	require.Len(t, nodes, 2)

	// 按 status 过滤
	resp = doRequest(t, server, "GET", "/api/v1/nodes?status=active", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &nodes)
	require.Len(t, nodes, 2)

	// 组合过滤
	resp = doRequest(t, server, "GET", "/api/v1/nodes?nodeType=Satellite&status=active", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &nodes)
	require.Len(t, nodes, 1)
	assert.Equal(t, "sat-lf-1", nodes[0].NodeID)
}

// TestUpdateNodeValidation 测试更新节点时的校验。
func TestUpdateNodeValidation(t *testing.T) {
	server := newTestServer(t)
	createNodeViaAPI(t, server, "sat-uv-1", "Satellite", "更新校验测试", "active", "comm-uv-1")

	// 空 changes
	resp := doRequestRaw(t, server, "PUT", "/api/v1/nodes/sat-uv-1", `{}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 无效 JSON
	resp = doRequestRaw(t, server, "PUT", "/api/v1/nodes/sat-uv-1", `invalid`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestCreateRelationshipWithProperties 测试带 properties 创建关系。
func TestCreateRelationshipWithProperties(t *testing.T) {
	server := newTestServer(t)

	createNodeViaAPI(t, server, "sat-rp-1", "Satellite", "属性关系测试卫星", "active", "comm-rp-1")
	createNodeViaAPI(t, server, "gs-rp-1", "GroundStation", "属性关系测试地面站", "active", "comm-rp-1")

	// 创建带 properties 的关系
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-rp-1/relationships",
		`{"toNodeId":"gs-rp-1","relType":"controlledBy","properties":{"since":"2026-01-01"}}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var rel model.Relationship
	decodeJSON(t, resp, &rel)
	assert.Equal(t, model.RelControlledBy, rel.RelType)
	assert.Equal(t, "2026-01-01", rel.Properties["since"])
}

// TestGraphTraverseBothDirections 测试双向图遍历。
func TestGraphTraverseBothDirections(t *testing.T) {
	server := newTestServer(t)

	createNodeViaAPI(t, server, "sat-tb-1", "Satellite", "遍历测试卫星", "active", "comm-tb-1")
	createNodeViaAPI(t, server, "gs-tb-1", "GroundStation", "遍历测试地面站", "active", "comm-tb-1")

	// sat -> gs (controlledBy)
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-tb-1/relationships",
		`{"toNodeId":"gs-tb-1","relType":"controlledBy"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// gs -> sat (providesServiceTo)
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/gs-tb-1/relationships",
		`{"toNodeId":"sat-tb-1","relType":"providesServiceTo"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 双向遍历应返回 2 条关系
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-tb-1/graph?direction=both&depth=1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rels []*model.Relationship
	decodeJSON(t, resp, &rels)
	require.Len(t, rels, 2)
}

// waitForSSELine 等待 SSE 流中包含指定关键字的行，超时则 fatal。
func waitForSSELine(t *testing.T, scanner *bufio.Scanner, keyword string, timeout time.Duration) {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), keyword) {
				done <- true
				return
			}
		}
		done <- false
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatalf("SSE 流结束，未找到关键字: %s", keyword)
		}
	case <-time.After(timeout):
		t.Fatalf("等待 SSE 关键字 '%s' 超时", keyword)
	}
}

// readSSEEvent 从 SSE 流中读取一个事件，超时则 fatal。
func readSSEEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) *event.Event {
	t.Helper()
	eventCh := make(chan *event.Event, 1)
	errCh := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var e event.Event
				if err := json.Unmarshal([]byte(line[6:]), &e); err != nil {
					errCh <- err
					return
				}
				eventCh <- &e
				return
			}
		}
		errCh <- fmt.Errorf("SSE 流结束，未收到事件")
	}()

	select {
	case e := <-eventCh:
		return e
	case err := <-errCh:
		t.Fatalf("读取 SSE 事件失败: %v", err)
		return nil
	case <-time.After(timeout):
		t.Fatal("等待 SSE 事件超时")
		return nil
	}
}

// readBody 读取响应体并以字符串返回。
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}

// TestSwaggerUI 测试 GET /api/v1/docs 返回 Swagger UI 页面。
func TestSwaggerUI(t *testing.T) {
	server := newTestServer(t)

	resp := doRequest(t, server, "GET", "/api/v1/docs", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 验证 Content-Type 为 text/html
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "text/html", "Swagger UI 响应应为 text/html")

	body := readBody(t, resp)
	// 验证页面包含 Swagger UI 关键标记
	assert.Contains(t, body, "swagger-ui", "页面应包含 swagger-ui 容器")
	assert.Contains(t, body, "SwaggerUIBundle", "页面应加载 SwaggerUIBundle 脚本")
	assert.Contains(t, body, "/api/v1/openapi.yaml", "页面应引用 openapi.yaml 规范文件")
}

// TestSwaggerUIMethodNotAllowed 测试 /api/v1/docs 非 GET 方法返回 405。
func TestSwaggerUIMethodNotAllowed(t *testing.T) {
	server := newTestServer(t)

	resp := doRequest(t, server, "POST", "/api/v1/docs", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()
}

// TestOpenAPISpec 测试 GET /api/v1/openapi.yaml 返回 OpenAPI YAML 规范。
func TestOpenAPISpec(t *testing.T) {
	server := newTestServer(t)

	resp := doRequest(t, server, "GET", "/api/v1/openapi.yaml", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 验证 Content-Type 为 application/yaml
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "application/yaml", "OpenAPI 规范响应应为 application/yaml")

	body := readBody(t, resp)

	// 验证 YAML 基本结构
	assert.Contains(t, body, "openapi: 3.0.3", "应为 OpenAPI 3.0.3 规范")
	assert.Contains(t, body, "title: Openspace OS Core REST API", "应包含标题")
	assert.Contains(t, body, "BearerAuth", "应包含 Bearer 认证方案")

	// 验证包含所有 paths
	expectedPaths := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/me",
		"/api/v1/users",
		"/api/v1/users/{userId}",
		"/api/v1/nodes",
		"/api/v1/nodes/{nodeId}",
		"/api/v1/nodes/{nodeId}/relationships",
		"/api/v1/relationships/{relId}",
		"/api/v1/nodes/{nodeId}/graph",
		"/api/v1/events/subscribe",
		"/api/v1/events/replay",
		"/api/v1/schemas",
		"/api/v1/commands",
		"/api/v1/commands/{commandId}",
		"/api/v1/command-adapters",
		"/api/v1/command-adapter",
		"/api/v1/telemetry/parsers",
		"/api/v1/telemetry/parser",
		"/api/v1/telemetry/send",
		"/api/v1/plugins",
		"/api/v1/plugins/{name}/unload",
		"/api/v1/health",
		"/healthz",
		"/metrics",
		"/api/v1/docs",
		"/api/v1/openapi.yaml",
	}
	for _, p := range expectedPaths {
		// 在 YAML 中 path 以引号包围或直接出现，这里用 path: 前缀匹配
		assert.Contains(t, body, "  "+p+":", "OpenAPI 规范应包含路径 %s", p)
	}

	// 验证包含所有 components/schemas
	expectedSchemas := []string{
		"Node:",
		"NodeType:",
		"Relationship:",
		"RelationshipType:",
		"Event:",
		"Schema:",
		"ErrorResponse:",
		"LoginRequest:",
		"LoginResponse:",
		"CreateNodeRequest:",
		"CreateRelationshipRequest:",
		"SendCommandRequest:",
		"SendTelemetryRequest:",
	}
	for _, s := range expectedSchemas {
		assert.Contains(t, body, "    "+s, "OpenAPI 规范应包含 schema %s", s)
	}

	// 验证包含所有 tags
	expectedTags := []string{"Auth", "Users", "Nodes", "Relationships", "Events", "Commands", "Telemetry", "Plugins", "Health"}
	for _, tag := range expectedTags {
		assert.Contains(t, body, "  - name: "+tag, "OpenAPI 规范应包含 tag %s", tag)
	}
}

// TestOpenAPISpecMethodNotAllowed 测试 /api/v1/openapi.yaml 非 GET 方法返回 405。
func TestOpenAPISpecMethodNotAllowed(t *testing.T) {
	server := newTestServer(t)

	resp := doRequest(t, server, "POST", "/api/v1/openapi.yaml", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	resp.Body.Close()
}
