package core

import (
	"context"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// MessageBus 是事件总线的抽象接口。
//
// MVP 提供本地进程内实现（LocalBus），后续可通过实现此接口
// 替换为 NATS/Kafka 等分布式消息中间件。
type MessageBus interface {
	// Publish 发布一个事件到总线。发布前会进行 Schema 校验，
	// 并先将事件持久化到 EventStore，持久化成功后才派发给订阅者。
	Publish(ctx context.Context, e *event.Event) error

	// Subscribe 订阅事件。opts 可指定 eventType 过滤。
	// 返回一个 channel，订阅者从中接收事件。
	// 返回的 unsubscribe 函数用于取消订阅。
	Subscribe(opts SubscribeOptions) (<-chan *event.Event, func())

	// Replay 回放历史事件。按 opts 中的条件过滤已持久化的事件。
	// 回放的事件不会重新持久化，但会派发给当前活跃的订阅者。
	Replay(ctx context.Context, opts ReplayOptions) ([]*event.Event, error)

	// Close 关闭总线，释放资源。
	Close() error

	// Ping 检查后台依赖（如消息中间件）的连通性，用于健康检查（T6.3）。
	// 返回 nil 表示依赖可达；进程内实现（LocalBus）恒返回 nil。
	Ping(ctx context.Context) error
}

// SubscribeOptions 是订阅事件的选项。
type SubscribeOptions struct {
	// EventTypes 订阅的事件类型列表。为空表示订阅所有事件。
	EventTypes []event.EventType
	// BufferSize 订阅 channel 的缓冲区大小，默认 256。
	BufferSize int
}

// ReplayOptions 是回放历史事件的选项。
type ReplayOptions struct {
	// EventTypes 过滤的事件类型列表。为空表示不限制。
	EventTypes []event.EventType
	// SourceNodeID 过滤的事件来源 Node ID。为空表示不限制。
	SourceNodeID string
	// StartTime 过滤条件：事件时间不早于此值。nil 表示不限制。
	StartTime *time.Time
	// EndTime 过滤条件：事件时间不晚于此值。nil 表示不限制。
	EndTime *time.Time
	// Limit 最大返回数量，0 表示无限制。
	Limit int
}

// 默认订阅 channel 缓冲区大小。
const defaultSubscribeBufferSize = 256
