package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// ---- Mock 实现 ----

type mockParser struct {
	name string
}

func (m *mockParser) Name() string { return m.name }
func (m *mockParser) Parse(raw []byte) ([]TelemetryFrame, error) {
	return []TelemetryFrame{{SatelliteID: "sat-mock", Quality: "good"}}, nil
}

type mockAdapter struct {
	name string
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Send(cmd Command) (Ack, error) {
	return Ack{Success: true, Message: "ok"}, nil
}

type mockSubscriber struct {
	name  string
	calls int
}

func (m *mockSubscriber) Name() string { return m.name }
func (m *mockSubscriber) HandleEvent(e *event.Event) error {
	m.calls++
	return nil
}

type mockHook struct {
	name       string
	registered []*model.Node
	updated    []*model.Node
	deleted    []string
}

func (m *mockHook) Name() string { return m.name }
func (m *mockHook) OnNodeRegistered(node *model.Node) error {
	m.registered = append(m.registered, node)
	return nil
}
func (m *mockHook) OnNodeUpdated(node *model.Node, changes map[string]any) error {
	m.updated = append(m.updated, node)
	return nil
}
func (m *mockHook) OnNodeDeleted(nodeID string) error {
	m.deleted = append(m.deleted, nodeID)
	return nil
}

// ---- 测试辅助 ----

// newTestBroker 创建用于测试的 Broker，使用内存 SQLite 和 MemoryEventStore。
func newTestBroker(t *testing.T) (*Broker, *core.KGService, core.MessageBus) {
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
	broker := NewBroker(bus, kg, nil)
	return broker, kg, bus
}

// ---- 注册与获取测试 ----

// TestRegisterAndGetParser 测试注册和获取 TelemetryParser。
func TestRegisterAndGetParser(t *testing.T) {
	mgr := NewManager(nil, nil)
	p := &mockParser{name: "test-parser"}

	mgr.RegisterParser("test-parser", p)

	got, ok := mgr.GetParser("test-parser")
	require.True(t, ok)
	assert.Equal(t, "test-parser", got.Name())

	// 未注册的解析器
	_, ok = mgr.GetParser("nonexistent")
	assert.False(t, ok)

	// ListParsers 应包含已注册的解析器
	parsers := mgr.ListParsers()
	assert.Contains(t, parsers, "test-parser")
}

// TestRegisterAndGetAdapter 测试注册和获取 CommandAdapter。
func TestRegisterAndGetAdapter(t *testing.T) {
	mgr := NewManager(nil, nil)
	a := &mockAdapter{name: "test-adapter"}

	mgr.RegisterAdapter("test-adapter", a)

	got, ok := mgr.GetAdapter("test-adapter")
	require.True(t, ok)
	assert.Equal(t, "test-adapter", got.Name())

	// 测试 Send
	ack, err := got.Send(Command{CommandID: "cmd-1"})
	require.NoError(t, err)
	assert.True(t, ack.Success)

	// 未注册的适配器
	_, ok = mgr.GetAdapter("nonexistent")
	assert.False(t, ok)

	// ListAdapters 应包含已注册的适配器
	adapters := mgr.ListAdapters()
	assert.Contains(t, adapters, "test-adapter")
}

// TestRegisterAndGetSubscriber 测试注册和获取 EventSubscriber。
func TestRegisterAndGetSubscriber(t *testing.T) {
	mgr := NewManager(nil, nil)
	s := &mockSubscriber{name: "test-subscriber"}

	mgr.RegisterSubscriber("test-subscriber", s)

	// 验证实例已创建
	plugins := mgr.ListPlugins()
	require.Len(t, plugins, 1)
	assert.Equal(t, "test-subscriber", plugins[0].Manifest.Name)
	assert.Equal(t, StatusLoaded, plugins[0].Status)
}

// TestRegisterAndGetHook 测试注册和获取 NodeLifecycleHook。
func TestRegisterAndGetHook(t *testing.T) {
	mgr := NewManager(nil, nil)
	h := &mockHook{name: "test-hook"}

	mgr.RegisterHook("test-hook", h)

	// 验证实例已创建
	plugins := mgr.ListPlugins()
	require.Len(t, plugins, 1)
	assert.Equal(t, "test-hook", plugins[0].Manifest.Name)
}

// ---- 生命周期钩子触发测试 ----

// TestNodeLifecycleHookTrigger 测试 NodeLifecycleHook 的三种触发回调。
func TestNodeLifecycleHookTrigger(t *testing.T) {
	mgr := NewManager(nil, nil)
	h := &mockHook{name: "hook-1"}
	mgr.RegisterHook("hook-1", h)

	node := &model.Node{
		NodeID:           "sat-trigger-1",
		NodeType:         model.NodeTypeSatellite,
		Name:             "测试卫星",
		Status:           "active",
		OwnerCommunityID: "comm-1",
	}
	changes := map[string]any{"status": "standby"}

	// 触发 OnNodeRegistered
	mgr.TriggerNodeRegistered(node)
	require.Len(t, h.registered, 1)
	assert.Equal(t, "sat-trigger-1", h.registered[0].NodeID)

	// 触发 OnNodeUpdated
	mgr.TriggerNodeUpdated(node, changes)
	require.Len(t, h.updated, 1)
	assert.Equal(t, "sat-trigger-1", h.updated[0].NodeID)

	// 触发 OnNodeDeleted
	mgr.TriggerNodeDeleted("sat-trigger-1")
	require.Len(t, h.deleted, 1)
	assert.Equal(t, "sat-trigger-1", h.deleted[0])
}

// TestMultipleHooksTrigger 测试多个钩子同时被触发。
func TestMultipleHooksTrigger(t *testing.T) {
	mgr := NewManager(nil, nil)
	h1 := &mockHook{name: "hook-a"}
	h2 := &mockHook{name: "hook-b"}
	mgr.RegisterHook("hook-a", h1)
	mgr.RegisterHook("hook-b", h2)

	node := &model.Node{
		NodeID:           "sat-multi-1",
		NodeType:         model.NodeTypeSatellite,
		Name:             "测试卫星",
		Status:           "active",
		OwnerCommunityID: "comm-1",
	}

	mgr.TriggerNodeRegistered(node)
	assert.Len(t, h1.registered, 1)
	assert.Len(t, h2.registered, 1)

	mgr.TriggerNodeDeleted("sat-multi-1")
	assert.Len(t, h1.deleted, 1)
	assert.Len(t, h2.deleted, 1)
}

// ---- ListPlugins 测试 ----

// TestListPlugins 测试 ListPlugins 返回正确的插件状态。
func TestListPlugins(t *testing.T) {
	mgr := NewManager(nil, nil)

	// 注册多个插件
	mgr.RegisterParser("parser-a", &mockParser{name: "parser-a"})
	mgr.RegisterAdapter("adapter-b", &mockAdapter{name: "adapter-b"})
	mgr.RegisterHook("hook-c", &mockHook{name: "hook-c"})

	plugins := mgr.ListPlugins()
	require.Len(t, plugins, 3)

	// 验证按名称排序
	assert.Equal(t, "adapter-b", plugins[0].Manifest.Name)
	assert.Equal(t, "hook-c", plugins[1].Manifest.Name)
	assert.Equal(t, "parser-a", plugins[2].Manifest.Name)

	// 验证状态
	for _, p := range plugins {
		assert.Equal(t, StatusLoaded, p.Status)
	}

	// 启动所有插件
	mgr.StartAll()
	plugins = mgr.ListPlugins()
	for _, p := range plugins {
		assert.Equal(t, StatusStarted, p.Status)
	}

	// 停止所有插件
	mgr.StopAll()
	plugins = mgr.ListPlugins()
	for _, p := range plugins {
		assert.Equal(t, StatusStopped, p.Status)
	}
}

// TestListPluginsReturnsClone 测试 ListPlugins 返回的是快照副本，修改不影响原始数据。
func TestListPluginsReturnsClone(t *testing.T) {
	mgr := NewManager(nil, nil)
	mgr.RegisterParser("test-clone", &mockParser{name: "test-clone"})

	plugins := mgr.ListPlugins()
	plugins[0].Status = "modified"

	// 原始数据不受影响
	plugins2 := mgr.ListPlugins()
	assert.Equal(t, StatusLoaded, plugins2[0].Status)
}

// ---- Unload 测试 ----

// TestUnload 测试卸载后插件不再可用。
func TestUnload(t *testing.T) {
	mgr := NewManager(nil, nil)
	mgr.RegisterParser("unload-test", &mockParser{name: "unload-test"})
	mgr.RegisterHook("unload-test", &mockHook{name: "unload-test"})

	// 卸载前可用
	_, ok := mgr.GetParser("unload-test")
	assert.True(t, ok)
	assert.Contains(t, mgr.ListParsers(), "unload-test")

	// 卸载
	err := mgr.Unload("unload-test")
	require.NoError(t, err)

	// 卸载后不再可用
	_, ok = mgr.GetParser("unload-test")
	assert.False(t, ok)
	assert.NotContains(t, mgr.ListParsers(), "unload-test")

	// 插件实例列表中不再包含
	plugins := mgr.ListPlugins()
	for _, p := range plugins {
		assert.NotEqual(t, "unload-test", p.Manifest.Name)
	}

	// 重复卸载返回错误
	err = mgr.Unload("unload-test")
	assert.Error(t, err)
}

// ---- LoadFromDirectory 测试 ----

// TestLoadFromDirectory 测试从目录加载插件清单。
func TestLoadFromDirectory(t *testing.T) {
	// 创建临时目录结构
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "telemetry-dummy")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))

	// 写入 plugin.yaml
	yamlContent := `name: telemetry-dummy
version: 1.0.0
description: 示例遥测解析插件
author: Openspace OS Team
extensionPoints:
  - TelemetryParser
permissions:
  - event.publish
  - kg.query
dependencies:
  openspace-os-core: ">=1.0.0"
entryPoint: ./telemetry-dummy
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(yamlContent), 0644))

	// 写入另一个 JSON 清单
	pluginDir2 := filepath.Join(tmpDir, "command-adapter")
	require.NoError(t, os.MkdirAll(pluginDir2, 0755))
	jsonContent := `{
		"name": "command-adapter",
		"version": "2.1.0",
		"description": "指令适配插件",
		"author": "Openspace OS Team",
		"extensionPoints": ["CommandAdapter"],
		"permissions": ["event.publish"],
		"entryPoint": "./command-adapter"
	}`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir2, "manifest.json"), []byte(jsonContent), 0644))

	// 加载
	mgr := NewManager(nil, nil)
	err := mgr.LoadFromDirectory(tmpDir)
	require.NoError(t, err)

	plugins := mgr.ListPlugins()
	require.Len(t, plugins, 2)

	// 验证 YAML 清单
	var yamlPlugin *PluginInstance
	var jsonPlugin *PluginInstance
	for _, p := range plugins {
		switch p.Manifest.Name {
		case "telemetry-dummy":
			yamlPlugin = p
		case "command-adapter":
			jsonPlugin = p
		}
	}
	require.NotNil(t, yamlPlugin)
	require.NotNil(t, jsonPlugin)

	// 验证 YAML 解析结果
	assert.Equal(t, "1.0.0", yamlPlugin.Manifest.Version)
	assert.Equal(t, "示例遥测解析插件", yamlPlugin.Manifest.Description)
	assert.Equal(t, "Openspace OS Team", yamlPlugin.Manifest.Author)
	assert.Equal(t, []string{"TelemetryParser"}, yamlPlugin.Manifest.ExtensionPoints)
	assert.Equal(t, []string{"event.publish", "kg.query"}, yamlPlugin.Manifest.Permissions)
	assert.Equal(t, ">=1.0.0", yamlPlugin.Manifest.Dependencies["openspace-os-core"])
	assert.Equal(t, "./telemetry-dummy", yamlPlugin.Manifest.EntryPoint)
	assert.Equal(t, StatusLoaded, yamlPlugin.Status)

	// 验证 JSON 解析结果
	assert.Equal(t, "2.1.0", jsonPlugin.Manifest.Version)
	assert.Equal(t, "指令适配插件", jsonPlugin.Manifest.Description)
	assert.Equal(t, []string{"CommandAdapter"}, jsonPlugin.Manifest.ExtensionPoints)
}

// TestLoadFromDirectoryInvalidManifest 测试加载无效清单时标记为 error 状态。
func TestLoadFromDirectoryInvalidManifest(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "bad-plugin")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))

	// 缺少必填字段 name 的清单
	invalidYaml := `version: 1.0.0
entryPoint: ./bad
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(invalidYaml), 0644))

	mgr := NewManager(nil, nil)
	err := mgr.LoadFromDirectory(tmpDir)
	// LoadFromDirectory 本身不返回错误（单个清单失败只记录状态）
	require.NoError(t, err)

	plugins := mgr.ListPlugins()
	require.Len(t, plugins, 1)
	assert.Equal(t, StatusError, plugins[0].Status)
	assert.NotEmpty(t, plugins[0].Error)
}

// TestLoadFromDirectoryNonexistent 测试加载不存在的目录返回错误。
func TestLoadFromDirectoryNonexistent(t *testing.T) {
	mgr := NewManager(nil, nil)
	err := mgr.LoadFromDirectory("/nonexistent/path/xyz")
	assert.Error(t, err)
}

// ---- Broker 测试 ----

// TestBrokerPublishEvent 测试 Broker.PublishEvent 正常工作。
func TestBrokerPublishEvent(t *testing.T) {
	broker, _, bus := newTestBroker(t)

	// 先订阅事件
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	// 构造合法的 TelemetryReceived 事件
	e := &event.Event{
		EventID:      uuid.NewString(),
		EventType:    event.EventTelemetryReceived,
		Timestamp:    time.Now(),
		SourceNodeID: "sat-broker-1",
	}
	require.NoError(t, e.SetPayload(event.TelemetryReceivedPayload{
		SatelliteID: "sat-broker-1",
		Timestamp:   time.Now(),
		Parameters:  map[string]any{"temp": 45.2},
		Quality:     "good",
	}))

	// 通过 Broker 发布事件
	err := broker.PublishEvent(context.Background(), e)
	require.NoError(t, err)

	// 验证订阅者收到事件
	select {
	case received := <-ch:
		assert.Equal(t, event.EventTelemetryReceived, received.EventType)
		assert.Equal(t, "sat-broker-1", received.SourceNodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("等待事件超时")
	}
}

// TestBrokerPublishEventNil 测试 Broker.PublishEvent 传入 nil 返回错误。
func TestBrokerPublishEventNil(t *testing.T) {
	broker, _, _ := newTestBroker(t)
	err := broker.PublishEvent(context.Background(), nil)
	assert.Error(t, err)
}

// TestBrokerQueryNode 测试 Broker.QueryNode 正常工作。
func TestBrokerQueryNode(t *testing.T) {
	broker, kg, _ := newTestBroker(t)

	// 通过 KGService 注册节点
	node := &model.Node{
		NodeID:           "sat-query-1",
		NodeType:         model.NodeTypeSatellite,
		Name:             "查询测试卫星",
		Status:           "active",
		OwnerCommunityID: "comm-1",
	}
	require.NoError(t, kg.RegisterNode(context.Background(), node))

	// 通过 Broker 查询节点
	got, err := broker.QueryNode(context.Background(), "sat-query-1")
	require.NoError(t, err)
	assert.Equal(t, "sat-query-1", got.NodeID)
	assert.Equal(t, model.NodeTypeSatellite, got.NodeType)
	assert.Equal(t, "查询测试卫星", got.Name)

	// 查询不存在的节点
	_, err = broker.QueryNode(context.Background(), "nonexistent")
	assert.Error(t, err)
}

// TestBrokerQueryNodeEmptyID 测试 Broker.QueryNode 空ID返回错误。
func TestBrokerQueryNodeEmptyID(t *testing.T) {
	broker, _, _ := newTestBroker(t)
	_, err := broker.QueryNode(context.Background(), "")
	assert.Error(t, err)
}

// TestBrokerListNodes 测试 Broker.ListNodes 正常工作。
func TestBrokerListNodes(t *testing.T) {
	broker, kg, _ := newTestBroker(t)

	// 注册两个节点
	require.NoError(t, kg.RegisterNode(context.Background(), &model.Node{
		NodeID: "sat-list-1", NodeType: model.NodeTypeSatellite,
		Name: "卫星1", Status: "active", OwnerCommunityID: "comm-1",
	}))
	require.NoError(t, kg.RegisterNode(context.Background(), &model.Node{
		NodeID: "sat-list-2", NodeType: model.NodeTypeSatellite,
		Name: "卫星2", Status: "standby", OwnerCommunityID: "comm-1",
	}))

	// 通过 Broker 列出节点
	nodes, err := broker.ListNodes(context.Background(), core.ListOptions{
		NodeType: model.NodeTypeSatellite,
	})
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
}

// TestBrokerSubscribeEvents 测试 Broker.SubscribeEvents 正常工作。
func TestBrokerSubscribeEvents(t *testing.T) {
	broker, kg, _ := newTestBroker(t)

	// 通过 Broker 订阅 NodeRegistered 事件
	ch, unsubscribe := broker.SubscribeEvents(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	defer unsubscribe()

	// 注册节点触发事件
	require.NoError(t, kg.RegisterNode(context.Background(), &model.Node{
		NodeID: "sat-sub-1", NodeType: model.NodeTypeSatellite,
		Name: "订阅测试卫星", Status: "active", OwnerCommunityID: "comm-1",
	}))

	// 验证收到事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventNodeRegistered, e.EventType)
		assert.Equal(t, "sat-sub-1", e.SourceNodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("等待事件超时")
	}
}

// ---- LoadGlobals 测试 ----

// TestLoadGlobals 测试从全局注册表导入插件。
func TestLoadGlobals(t *testing.T) {
	// 向全局注册表注册一个临时插件
	RegisterParser("global-test-parser", &mockParser{name: "global-test-parser"})
	defer func() {
		globalRegistry.mu.Lock()
		delete(globalRegistry.parsers, "global-test-parser")
		globalRegistry.mu.Unlock()
	}()

	mgr := NewManager(nil, nil)
	mgr.LoadGlobals()

	// 验证全局注册的插件已导入
	p, ok := mgr.GetParser("global-test-parser")
	require.True(t, ok)
	assert.Equal(t, "global-test-parser", p.Name())
}

// ---- Manifest 加载测试 ----

// TestLoadManifestYAML 测试加载 YAML 格式清单。
func TestLoadManifestYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "plugin.yaml")
	content := `name: test-plugin
version: 1.2.3
description: 测试插件
author: tester
entryPoint: ./test
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	m, err := LoadManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "test-plugin", m.Name)
	assert.Equal(t, "1.2.3", m.Version)
	assert.Equal(t, "测试插件", m.Description)
	assert.Equal(t, "tester", m.Author)
	assert.Equal(t, "./test", m.EntryPoint)
}

// TestLoadManifestJSON 测试加载 JSON 格式清单。
func TestLoadManifestJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "manifest.json")
	content := `{
		"name": "json-plugin",
		"version": "0.1.0",
		"entryPoint": "./json-plugin"
	}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	m, err := LoadManifest(path)
	require.NoError(t, err)
	assert.Equal(t, "json-plugin", m.Name)
	assert.Equal(t, "0.1.0", m.Version)
}

// TestLoadManifestInvalid 测试加载缺少必填字段的清单返回错误。
func TestLoadManifestInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "plugin.yaml")
	content := `version: 1.0.0
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, err := LoadManifest(path)
	assert.Error(t, err)
}

// TestLoadManifestUnsupportedFormat 测试不支持的文件格式返回错误。
func TestLoadManifestUnsupportedFormat(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "plugin.txt")
	require.NoError(t, os.WriteFile(path, []byte("name: test"), 0644))

	_, err := LoadManifest(path)
	assert.Error(t, err)
}
