package core

import (
	"context"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// EventStore 是事件持久化存储的抽象接口。
//
// 提供事件的追加与查询能力，供 LocalBus 持久化事件。
// 当前提供 SQLite 实现与内存实现。
type EventStore interface {
	// Append 追加一个事件到持久化存储。
	Append(ctx context.Context, e *event.Event) error

	// Query 按条件查询历史事件。
	Query(ctx context.Context, opts QueryOptions) ([]*event.Event, error)

	// Close 关闭存储，释放资源。
	Close() error
}

// QueryOptions 是查询历史事件的选项。
type QueryOptions struct {
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
	// Offset 分页偏移。
	Offset int
}

// Cleaner 是 EventStore 可选的清理接口。
//
// 实现 EventStore 的存储可通过实现此接口支持 TTL 与上限清理。
// LocalBus 会定期调用 Cleanup 以清理过期与超额事件。
type Cleaner interface {
	// Cleanup 清理过期与超额事件，返回被清理的事件数量。
	Cleanup(ctx context.Context) (int, error)
}

// StoreConfig 是 EventStore 实现的公共配置。
type StoreConfig struct {
	// TTL 事件保留时长，超过后自动清理。默认 7 天。
	TTL time.Duration
	// MaxEvents 事件数量上限，超过后清理最旧的事件。默认 100 万。
	MaxEvents int
	// CleanupInterval 后台清理执行间隔。默认 1 小时。
	CleanupInterval time.Duration
	// Now 时钟函数，默认 time.Now。可用于测试中控制时间。
	Now func() time.Time
}

// DefaultStoreConfig 返回带默认值的 StoreConfig。
func DefaultStoreConfig() StoreConfig {
	return StoreConfig{
		TTL:             7 * 24 * time.Hour,
		MaxEvents:       1000000,
		CleanupInterval: time.Hour,
		Now:             time.Now,
	}
}

// applyDefaults 将 cfg 中零值字段填充为默认值，返回填充后的配置。
func (cfg StoreConfig) applyDefaults() StoreConfig {
	out := cfg
	if out.TTL <= 0 {
		out.TTL = 7 * 24 * time.Hour
	}
	if out.MaxEvents <= 0 {
		out.MaxEvents = 1000000
	}
	if out.CleanupInterval <= 0 {
		out.CleanupInterval = time.Hour
	}
	if out.Now == nil {
		out.Now = time.Now
	}
	return out
}
