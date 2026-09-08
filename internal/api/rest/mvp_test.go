// Package rest 的 MVP 闭环集成测试。
//
// 本文件验证 Openspace OS Core Phase 1 MVP 的 6 个核心场景：
//   - 场景 A：注册节点并建立关系（Community / Satellite / GroundStation）
//   - 场景 B：遥测接收与事件发布（JSON 解析器）
//   - 场景 C：指令下发闭环（CommandSent → CommandAcked）
//   - 场景 D：插件与解析器切换（json / tle 热切换）
//   - 场景 E：持久化与恢复（文件 SQLite + 事件回放）
//   - 场景 F：TLE 解析验证（ISS 真实 TLE 数据）
//
// 测试使用 httptest.Server + 内存/文件 SQLite，不依赖外部服务。
package rest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/command"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// ISS 真实 TLE 数据（2008 年历元），用于场景 D 与场景 F。
const (
	mvpTLELine1 = "1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927"
	mvpTLELine2 = "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537"
	mvpTLEName  = "ISS (ZARYA)"
)

// mvpServerBundle 聚合了 MVP 测试所需的服务组件，
// 便于在测试中访问 bus、kg 等内部对象进行事件订阅与断言。
type mvpServerBundle struct {
	server     *httptest.Server
	bus        *core.LocalBus
	kg         *core.KGService
	handler    *Handler
	pluginMgr  *plugin.Manager
	telPipeline *telemetry.Pipeline
	cmdPipeline *command.Pipeline
	// closeAll 关闭所有底层资源（bus、db、pipeline 等）。
	closeAll func()
}

// newMVPTestServer 创建一个装配完整的 MVP 测试服务器：
//   - 内存 SQLite（nodes / relationships）
//   - 内存 EventStore + LocalBus
//   - 插件管理器（注册 json / tle 解析器与 mock 适配器）
//   - 遥测流水线（默认 json 解析器，不启动 TCP 接收器）
//   - 指令流水线（默认 mock 适配器，启动调度器）
//
// 认证未启用（便于测试）。所有资源在 t.Cleanup 中自动释放。
func newMVPTestServer(t *testing.T) *mvpServerBundle {
	t.Helper()
	return newMVPTestServerWithDB(t, ":memory:")
}

// newMVPTestServerWithDB 创建使用指定 SQLite 路径的 MVP 测试服务器。
//
// dbPath 为 ":memory:" 时使用内存数据库；为文件路径时使用文件 SQLite。
func newMVPTestServerWithDB(t *testing.T, dbPath string) *mvpServerBundle {
	t.Helper()
	ctx := context.Background()

	// 初始化 SQLite（nodes / relationships 表）
	db, err := core.InitDB(dbPath)
	require.NoError(t, err)

	nodeRepo := core.NewSQLiteNodeRepository(db)
	relRepo := core.NewSQLiteRelationshipRepository(db)
	registry := event.NewSchemaRegistry()

	// 事件存储：内存 SQLite 路径时用内存 EventStore，文件路径用 SQLite EventStore
	var store core.EventStore
	if dbPath == ":memory:" {
		store = core.NewMemoryEventStore(core.StoreConfig{})
	} else {
		store, err = core.NewSQLiteEventStore(ctx, dbPath, core.StoreConfig{})
		require.NoError(t, err)
	}

	bus := core.NewLocalBus(store, registry, nil)
	kg := core.NewKGService(nodeRepo, relRepo, bus, nil)
	handler := NewHandler(kg, bus, registry, nil)

	// 插件框架
	broker := plugin.NewBroker(bus, kg, nil)
	pluginMgr := plugin.NewManager(broker, nil)
	pluginMgr.RegisterParser("json", &telemetry.JSONParser{})
	pluginMgr.RegisterParser("tle", &telemetry.TLEParser{})
	pluginMgr.RegisterAdapter("mock", command.NewMockAdapter(20*time.Millisecond, 1.0))
	handler.SetPluginManager(pluginMgr)

	// 遥测流水线（仅创建并设置解析器，不启动 TCP 接收器）
	telPipeline := telemetry.NewPipeline(pluginMgr, bus, nil)
	require.NoError(t, telPipeline.SetParser("json"))
	handler.SetTelemetryPipeline(telPipeline)

	// 指令流水线（启动调度器）
	cmdPipeline := command.NewPipeline(pluginMgr, bus, nil)
	cmdPipeline.SetSchedulerConfig(command.SchedulerConfig{
		MaxConcurrent: 5,
		Timeout:       5 * time.Second,
		MaxRetries:    1,
		RetryBackoff:  50 * time.Millisecond,
	})
	require.NoError(t, cmdPipeline.SetAdapter("mock"))
	cmdPipeline.Start()
	handler.SetCommandPipeline(cmdPipeline)

	router := NewRouter(handler)
	server := httptest.NewServer(router)

	closeAll := func() {
		server.Close()
		cmdPipeline.Stop()
		_ = telPipeline.Stop()
		_ = bus.Close()
		_ = db.Close()
	}
	t.Cleanup(closeAll)

	return &mvpServerBundle{
		server:      server,
		bus:         bus,
		kg:          kg,
		handler:     handler,
		pluginMgr:   pluginMgr,
		telPipeline: telPipeline,
		cmdPipeline: cmdPipeline,
		closeAll:    closeAll,
	}
}

// waitForEvent 从事件 channel 等待指定类型的事件，超时则 fatal。
func waitForEvent(t *testing.T, ch <-chan *event.Event, want event.EventType, timeout time.Duration) *event.Event {
	t.Helper()
	for {
		select {
		case e := <-ch:
			if e.EventType == want {
				return e
			}
			// 其它事件继续等待
		case <-time.After(timeout):
			t.Fatalf("等待事件 %s 超时", want)
			return nil
		}
	}
}

// TestMVP_ScenarioA_NodeRegistration 验证场景 A：注册节点并建立关系。
//
// 流程：
//  1. 注册 Community
//  2. 注册 Satellite（含 orbit 属性）
//  3. 注册 GroundStation
//  4. 建立 Satellite belongsTo Community 关系
//  5. 建立 Satellite controlledBy GroundStation 关系
//  6. 通过图查询验证关系
//  7. 验证 NodeRegistered 事件被发布
func TestMVP_ScenarioA_NodeRegistration(t *testing.T) {
	bundle := newMVPTestServer(t)
	server := bundle.server

	// 订阅 NodeRegistered 事件（用于步骤 7 验证）
	ch, unsubscribe := bundle.bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	defer unsubscribe()

	// 1. 注册 Community
	commBody := `{"nodeId":"comm-mvp-a","nodeType":"Community","name":"MVP测试社区","status":"active","ownerCommunityId":"comm-mvp-a"}`
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes", commBody)
	require.Equal(t, 201, resp.StatusCode)
	var comm model.Node
	decodeJSON(t, resp, &comm)
	assert.Equal(t, "comm-mvp-a", comm.NodeID)
	assert.Equal(t, model.NodeTypeCommunity, comm.NodeType)

	// 2. 注册 Satellite（含 orbit 属性）
	satBody := `{"nodeId":"sat-mvp-a","nodeType":"Satellite","name":"MVP测试卫星","status":"active","ownerCommunityId":"comm-mvp-a","properties":{"orbit":{"noradId":"25544","inclination":51.6416},"noradId":"25544"}}`
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", satBody)
	require.Equal(t, 201, resp.StatusCode)
	var sat model.Node
	decodeJSON(t, resp, &sat)
	assert.Equal(t, "sat-mvp-a", sat.NodeID)
	assert.Equal(t, model.NodeTypeSatellite, sat.NodeType)
	require.NotNil(t, sat.Properties)
	assert.Equal(t, "25544", sat.Properties["noradId"])

	// 3. 注册 GroundStation
	gsBody := `{"nodeId":"gs-mvp-a","nodeType":"GroundStation","name":"MVP测试地面站","status":"active","ownerCommunityId":"comm-mvp-a"}`
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes", gsBody)
	require.Equal(t, 201, resp.StatusCode)
	var gs model.Node
	decodeJSON(t, resp, &gs)
	assert.Equal(t, "gs-mvp-a", gs.NodeID)
	assert.Equal(t, model.NodeTypeGroundStation, gs.NodeType)

	// 4. 建立 Satellite belongsTo Community 关系
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-mvp-a/relationships",
		`{"toNodeId":"comm-mvp-a","relType":"belongsTo"}`)
	require.Equal(t, 201, resp.StatusCode)
	var rel1 model.Relationship
	decodeJSON(t, resp, &rel1)
	assert.Equal(t, model.RelBelongsTo, rel1.RelType)
	assert.Equal(t, "sat-mvp-a", rel1.FromNodeID)
	assert.Equal(t, "comm-mvp-a", rel1.ToNodeID)

	// 5. 建立 Satellite controlledBy GroundStation 关系
	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-mvp-a/relationships",
		`{"toNodeId":"gs-mvp-a","relType":"controlledBy"}`)
	require.Equal(t, 201, resp.StatusCode)
	var rel2 model.Relationship
	decodeJSON(t, resp, &rel2)
	assert.Equal(t, model.RelControlledBy, rel2.RelType)
	assert.Equal(t, "sat-mvp-a", rel2.FromNodeID)
	assert.Equal(t, "gs-mvp-a", rel2.ToNodeID)

	// 6. 通过图查询验证关系（出边方向，深度 1）
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-mvp-a/graph?direction=out&depth=1", nil)
	require.Equal(t, 200, resp.StatusCode)
	var rels []*model.Relationship
	decodeJSON(t, resp, &rels)
	require.Len(t, rels, 2, "卫星应有两个出边关系")

	// 验证关系类型集合
	relTypes := map[model.RelationshipType]bool{}
	for _, r := range rels {
		relTypes[r.RelType] = true
	}
	assert.True(t, relTypes[model.RelBelongsTo], "应包含 belongsTo 关系")
	assert.True(t, relTypes[model.RelControlledBy], "应包含 controlledBy 关系")

	// 7. 验证 NodeRegistered 事件被发布（应收到 3 个：Community / Satellite / GroundStation）
	received := 0
	expected := 3
	for received < expected {
		e := waitForEvent(t, ch, event.EventNodeRegistered, 3*time.Second)
		require.NotNil(t, e)
		var payload event.NodeRegisteredPayload
		require.NoError(t, e.GetPayload(&payload))
		// 验证 payload 中的节点类型合法
		assert.NotEmpty(t, payload.NodeID)
		assert.NotEmpty(t, payload.NodeType)
		received++
	}
	assert.Equal(t, expected, received, "应收到 3 个 NodeRegistered 事件")
}

// TestMVP_ScenarioB_TelemetryPipeline 验证场景 B：遥测接收与事件发布。
//
// 流程：
//  1. 先注册一个 Satellite
//  2. 订阅 TelemetryReceived 事件
//  3. 通过 /api/v1/telemetry/send 发送 JSON 遥测数据
//  4. 验证 TelemetryReceived 事件被发布
//  5. 验证遥测参数包含正确数据
func TestMVP_ScenarioB_TelemetryPipeline(t *testing.T) {
	bundle := newMVPTestServer(t)
	server := bundle.server

	// 1. 注册 Satellite
	createNodeViaAPI(t, server, "sat-mvp-b", "Satellite", "遥测测试卫星", "active", "comm-mvp-b")

	// 2. 订阅 TelemetryReceived 事件
	ch, unsubscribe := bundle.bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	// 3. 通过 /api/v1/telemetry/send 发送 JSON 遥测数据
	telemetryJSON := `{"satelliteId":"sat-mvp-b","timestamp":"2026-07-01T00:00:00Z","parameters":{"temp":45.2,"voltage":28.1},"quality":"good"}`
	sendBody := encodeSendTelemetryBody(t, telemetryJSON)
	resp := doRequestRaw(t, server, "POST", "/api/v1/telemetry/send", sendBody)
	require.Equal(t, 202, resp.StatusCode, "遥测发送应返回 202 Accepted")
	resp.Body.Close()

	// 4. 验证 TelemetryReceived 事件被发布
	e := waitForEvent(t, ch, event.EventTelemetryReceived, 3*time.Second)
	require.NotNil(t, e)
	assert.Equal(t, "sat-mvp-b", e.SourceNodeID)

	// 5. 验证遥测参数包含正确数据
	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.Equal(t, "sat-mvp-b", payload.SatelliteID)
	assert.Equal(t, "good", payload.Quality)
	require.NotNil(t, payload.Parameters)
	assert.Equal(t, 45.2, payload.Parameters["temp"])
	assert.Equal(t, 28.1, payload.Parameters["voltage"])

	// 验证 logicalShard 分片信息
	shardAny, ok := e.Payload["logicalShard"]
	require.True(t, ok, "应包含 logicalShard 字段")
	shard, ok := shardAny.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sat-mvp-b", shard["satelliteId"])
}

// encodeSendTelemetryBody 构造 /api/v1/telemetry/send 的请求体 JSON。
func encodeSendTelemetryBody(t *testing.T, data string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"data": data})
	require.NoError(t, err)
	return string(b)
}

// TestMVP_ScenarioC_CommandPipeline 验证场景 C：指令下发闭环。
//
// 流程：
//  1. 先注册一个 Satellite
//  2. 订阅 CommandSent / CommandAcked 事件
//  3. 通过 /api/v1/commands 下发指令
//  4. 验证 CommandSent 事件被发布
//  5. 等待 Mock 适配器回 Ack
//  6. 验证 CommandAcked 事件被发布
//  7. 查询指令状态为 acked
func TestMVP_ScenarioC_CommandPipeline(t *testing.T) {
	bundle := newMVPTestServer(t)
	server := bundle.server

	// 1. 注册 Satellite
	createNodeViaAPI(t, server, "sat-mvp-c", "Satellite", "指令测试卫星", "active", "comm-mvp-c")

	// 2. 订阅 CommandSent / CommandAcked 事件
	ch, unsubscribe := bundle.bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventCommandSent, event.EventCommandAcked},
	})
	defer unsubscribe()

	// 3. 通过 /api/v1/commands 下发指令
	cmdBody := `{"satelliteId":"sat-mvp-c","commandType":"attitude","parameters":{"mode":"sun_pointing"},"priority":8}`
	resp := doRequestRaw(t, server, "POST", "/api/v1/commands", cmdBody)
	require.Equal(t, 202, resp.StatusCode, "指令下发应返回 202 Accepted")
	var cmdResp struct {
		CommandID string `json:"commandId"`
		Status    string `json:"status"`
	}
	decodeJSON(t, resp, &cmdResp)
	assert.Equal(t, "queued", cmdResp.Status)
	assert.NotEmpty(t, cmdResp.CommandID)
	commandID := cmdResp.CommandID

	// 4. 验证 CommandSent 事件被发布
	sentEvt := waitForEvent(t, ch, event.EventCommandSent, 3*time.Second)
	require.NotNil(t, sentEvt)
	assert.Equal(t, "sat-mvp-c", sentEvt.SourceNodeID)
	var sentPayload event.CommandSentPayload
	require.NoError(t, sentEvt.GetPayload(&sentPayload))
	assert.Equal(t, commandID, sentPayload.CommandID)
	assert.Equal(t, "sat-mvp-c", sentPayload.SatelliteID)
	assert.Equal(t, "attitude", sentPayload.CommandType)

	// 5. 等待 Mock 适配器回 Ack
	// 6. 验证 CommandAcked 事件被发布
	ackedEvt := waitForEvent(t, ch, event.EventCommandAcked, 3*time.Second)
	require.NotNil(t, ackedEvt)
	var ackedPayload event.CommandAckedPayload
	require.NoError(t, ackedEvt.GetPayload(&ackedPayload))
	assert.Equal(t, commandID, ackedPayload.CommandID)
	assert.True(t, ackedPayload.Success, "Mock 适配器应返回成功确认")

	// 7. 查询指令状态为 acked
	resp = doRequest(t, server, "GET", "/api/v1/commands/"+commandID, nil)
	require.Equal(t, 200, resp.StatusCode)
	var status struct {
		CommandID   string `json:"commandId"`
		Status      string `json:"status"`
		SatelliteID string `json:"satelliteId"`
		CommandType string `json:"commandType"`
	}
	decodeJSON(t, resp, &status)
	assert.Equal(t, commandID, status.CommandID)
	assert.Equal(t, "acked", status.Status, "指令最终状态应为 acked")
	assert.Equal(t, "sat-mvp-c", status.SatelliteID)
	assert.Equal(t, "attitude", status.CommandType)
}

// TestMVP_ScenarioD_PluginAndParser 验证场景 D：插件与解析器切换。
//
// 流程：
//  1. 查询可用解析器列表（应包含 json 和 tle）
//  2. 设置当前解析器为 tle
//  3. 发送 TLE 格式遥测数据（使用 ISS 的 TLE）
//  4. 验证 TelemetryReceived 事件被发布，且参数包含轨道根数
//  5. 切换回 json 解析器
//  6. 查询可用插件列表
func TestMVP_ScenarioD_PluginAndParser(t *testing.T) {
	bundle := newMVPTestServer(t)
	server := bundle.server

	// 1. 查询可用解析器列表（应包含 json 和 tle）
	resp := doRequest(t, server, "GET", "/api/v1/telemetry/parsers", nil)
	require.Equal(t, 200, resp.StatusCode)
	var parsers struct {
		Parsers   []string `json:"parsers"`
		Active    string   `json:"active"`
		Available bool     `json:"available"`
	}
	decodeJSON(t, resp, &parsers)
	assert.True(t, parsers.Available, "插件管理器应可用")
	assert.Contains(t, parsers.Parsers, "json")
	assert.Contains(t, parsers.Parsers, "tle")
	assert.Equal(t, "json", parsers.Active, "默认解析器应为 json")

	// 2. 设置当前解析器为 tle
	resp = doRequestRaw(t, server, "POST", "/api/v1/telemetry/parser", `{"name":"tle"}`)
	require.Equal(t, 200, resp.StatusCode)
	var setActive struct {
		Active string `json:"active"`
	}
	decodeJSON(t, resp, &setActive)
	assert.Equal(t, "tle", setActive.Active)

	// 3. 发送 TLE 格式遥测数据（使用 ISS 的 TLE）
	tleData := mvpTLELine1 + "\n" + mvpTLELine2
	sendBody := encodeSendTelemetryBody(t, tleData)

	// 订阅 TelemetryReceived 事件
	ch, unsubscribe := bundle.bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	resp = doRequestRaw(t, server, "POST", "/api/v1/telemetry/send", sendBody)
	require.Equal(t, 202, resp.StatusCode)
	resp.Body.Close()

	// 4. 验证 TelemetryReceived 事件被发布，且参数包含轨道根数
	e := waitForEvent(t, ch, event.EventTelemetryReceived, 3*time.Second)
	require.NotNil(t, e)
	assert.Equal(t, "25544", e.SourceNodeID, "TLE 解析后 sourceNodeId 应为 NORAD ID")

	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.Equal(t, "25544", payload.SatelliteID)

	// 验证 parameters 中包含 orbit 轨道根数
	orbitAny, ok := payload.Parameters["orbit"]
	require.True(t, ok, "应包含 orbit 字段")
	orbit, ok := orbitAny.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "25544", orbit["noradId"])
	assert.InDelta(t, 51.6416, toFloat64(orbit["inclination"]), 0.0001)
	assert.InDelta(t, 0.0006703, toFloat64(orbit["eccentricity"]), 0.0000001)
	assert.InDelta(t, 15.72125391, toFloat64(orbit["meanMotion"]), 0.0000001)

	// 5. 切换回 json 解析器
	resp = doRequestRaw(t, server, "POST", "/api/v1/telemetry/parser", `{"name":"json"}`)
	require.Equal(t, 200, resp.StatusCode)
	decodeJSON(t, resp, &setActive)
	assert.Equal(t, "json", setActive.Active)

	// 6. 查询可用插件列表
	resp = doRequest(t, server, "GET", "/api/v1/plugins", nil)
	require.Equal(t, 200, resp.StatusCode)
	var plugins []struct {
		Manifest struct {
			Name string `json:"name"`
		} `json:"manifest"`
		Status string `json:"status"`
	}
	decodeJSON(t, resp, &plugins)
	// 至少应有 json / tle / mock 三个内置插件
	assert.GreaterOrEqual(t, len(plugins), 3, "应至少注册 3 个内置插件")

	// 验证插件名称集合包含 json、tle、mock
	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Manifest.Name] = true
	}
	assert.True(t, names["json"], "应包含 json 解析器插件")
	assert.True(t, names["tle"], "应包含 tle 解析器插件")
	assert.True(t, names["mock"], "应包含 mock 适配器插件")
}

// toFloat64 将 interface{} 安全转换为 float64（JSON 反序列化后数字默认为 float64）。
func toFloat64(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// TestMVP_ScenarioE_Persistence 验证场景 E：持久化与恢复。
//
// 流程：
//  1. 使用临时文件 SQLite（非内存）
//  2. 注册节点和关系
//  3. 通过 /api/v1/events/replay 查询历史事件
//  4. 验证事件可回放
//  5. 验证 Node 数据持久化（重新查询仍存在）
//  6. 关闭服务后重新打开同一数据库，验证 Node 仍然存在
func TestMVP_ScenarioE_Persistence(t *testing.T) {
	// 1. 使用临时文件 SQLite（非内存）
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "aos-mvp-test.db")
	t.Cleanup(func() {
		_ = os.Remove(dbPath)
		// WAL 模式可能产生 -wal / -shm 文件，一并清理
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	// 2. 创建文件 SQLite 测试服务器
	bundle := newMVPTestServerWithDB(t, dbPath)
	server := bundle.server

	// 注册节点
	createNodeViaAPI(t, server, "sat-mvp-e", "Satellite", "持久化测试卫星", "active", "comm-mvp-e")
	createNodeViaAPI(t, server, "comm-mvp-e", "Community", "持久化测试社区", "active", "comm-mvp-e")
	createNodeViaAPI(t, server, "gs-mvp-e", "GroundStation", "持久化测试地面站", "active", "comm-mvp-e")

	// 建立关系
	resp := doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-mvp-e/relationships",
		`{"toNodeId":"comm-mvp-e","relType":"belongsTo"}`)
	require.Equal(t, 201, resp.StatusCode)
	resp.Body.Close()

	resp = doRequestRaw(t, server, "POST", "/api/v1/nodes/sat-mvp-e/relationships",
		`{"toNodeId":"gs-mvp-e","relType":"controlledBy"}`)
	require.Equal(t, 201, resp.StatusCode)
	resp.Body.Close()

	// 3. 通过 /api/v1/events/replay 查询历史事件
	resp = doRequestRaw(t, server, "POST", "/api/v1/events/replay", `{}`)
	require.Equal(t, 200, resp.StatusCode)
	var events []*event.Event
	decodeJSON(t, resp, &events)

	// 4. 验证事件可回放
	// 3 个 Node 注册，每个产生 NodeRegistered + StateUpdated = 6 个事件
	require.GreaterOrEqual(t, len(events), 6, "应至少回放 6 个事件（3 个 Node × 2 事件）")

	// 验证事件类型集合包含 NodeRegistered
	typeSet := map[event.EventType]bool{}
	for _, e := range events {
		typeSet[e.EventType] = true
	}
	assert.True(t, typeSet[event.EventNodeRegistered], "应包含 NodeRegistered 事件")
	assert.True(t, typeSet[event.EventStateUpdated], "应包含 StateUpdated 事件")

	// 5. 验证 Node 数据持久化（重新查询仍存在）
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-mvp-e", nil)
	require.Equal(t, 200, resp.StatusCode)
	var sat model.Node
	decodeJSON(t, resp, &sat)
	assert.Equal(t, "sat-mvp-e", sat.NodeID)
	assert.Equal(t, "持久化测试卫星", sat.Name)

	// 验证关系图仍然存在
	resp = doRequest(t, server, "GET", "/api/v1/nodes/sat-mvp-e/graph?direction=out&depth=1", nil)
	require.Equal(t, 200, resp.StatusCode)
	var rels []*model.Relationship
	decodeJSON(t, resp, &rels)
	require.Len(t, rels, 2, "卫星应仍有 2 个出边关系")

	// 6. 关闭服务后重新打开同一数据库，验证 Node 仍然存在（模拟重启）
	bundle.closeAll()

	// 重新打开同一数据库文件
	bundle2 := newMVPTestServerWithDB(t, dbPath)
	server2 := bundle2.server

	// 验证 Node 在重启后仍然存在
	resp = doRequest(t, server2, "GET", "/api/v1/nodes/sat-mvp-e", nil)
	require.Equal(t, 200, resp.StatusCode, "重启后卫星节点应仍存在")
	var sat2 model.Node
	decodeJSON(t, resp, &sat2)
	assert.Equal(t, "sat-mvp-e", sat2.NodeID)
	assert.Equal(t, "持久化测试卫星", sat2.Name)
	assert.Equal(t, model.NodeTypeSatellite, sat2.NodeType)

	// 验证 Community 在重启后仍然存在
	resp = doRequest(t, server2, "GET", "/api/v1/nodes/comm-mvp-e", nil)
	require.Equal(t, 200, resp.StatusCode)
	decodeJSON(t, resp, &sat2)
	assert.Equal(t, "comm-mvp-e", sat2.NodeID)

	// 验证关系在重启后仍然存在
	resp = doRequest(t, server2, "GET", "/api/v1/nodes/sat-mvp-e/graph?direction=out&depth=1", nil)
	require.Equal(t, 200, resp.StatusCode)
	decodeJSON(t, resp, &rels)
	require.Len(t, rels, 2, "重启后卫星应仍有 2 个出边关系")

	// 验证历史事件在重启后可通过 replay 查询（SQLite EventStore 持久化）
	resp = doRequestRaw(t, server2, "POST", "/api/v1/events/replay", `{"eventTypes":["NodeRegistered"]}`)
	require.Equal(t, 200, resp.StatusCode)
	decodeJSON(t, resp, &events)
	require.GreaterOrEqual(t, len(events), 3, "重启后应能回放至少 3 个 NodeRegistered 事件")
	for _, e := range events {
		assert.Equal(t, event.EventNodeRegistered, e.EventType)
	}
}

// TestMVP_ScenarioF_TLEParsing 验证场景 F：TLE 解析验证。
//
// 使用 ISS 的真实 TLE 数据，验证解析结果的关键参数：
//   - noradId = "25544"
//   - inclination ≈ 51.6416
//   - eccentricity ≈ 0.0006703
//   - meanMotion ≈ 15.72125391
//
// 同时验证通过 REST API 发送 TLE 数据后事件 payload 的轨道根数正确。
func TestMVP_ScenarioF_TLEParsing(t *testing.T) {
	bundle := newMVPTestServer(t)
	server := bundle.server

	// 直接验证 TLEParser 的解析结果
	parser := &telemetry.TLEParser{}

	// 三行格式（含名称行）
	raw := []byte(mvpTLEName + "\n" + mvpTLELine1 + "\n" + mvpTLELine2)
	frames, err := parser.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1, "应解析出 1 帧")

	frame := frames[0]

	// 验证关键字段
	assert.Equal(t, "25544", frame.SatelliteID, "NORAD ID 应为 25544")
	assert.Equal(t, "good", frame.Quality)

	// 验证 orbit 字段存在且为 OrbitElements 类型
	orbitAny, ok := frame.Parameters["orbit"]
	require.True(t, ok, "应包含 orbit 字段")
	orbit, ok := orbitAny.(model.OrbitElements)
	require.True(t, ok, "orbit 应为 model.OrbitElements 类型")

	// 验证关键轨道根数
	assert.Equal(t, "25544", orbit.NoradID)
	assert.InDelta(t, 51.6416, orbit.Inclination, 0.0001, "倾角应约为 51.6416")
	assert.InDelta(t, 247.4627, orbit.RAAN, 0.0001)
	assert.InDelta(t, 0.0006703, orbit.Eccentricity, 0.0000001, "偏心率应约为 0.0006703")
	assert.InDelta(t, 130.5360, orbit.ArgPerigee, 0.0001)
	assert.InDelta(t, 325.0288, orbit.MeanAnomaly, 0.0001)
	assert.InDelta(t, 15.72125391, orbit.MeanMotion, 0.0000001, "平均运动应约为 15.72125391")

	// 验证派生参数
	assert.InDelta(t, 1440.0/15.72125391, orbit.Period, 0.01, "周期应约为 91.6 分钟")
	assert.Greater(t, orbit.SemiMajorAxis, 6700.0, "半长轴应约 6730 km")
	assert.Less(t, orbit.SemiMajorAxis, 6800.0)

	// 验证历元：2008 年第 264 天 ≈ 2008-09-20
	assert.Equal(t, 2008, orbit.Epoch.Year())
	assert.Equal(t, time.September, orbit.Epoch.Month())
	assert.Equal(t, 20, orbit.Epoch.Day())

	// 端到端验证：通过 REST API 发送 TLE 数据，验证事件 payload
	// 先切换解析器为 tle
	resp := doRequestRaw(t, server, "POST", "/api/v1/telemetry/parser", `{"name":"tle"}`)
	require.Equal(t, 200, resp.StatusCode)
	resp.Body.Close()

	// 订阅 TelemetryReceived 事件
	ch, unsubscribe := bundle.bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	// 发送 TLE 数据
	tleData := mvpTLELine1 + "\n" + mvpTLELine2
	sendBody := encodeSendTelemetryBody(t, tleData)
	resp = doRequestRaw(t, server, "POST", "/api/v1/telemetry/send", sendBody)
	require.Equal(t, 202, resp.StatusCode)
	resp.Body.Close()

	// 验证事件
	e := waitForEvent(t, ch, event.EventTelemetryReceived, 3*time.Second)
	require.NotNil(t, e)
	assert.Equal(t, "25544", e.SourceNodeID)

	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.Equal(t, "25544", payload.SatelliteID)

	// 验证事件 payload 中的轨道根数（经 JSON 往返后为 map[string]any）
	orbitMap, ok := payload.Parameters["orbit"].(map[string]any)
	require.True(t, ok, "事件 payload 中 orbit 应为 map")
	assert.Equal(t, "25544", orbitMap["noradId"])
	assert.InDelta(t, 51.6416, toFloat64(orbitMap["inclination"]), 0.0001)
	assert.InDelta(t, 0.0006703, toFloat64(orbitMap["eccentricity"]), 0.0000001)
	assert.InDelta(t, 15.72125391, toFloat64(orbitMap["meanMotion"]), 0.0000001)
}
