package plugin

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// Broker 是插件与 Core 交互的受限网关。
//
// 仅暴露允许的 API 给插件，实现最小权限隔离。
// 插件不直接访问 MessageBus 或 KGService，而是通过 Broker 间接调用，
// 从而限制插件的能力范围，防止未授权操作。
type Broker struct {
	bus    core.MessageBus
	kg     *core.KGService
	logger *slog.Logger
}

// NewBroker 创建插件 Broker。
//
// logger 为 nil 时使用 slog.Default()。
func NewBroker(bus core.MessageBus, kg *core.KGService, logger *slog.Logger) *Broker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Broker{
		bus:    bus,
		kg:     kg,
		logger: logger,
	}
}

// PublishEvent 允许插件发布事件到总线。
//
// 事件需通过 Schema 校验才会被发布。
// 插件需在 Manifest 的 permissions 中声明 "event.publish" 权限。
func (b *Broker) PublishEvent(ctx context.Context, e *event.Event) error {
	if e == nil {
		return fmt.Errorf("event 不能为 nil")
	}
	return b.bus.Publish(ctx, e)
}

// QueryNode 允许插件查询单个节点。
//
// 插件需在 Manifest 的 permissions 中声明 "kg.query" 权限。
func (b *Broker) QueryNode(ctx context.Context, nodeID string) (*model.Node, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("nodeID 不能为空")
	}
	return b.kg.GetNode(ctx, nodeID)
}

// ListNodes 允许插件按条件列出节点。
func (b *Broker) ListNodes(ctx context.Context, opts core.ListOptions) ([]*model.Node, error) {
	return b.kg.ListNodes(ctx, opts)
}

// SubscribeEvents 允许插件订阅事件总线上的事件。
//
// 返回事件 channel 与取消订阅函数。
// opts.EventTypes 为空表示订阅所有事件。
func (b *Broker) SubscribeEvents(opts core.SubscribeOptions) (<-chan *event.Event, func()) {
	return b.bus.Subscribe(opts)
}
