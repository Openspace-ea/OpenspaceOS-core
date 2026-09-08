// Package main 提供 Openspace OS CLI 工具的集成测试。
//
// 使用 httptest.Server 模拟 Openspace OS Core 服务，验证各子命令的参数解析、
// 请求构造与输出格式化行为。
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCore 模拟 Openspace OS Core 服务，记录最近一次请求并返回预设响应。
type mockCore struct {
	server *httptest.Server
	mu     sync.Mutex
	lastMethod string
	lastPath   string
	lastBody   []byte
	lastAuth   string
}

// newMockCore 创建模拟 Core 服务。
func newMockCore() *mockCore {
	m := &mockCore{}
	m.server = httptest.NewServer(http.HandlerFunc(m.handler))
	return m
}

// close 关闭模拟服务。
func (m *mockCore) close() { m.server.Close() }

// reset 清空记录的请求信息。
func (m *mockCore) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastMethod = ""
	m.lastPath = ""
	m.lastBody = nil
	m.lastAuth = ""
}

// snapshot 返回最近一次请求的快照。
func (m *mockCore) snapshot() (method, path, auth string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastMethod, m.lastPath, m.lastAuth, m.lastBody
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handler 根据方法和路径分发到预设的模拟响应。
func (m *mockCore) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	m.mu.Lock()
	m.lastMethod = r.Method
	m.lastPath = r.URL.RequestURI() // 包含查询参数
	m.lastAuth = r.Header.Get("Authorization")
	m.lastBody = body
	m.mu.Unlock()

	// 检查 Content-Type
	if r.Header.Get("Content-Type") != "application/json" && len(body) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	path := r.URL.Path
	switch {
	// 健康检查
	case path == "/healthz" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	// 节点：创建
	case path == "/api/v1/nodes" && r.Method == http.MethodPost:
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		resp := map[string]any{
			"nodeId":           req["nodeId"],
			"nodeType":         req["nodeType"],
			"name":             req["name"],
			"status":           req["status"],
			"ownerCommunityId": req["ownerCommunityId"],
			"createdAt":        "2024-01-01T00:00:00Z",
			"updatedAt":        "2024-01-01T00:00:00Z",
		}
		writeJSON(w, http.StatusCreated, resp)

	// 节点：列表
	case path == "/api/v1/nodes" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"nodeId": "sat-1", "nodeType": "Satellite", "name": "TestSat", "status": "active", "ownerCommunityId": "comm-1"},
			{"nodeId": "gs-1", "nodeType": "GroundStation", "name": "TestGS", "status": "active", "ownerCommunityId": "comm-1"},
		})

	// 节点：关系图
	case strings.HasPrefix(path, "/api/v1/nodes/") && strings.HasSuffix(path, "/graph") && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"relId": "rel-1", "fromNodeId": "sat-1", "toNodeId": "comm-1", "relType": "belongsTo"},
		})

	// 节点：建立关系
	case strings.HasPrefix(path, "/api/v1/nodes/") && strings.HasSuffix(path, "/relationships") && r.Method == http.MethodPost:
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		writeJSON(w, http.StatusCreated, map[string]any{
			"relId":      "rel-1",
			"fromNodeId": strings.TrimPrefix(strings.TrimSuffix(path, "/relationships"), "/api/v1/nodes/"),
			"toNodeId":   req["toNodeId"],
			"relType":    req["relType"],
			"createdAt":  "2024-01-01T00:00:00Z",
		})

	// 节点：查询单个
	case strings.HasPrefix(path, "/api/v1/nodes/") && r.Method == http.MethodGet:
		nodeID := strings.TrimPrefix(path, "/api/v1/nodes/")
		if nodeID == "notfound" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not Found", "message": "节点不存在"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"nodeId":           nodeID,
			"nodeType":         "Satellite",
			"name":             "TestSat",
			"status":           "active",
			"ownerCommunityId": "comm-1",
			"createdAt":        "2024-01-01T00:00:00Z",
			"updatedAt":        "2024-01-01T00:00:00Z",
		})

	// 节点：更新
	case strings.HasPrefix(path, "/api/v1/nodes/") && r.Method == http.MethodPut:
		nodeID := strings.TrimPrefix(path, "/api/v1/nodes/")
		writeJSON(w, http.StatusOK, map[string]any{
			"nodeId":           nodeID,
			"nodeType":         "Satellite",
			"name":             "UpdatedSat",
			"status":           "inactive",
			"ownerCommunityId": "comm-1",
			"updatedAt":        "2024-01-01T00:00:00Z",
		})

	// 节点：删除
	case strings.HasPrefix(path, "/api/v1/nodes/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)

	// 关系：删除
	case strings.HasPrefix(path, "/api/v1/relationships/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)

	// 事件 Schema
	case path == "/api/v1/schemas" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"eventType": "TelemetryReceived", "version": "1.0.0", "fields": []any{"a", "b", "c", "d"}},
			{"eventType": "NodeRegistered", "version": "1.0.0", "fields": []any{"a", "b"}},
		})

	// 事件回放
	case path == "/api/v1/events/replay" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"eventId": "evt-1", "eventType": "NodeRegistered", "sourceNodeId": "sat-1", "timestamp": "2024-01-01T00:00:00Z"},
		})

	// 指令：发送
	case path == "/api/v1/commands" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusAccepted, map[string]string{"commandId": "cmd-1", "status": "queued"})

	// 指令：列表
	case path == "/api/v1/commands" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"commandId": "cmd-1", "satelliteId": "sat-1", "commandType": "attitude", "priority": float64(5), "status": "queued"},
		})

	// 指令：查询状态
	case strings.HasPrefix(path, "/api/v1/commands/") && r.Method == http.MethodGet:
		cmdID := strings.TrimPrefix(path, "/api/v1/commands/")
		writeJSON(w, http.StatusOK, map[string]any{
			"commandId":   cmdID,
			"satelliteId": "sat-1",
			"commandType": "attitude",
			"priority":    float64(5),
			"status":      "acked",
		})

	// 指令：取消
	case strings.HasPrefix(path, "/api/v1/commands/") && r.Method == http.MethodDelete:
		cmdID := strings.TrimPrefix(path, "/api/v1/commands/")
		writeJSON(w, http.StatusOK, map[string]string{"commandId": cmdID, "status": "cancelled"})

	// 遥测解析器
	case path == "/api/v1/telemetry/parsers" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"parsers": []string{"json", "tle"}, "active": "json", "available": true})

	// 遥测发送
	case path == "/api/v1/telemetry/send" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "parser": "json"})

	// 插件列表
	case path == "/api/v1/plugins" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []map[string]any{
			{"Manifest": map[string]any{"name": "telemetry-dummy", "version": "1.0.0"}, "Status": "started"},
		})

	// 插件卸载
	case strings.HasPrefix(path, "/api/v1/plugins/") && strings.HasSuffix(path, "/unload") && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusNoContent)

	// 认证：登录
	case path == "/api/v1/auth/login" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, map[string]any{"token": "jwt-token-123", "expiresIn": float64(3600)})

	// 认证：当前用户
	case path == "/api/v1/auth/me" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"userId": "u-1", "username": "admin", "roles": []string{"admin"}})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// captureStdout 捕获函数执行期间的标准输出。
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan string)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()

	f()

	os.Stdout = old
	_ = w.Close()
	return <-done
}

// runCmd 执行 CLI 命令并返回输出与错误。自动注入 --server 参数。
func runCmd(serverURL string, args ...string) (string, error) {
	fullArgs := append([]string{"--server", serverURL}, args...)
	var execErr error
	out := captureStdout(func() {
		// 每次执行前重置输出格式
		outputFmt = "json"
		token = ""
		root := newRootCmd()
		root.SilenceUsage = true
		root.SilenceErrors = true
		root.SetArgs(fullArgs)
		execErr = root.Execute()
	})
	return out, execErr
}

// decodeBody 解码记录的请求体到 map。
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	if len(body) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

// -------------------- 测试用例 --------------------

func TestStatusCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "status")
	require.NoError(t, err)
	assert.Contains(t, out, "ok")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/healthz", path)
}

func TestNodeRegisterCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "register",
		"--type", "Satellite", "--name", "TestSat", "--id", "sat-1",
		"--community", "comm-1", "--status", "active")
	require.NoError(t, err)
	assert.Contains(t, out, "sat-1")
	assert.Contains(t, out, "TestSat")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/nodes", path)
	req := decodeBody(t, body)
	assert.Equal(t, "Satellite", req["nodeType"])
	assert.Equal(t, "TestSat", req["name"])
	assert.Equal(t, "sat-1", req["nodeId"])
	assert.Equal(t, "comm-1", req["ownerCommunityId"])
	assert.Equal(t, "active", req["status"])
}

func TestNodeRegisterMissingRequiredFlag(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	// 缺少 --type 必填参数，应返回错误
	_, err := runCmd(mc.server.URL, "node", "register",
		"--name", "TestSat", "--id", "sat-1",
		"--community", "comm-1", "--status", "active")
	require.Error(t, err)
}

func TestNodeListCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "sat-1")
	assert.Contains(t, out, "gs-1")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/nodes", path)
}

func TestNodeListWithFilters(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	_, err := runCmd(mc.server.URL, "node", "list", "--type", "Satellite", "--community", "comm-1")
	require.NoError(t, err)

	_, path, _, _ := mc.snapshot()
	assert.Contains(t, path, "nodeType=Satellite")
	assert.Contains(t, path, "communityId=comm-1")
}

func TestNodeListTableOutput(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "list", "--output", "table")
	require.NoError(t, err)
	assert.Contains(t, out, "NodeID")
	assert.Contains(t, out, "Type")
	assert.Contains(t, out, "sat-1")
}

func TestNodeShowCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "show", "sat-1")
	require.NoError(t, err)
	assert.Contains(t, out, "sat-1")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/nodes/sat-1", path)
}

func TestNodeUpdateCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "update", "sat-1", "--status", "inactive")
	require.NoError(t, err)
	assert.Contains(t, out, "inactive")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPut, method)
	assert.Equal(t, "/api/v1/nodes/sat-1", path)
	req := decodeBody(t, body)
	assert.Equal(t, "inactive", req["status"])
}

func TestNodeUpdateNoChanges(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	// 未指定任何更新字段，应返回错误
	_, err := runCmd(mc.server.URL, "node", "update", "sat-1")
	require.Error(t, err)
}

func TestNodeDeleteCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "delete", "sat-1")
	require.NoError(t, err)
	assert.Contains(t, out, "已删除")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/api/v1/nodes/sat-1", path)
}

func TestNodeRelateCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "relate", "sat-1", "--to", "comm-1", "--type", "belongsTo")
	require.NoError(t, err)
	assert.Contains(t, out, "rel-1")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/nodes/sat-1/relationships", path)
	req := decodeBody(t, body)
	assert.Equal(t, "comm-1", req["toNodeId"])
	assert.Equal(t, "belongsTo", req["relType"])
}

func TestNodeGraphCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "node", "graph", "sat-1", "--depth", "2", "--rel-type", "belongsTo")
	require.NoError(t, err)
	assert.Contains(t, out, "rel-1")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Contains(t, path, "/api/v1/nodes/sat-1/graph")
	assert.Contains(t, path, "depth=2")
	assert.Contains(t, path, "relType=belongsTo")
}

func TestRelationshipDeleteCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "relationship", "delete", "rel-1")
	require.NoError(t, err)
	assert.Contains(t, out, "已删除")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/api/v1/relationships/rel-1", path)
}

func TestEventSchemasCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "event", "schemas")
	require.NoError(t, err)
	assert.Contains(t, out, "TelemetryReceived")
	assert.Contains(t, out, "NodeRegistered")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/schemas", path)
}

func TestEventSchemasTableOutput(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "event", "schemas", "--output", "table")
	require.NoError(t, err)
	assert.Contains(t, out, "EventType")
	assert.Contains(t, out, "TelemetryReceived")
}

func TestEventReplayCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "event", "replay", "--type", "NodeRegistered,StateUpdated", "--limit", "50")
	require.NoError(t, err)
	assert.Contains(t, out, "evt-1")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/events/replay", path)
	req := decodeBody(t, body)
	types, ok := req["eventTypes"].([]any)
	require.True(t, ok)
	assert.Len(t, types, 2)
	assert.Equal(t, float64(50), req["limit"])
}

func TestCommandSendCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "command", "send",
		"--sat", "sat-1", "--type", "attitude", "--priority", "5",
		"--params", `{"mode":"normal"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "cmd-1")
	assert.Contains(t, out, "queued")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/commands", path)
	req := decodeBody(t, body)
	assert.Equal(t, "sat-1", req["satelliteId"])
	assert.Equal(t, "attitude", req["commandType"])
	assert.Equal(t, float64(5), req["priority"])
	params, ok := req["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "normal", params["mode"])
}

func TestCommandSendInvalidParams(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	_, err := runCmd(mc.server.URL, "command", "send",
		"--sat", "sat-1", "--type", "attitude", "--params", "invalid-json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不是有效的 JSON")
}

func TestCommandStatusCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "command", "status", "cmd-1")
	require.NoError(t, err)
	assert.Contains(t, out, "acked")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/commands/cmd-1", path)
}

func TestCommandListCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "command", "list", "--status", "queued")
	require.NoError(t, err)
	assert.Contains(t, out, "cmd-1")

	_, path, _, _ := mc.snapshot()
	assert.Contains(t, path, "status=queued")
}

func TestCommandCancelCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "command", "cancel", "cmd-1")
	require.NoError(t, err)
	assert.Contains(t, out, "cancelled")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/api/v1/commands/cmd-1", path)
}

func TestTelemetryParsersCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "telemetry", "parsers")
	require.NoError(t, err)
	assert.Contains(t, out, "json")
	assert.Contains(t, out, "active")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/telemetry/parsers", path)
}

func TestTelemetrySendCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "telemetry", "send", "--data", `{"satelliteId":"sat-1"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "accepted")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/telemetry/send", path)
	req := decodeBody(t, body)
	assert.Equal(t, `{"satelliteId":"sat-1"}`, req["data"])
}

func TestPluginListCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "plugin", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "telemetry-dummy")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/plugins", path)
}

func TestPluginListTableOutput(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "plugin", "list", "--output", "table")
	require.NoError(t, err)
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "Version")
	assert.Contains(t, out, "Status")
	assert.Contains(t, out, "telemetry-dummy")
}

func TestPluginUnloadCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "plugin", "unload", "telemetry-dummy")
	require.NoError(t, err)
	assert.Contains(t, out, "已卸载")

	method, path, _, _ := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/plugins/telemetry-dummy/unload", path)
}

func TestAuthLoginCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "auth", "login", "--username", "admin", "--password", "admin123")
	require.NoError(t, err)
	assert.Contains(t, out, "jwt-token-123")
	assert.Contains(t, out, "expiresIn")

	method, path, _, body := mc.snapshot()
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/auth/login", path)
	req := decodeBody(t, body)
	assert.Equal(t, "admin", req["username"])
	assert.Equal(t, "admin123", req["password"])
}

func TestAuthMeCommand(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	out, err := runCmd(mc.server.URL, "auth", "me", "--token", "my-jwt")
	require.NoError(t, err)
	assert.Contains(t, out, "admin")

	method, path, auth, _ := mc.snapshot()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/auth/me", path)
	assert.Equal(t, "Bearer my-jwt", auth)
}

func TestTokenFromEnvVar(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	// 设置环境变量
	old := os.Getenv("OPENSPACE_TOKEN")
	_ = os.Setenv("OPENSPACE_TOKEN", "env-jwt-token")
	defer func() { _ = os.Setenv("OPENSPACE_TOKEN", old) }()

	_, err := runCmd(mc.server.URL, "auth", "me")
	require.NoError(t, err)

	_, _, auth, _ := mc.snapshot()
	assert.Equal(t, "Bearer env-jwt-token", auth)
}

func TestTokenFlagOverridesEnvVar(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	old := os.Getenv("OPENSPACE_TOKEN")
	_ = os.Setenv("OPENSPACE_TOKEN", "env-jwt-token")
	defer func() { _ = os.Setenv("OPENSPACE_TOKEN", old) }()

	_, err := runCmd(mc.server.URL, "auth", "me", "--token", "flag-jwt")
	require.NoError(t, err)

	_, _, auth, _ := mc.snapshot()
	assert.Equal(t, "Bearer flag-jwt", auth)
}

func TestServerErrorHandling(t *testing.T) {
	mc := newMockCore()
	defer mc.close()

	// 请求不存在的节点，模拟服务端返回 404
	_, err := runCmd(mc.server.URL, "node", "show", "notfound")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestPrintJSON(t *testing.T) {
	out := captureStdout(func() {
		PrintJSON(map[string]string{"key": "value"})
	})
	assert.Contains(t, out, "key")
	assert.Contains(t, out, "value")
}

func TestPrintTable(t *testing.T) {
	out := captureStdout(func() {
		PrintTable(
			[]string{"Name", "Age"},
			[][]string{{"Alice", "30"}, {"Bob", "25"}},
		)
	})
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "Age")
	assert.Contains(t, out, "Alice")
	assert.Contains(t, out, "Bob")
	assert.Contains(t, out, "--") // 分隔线
}

func TestPrintError(t *testing.T) {
	// PrintError 写入 stderr，此处仅验证不 panic
	PrintError(assertError("test error"))
}

// assertError 返回一个错误用于测试。
func assertError(msg string) error {
	return &testError{msg: msg}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestStrField(t *testing.T) {
	m := map[string]any{"name": "test", "num": float64(42), "nil": nil}
	assert.Equal(t, "test", strField(m, "name"))
	assert.Equal(t, "42", strField(m, "num"))
	assert.Equal(t, "", strField(m, "nil"))
	assert.Equal(t, "", strField(m, "missing"))
}

func TestNestedStr(t *testing.T) {
	m := map[string]any{
		"Manifest": map[string]any{"name": "plugin-1", "version": "1.0.0"},
	}
	assert.Equal(t, "plugin-1", nestedStr(m, "Manifest", "name"))
	assert.Equal(t, "1.0.0", nestedStr(m, "Manifest", "version"))
	assert.Equal(t, "", nestedStr(m, "NonExistent", "field"))
}

func TestRootCommandHasAllSubcommands(t *testing.T) {
	root := newRootCmd()
	expected := []string{"status", "node", "relationship", "event", "command", "telemetry", "plugin", "auth"}
	for _, name := range expected {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		assert.True(t, found, "根命令应包含子命令: %s", name)
	}
}

func TestNodeSubcommands(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"node", "--help"})
	err := root.Execute()
	require.NoError(t, err)
}

func TestClientMethods(t *testing.T) {
	// 验证 Client 的 Get/Post/Put/Delete 方法签名可编译且可调用
	c := NewClient("http://example.com", "token")
	require.NotNil(t, c)
	// 不实际发送请求，仅验证对象构造
}
