package plugin

import (
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// TelemetryParser 遥测解析扩展点。
//
// 插件实现此接口以解析特定格式的遥测原始数据，
// 将其转换为统一的 TelemetryFrame 结构。
type TelemetryParser interface {
	// Name 返回解析器名称。
	Name() string
	// Parse 将原始遥测数据解析为遥测帧列表。
	Parse(raw []byte) ([]TelemetryFrame, error)
}

// TelemetryFrame 是解析后的标准遥测数据帧。
type TelemetryFrame struct {
	// SatelliteID 卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// Timestamp 遥测时间戳。
	Timestamp time.Time `json:"timestamp"`
	// Parameters 遥测参数键值对。
	Parameters map[string]any `json:"parameters"`
	// Quality 遥测质量标识。
	Quality string `json:"quality"`
}

// CommandAdapter 遥控指令适配扩展点。
//
// 插件实现此接口以将统一的指令结构适配发送到特定目标系统。
type CommandAdapter interface {
	// Name 返回适配器名称。
	Name() string
	// Send 发送一条遥控指令，返回确认响应。
	Send(cmd Command) (Ack, error)
}

// Command 是待发送的遥控指令。
type Command struct {
	// CommandID 指令唯一标识。
	CommandID string `json:"commandId"`
	// SatelliteID 目标卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// CommandType 指令类型。
	CommandType string `json:"commandType"`
	// Parameters 指令参数键值对。
	Parameters map[string]any `json:"parameters"`
}

// Ack 是指令发送的确认响应。
type Ack struct {
	// Success 是否成功。
	Success bool `json:"success"`
	// Message 确认信息。
	Message string `json:"message"`
}

// EventSubscriber 事件订阅扩展点。
//
// 插件实现此接口以处理事件总线上的事件。
type EventSubscriber interface {
	// Name 返回订阅者名称。
	Name() string
	// HandleEvent 处理一个事件。
	HandleEvent(event *event.Event) error
}

// NodeLifecycleHook Node 生命周期钩子扩展点。
//
// 插件实现此接口以在节点注册、更新、删除时执行自定义逻辑。
type NodeLifecycleHook interface {
	// Name 返回钩子名称。
	Name() string
	// OnNodeRegistered 节点注册时触发。
	OnNodeRegistered(node *model.Node) error
	// OnNodeUpdated 节点更新时触发。
	OnNodeUpdated(node *model.Node, changes map[string]any) error
	// OnNodeDeleted 节点删除时触发。
	OnNodeDeleted(nodeID string) error
}
