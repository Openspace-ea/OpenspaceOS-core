package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// 插件状态常量。
const (
	StatusLoaded  = "loaded"  // 已加载但未启动
	StatusStarted = "started" // 已启动
	StatusStopped = "stopped" // 已停止
	StatusError   = "error"   // 加载或运行出错
)

// PluginInstance 表示一个已加载的插件实例及其运行状态。
type PluginInstance struct {
	// Manifest 插件清单信息。
	Manifest Manifest
	// Status 当前状态：loaded / started / stopped / error。
	Status string
	// Error 出错时的错误信息。
	Error string
}

// Manager 管理插件的注册、查询与生命周期。
//
// MVP 阶段采用内置注册方案：插件通过 Register* 方法注册到 Manager 实例，
// 或通过 init() 注册到全局注册表后由 LoadGlobals 批量导入。
// 插件隔离通过 Broker 限制 API 访问实现，而非进程隔离。
type Manager struct {
	broker   *Broker
	logger   *slog.Logger
	plugins  map[string]*PluginInstance
	parsers  map[string]TelemetryParser
	adapters map[string]CommandAdapter
	subs     map[string]EventSubscriber
	hooks    map[string]NodeLifecycleHook
	mu       sync.RWMutex
}

// NewManager 创建插件管理器。
//
// logger 为 nil 时使用 slog.Default()。
func NewManager(broker *Broker, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		broker:   broker,
		logger:   logger,
		plugins:  make(map[string]*PluginInstance),
		parsers:  make(map[string]TelemetryParser),
		adapters: make(map[string]CommandAdapter),
		subs:     make(map[string]EventSubscriber),
		hooks:    make(map[string]NodeLifecycleHook),
	}
}

// RegisterParser 注册遥测解析器。
func (m *Manager) RegisterParser(name string, parser TelemetryParser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parsers[name] = parser
	m.ensureInstance(name)
	m.logger.Info("已注册遥测解析器", "name", name)
}

// RegisterAdapter 注册指令适配器。
func (m *Manager) RegisterAdapter(name string, adapter CommandAdapter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[name] = adapter
	m.ensureInstance(name)
	m.logger.Info("已注册指令适配器", "name", name)
}

// RegisterSubscriber 注册事件订阅者。
func (m *Manager) RegisterSubscriber(name string, sub EventSubscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[name] = sub
	m.ensureInstance(name)
	m.logger.Info("已注册事件订阅者", "name", name)
}

// RegisterHook 注册 Node 生命周期钩子。
func (m *Manager) RegisterHook(name string, hook NodeLifecycleHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[name] = hook
	m.ensureInstance(name)
	m.logger.Info("已注册生命周期钩子", "name", name)
}

// ensureInstance 确保指定名称的插件实例存在（调用方需持写锁）。
func (m *Manager) ensureInstance(name string) {
	if _, ok := m.plugins[name]; !ok {
		m.plugins[name] = &PluginInstance{
			Manifest: Manifest{Name: name},
			Status:   StatusLoaded,
		}
	}
}

// GetParser 获取指定名称的遥测解析器。
func (m *Manager) GetParser(name string) (TelemetryParser, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.parsers[name]
	return p, ok
}

// GetAdapter 获取指定名称的指令适配器。
func (m *Manager) GetAdapter(name string) (CommandAdapter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.adapters[name]
	return a, ok
}

// ListParsers 返回所有已注册的解析器名称（按字母序排列）。
func (m *Manager) ListParsers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.parsers))
	for name := range m.parsers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListAdapters 返回所有已注册的适配器名称（按字母序排列）。
func (m *Manager) ListAdapters() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.adapters))
	for name := range m.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListPlugins 返回所有已注册插件实例的快照（按名称排列）。
func (m *Manager) ListPlugins() []*PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PluginInstance, 0, len(m.plugins))
	for _, p := range m.plugins {
		clone := *p
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.Name < out[j].Manifest.Name
	})
	return out
}

// LoadFromDirectory 从目录加载插件清单。
//
// 扫描目录及其一级子目录，查找 plugin.yaml / plugin.yml / manifest.json 文件。
// MVP 阶段仅解析清单元数据，实际扩展点注册需通过 Register* 方法完成。
func (m *Manager) LoadFromDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读取插件目录失败: %w", err)
	}

	for _, entry := range entries {
		var manifestPath, fallbackName string
		if entry.IsDir() {
			// 在子目录中查找清单文件
			subDir := filepath.Join(dir, entry.Name())
			manifestPath = findManifestInDir(subDir)
			fallbackName = entry.Name()
		} else if isManifestFile(entry.Name()) {
			manifestPath = filepath.Join(dir, entry.Name())
			fallbackName = stripExt(entry.Name())
		} else {
			continue
		}
		if manifestPath == "" {
			continue
		}

		manifest, err := LoadManifest(manifestPath)
		if err != nil {
			m.logger.Error("加载插件清单失败", "path", manifestPath, "error", err)
			m.mu.Lock()
			m.plugins[fallbackName] = &PluginInstance{
				Manifest: Manifest{Name: fallbackName},
				Status:   StatusError,
				Error:    err.Error(),
			}
			m.mu.Unlock()
			continue
		}

		m.mu.Lock()
		m.plugins[manifest.Name] = &PluginInstance{
			Manifest: *manifest,
			Status:   StatusLoaded,
		}
		m.mu.Unlock()
		m.logger.Info("已加载插件清单", "name", manifest.Name, "version", manifest.Version)
	}
	return nil
}

// isManifestFile 判断文件名是否为支持的清单文件。
func isManifestFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == "plugin.yaml" || lower == "plugin.yml" || lower == "manifest.json"
}

// findManifestInDir 在指定目录中查找清单文件，返回找到的第一个路径。
func findManifestInDir(dir string) string {
	for _, name := range []string{"plugin.yaml", "plugin.yml", "manifest.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// stripExt 去除文件扩展名。
func stripExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// Unload 卸载指定名称的插件。
//
// 从所有扩展点注册表中移除该插件，并删除其实例记录。
func (m *Manager) Unload(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.plugins[name]; !ok {
		return fmt.Errorf("插件 %s 不存在", name)
	}
	delete(m.parsers, name)
	delete(m.adapters, name)
	delete(m.subs, name)
	delete(m.hooks, name)
	delete(m.plugins, name)
	m.logger.Info("已卸载插件", "name", name)
	return nil
}

// StartAll 将所有插件状态置为 started。
func (m *Manager) StartAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.plugins {
		p.Status = StatusStarted
	}
	m.logger.Info("已启动所有插件", "count", len(m.plugins))
}

// StopAll 将所有插件状态置为 stopped。
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.plugins {
		p.Status = StatusStopped
	}
	m.logger.Info("已停止所有插件", "count", len(m.plugins))
}

// TriggerNodeRegistered 触发所有已注册钩子的 OnNodeRegistered 回调。
func (m *Manager) TriggerNodeRegistered(node *model.Node) {
	hooks := m.snapshotHooks()
	for _, h := range hooks {
		if err := h.OnNodeRegistered(node); err != nil {
			m.logger.Error("OnNodeRegistered 钩子执行失败", "name", h.Name(), "error", err)
		}
	}
}

// TriggerNodeUpdated 触发所有已注册钩子的 OnNodeUpdated 回调。
func (m *Manager) TriggerNodeUpdated(node *model.Node, changes map[string]any) {
	hooks := m.snapshotHooks()
	for _, h := range hooks {
		if err := h.OnNodeUpdated(node, changes); err != nil {
			m.logger.Error("OnNodeUpdated 钩子执行失败", "name", h.Name(), "error", err)
		}
	}
}

// TriggerNodeDeleted 触发所有已注册钩子的 OnNodeDeleted 回调。
func (m *Manager) TriggerNodeDeleted(nodeID string) {
	hooks := m.snapshotHooks()
	for _, h := range hooks {
		if err := h.OnNodeDeleted(nodeID); err != nil {
			m.logger.Error("OnNodeDeleted 钩子执行失败", "name", h.Name(), "error", err)
		}
	}
}

// snapshotHooks 获取当前所有钩子的快照（避免回调中持锁导致死锁）。
func (m *Manager) snapshotHooks() []NodeLifecycleHook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	hooks := make([]NodeLifecycleHook, 0, len(m.hooks))
	for _, h := range m.hooks {
		hooks = append(hooks, h)
	}
	return hooks
}

// ---- 全局注册表 ----
// 供插件通过 init() 自动注册，main.go 通过 blank import 触发 init()，
// 再调用 Manager.LoadGlobals() 将全局注册的插件导入到 Manager 实例。

// globalRegistry 全局插件注册表。
var globalRegistry = struct {
	mu       sync.Mutex
	parsers  map[string]TelemetryParser
	adapters map[string]CommandAdapter
	subs     map[string]EventSubscriber
	hooks    map[string]NodeLifecycleHook
}{
	parsers:  make(map[string]TelemetryParser),
	adapters: make(map[string]CommandAdapter),
	subs:     make(map[string]EventSubscriber),
	hooks:    make(map[string]NodeLifecycleHook),
}

// RegisterParser 向全局注册表注册遥测解析器（通常在插件 init() 中调用）。
func RegisterParser(name string, parser TelemetryParser) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.parsers[name] = parser
}

// RegisterAdapter 向全局注册表注册指令适配器（通常在插件 init() 中调用）。
func RegisterAdapter(name string, adapter CommandAdapter) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.adapters[name] = adapter
}

// RegisterSubscriber 向全局注册表注册事件订阅者（通常在插件 init() 中调用）。
func RegisterSubscriber(name string, sub EventSubscriber) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.subs[name] = sub
}

// RegisterHook 向全局注册表注册生命周期钩子（通常在插件 init() 中调用）。
func RegisterHook(name string, hook NodeLifecycleHook) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.hooks[name] = hook
}

// LoadGlobals 将全局注册表中所有插件导入到当前 Manager 实例。
//
// 通常在 main.go 中创建 Manager 后调用，以加载通过 init() 自动注册的内置插件。
func (m *Manager) LoadGlobals() {
	globalRegistry.mu.Lock()
	// 复制快照后释放锁，避免 Register* 方法与全局锁产生死锁
	parsers := make(map[string]TelemetryParser, len(globalRegistry.parsers))
	for k, v := range globalRegistry.parsers {
		parsers[k] = v
	}
	adapters := make(map[string]CommandAdapter, len(globalRegistry.adapters))
	for k, v := range globalRegistry.adapters {
		adapters[k] = v
	}
	subs := make(map[string]EventSubscriber, len(globalRegistry.subs))
	for k, v := range globalRegistry.subs {
		subs[k] = v
	}
	hooks := make(map[string]NodeLifecycleHook, len(globalRegistry.hooks))
	for k, v := range globalRegistry.hooks {
		hooks[k] = v
	}
	globalRegistry.mu.Unlock()

	for name, p := range parsers {
		m.RegisterParser(name, p)
	}
	for name, a := range adapters {
		m.RegisterAdapter(name, a)
	}
	for name, s := range subs {
		m.RegisterSubscriber(name, s)
	}
	for name, h := range hooks {
		m.RegisterHook(name, h)
	}
}
