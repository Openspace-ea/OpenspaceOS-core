package core

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// memEntry 是内存 EventStore 中的一条事件记录。
type memEntry struct {
	event     *event.Event
	createdAt time.Time
}

// MemoryEventStore 是基于内存切片的 EventStore 实现。
//
// 不依赖 SQLite，适用于单元测试与快速启动场景。
// 使用 sync.RWMutex 保护并发访问，支持 TTL 与数量上限清理。
type MemoryEventStore struct {
	mu        sync.RWMutex
	entries   []memEntry
	cfg       StoreConfig
	closed    bool
	closeOnce sync.Once
}

// NewMemoryEventStore 创建内存 EventStore。
//
// cfg 为零值时使用默认配置（TTL 7 天、上限 100 万、每小时清理）。
func NewMemoryEventStore(cfg StoreConfig) *MemoryEventStore {
	return &MemoryEventStore{
		entries: make([]memEntry, 0, 64),
		cfg:     cfg.applyDefaults(),
	}
}

// Append 追加一个事件到内存存储。
func (s *MemoryEventStore) Append(ctx context.Context, e *event.Event) error {
	if e == nil {
		return errNilEvent
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errStoreClosed
	}
	s.entries = append(s.entries, memEntry{event: e, createdAt: s.cfg.Now()})
	s.mu.Unlock()
	return nil
}

// Query 按条件查询历史事件，按事件 Timestamp 升序排列。
func (s *MemoryEventStore) Query(ctx context.Context, opts QueryOptions) ([]*event.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errStoreClosed
	}

	typeFilter := make(map[event.EventType]struct{}, len(opts.EventTypes))
	for _, et := range opts.EventTypes {
		typeFilter[et] = struct{}{}
	}

	result := make([]*event.Event, 0, len(s.entries))
	for _, entry := range s.entries {
		if !matchQuery(entry.event, opts, typeFilter) {
			continue
		}
		// 返回事件的副本，避免调用方修改存储中的事件
		result = append(result, cloneEvent(entry.event))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	if opts.Offset > 0 && opts.Offset < len(result) {
		result = result[opts.Offset:]
	} else if opts.Offset >= len(result) {
		return nil, nil
	}
	if opts.Limit > 0 && opts.Limit < len(result) {
		result = result[:opts.Limit]
	}
	return result, nil
}

// Cleanup 清理过期与超额事件，返回被清理的事件数量。
func (s *MemoryEventStore) Cleanup(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errStoreClosed
	}

	now := s.cfg.Now()
	cutoff := now.Add(-s.cfg.TTL)
	kept := s.entries[:0]
	deleted := 0
	for _, entry := range s.entries {
		if entry.createdAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, entry)
	}
	s.entries = kept

	// 清理超额事件（保留最新的 MaxEvents 条）
	if len(s.entries) > s.cfg.MaxEvents {
		excess := len(s.entries) - s.cfg.MaxEvents
		// entries 按 Append 顺序排列，最早的在前面
		s.entries = s.entries[excess:]
		deleted += excess
	}
	return deleted, nil
}

// Close 关闭存储。
func (s *MemoryEventStore) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.entries = nil
		s.mu.Unlock()
	})
	return nil
}

// Len 返回当前存储的事件数量（用于测试）。
func (s *MemoryEventStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// matchQuery 判断事件是否匹配查询条件。
func matchQuery(e *event.Event, opts QueryOptions, typeFilter map[event.EventType]struct{}) bool {
	if len(typeFilter) > 0 {
		if _, ok := typeFilter[e.EventType]; !ok {
			return false
		}
	}
	if opts.SourceNodeID != "" && e.SourceNodeID != opts.SourceNodeID {
		return false
	}
	if opts.StartTime != nil && e.Timestamp.Before(*opts.StartTime) {
		return false
	}
	if opts.EndTime != nil && e.Timestamp.After(*opts.EndTime) {
		return false
	}
	return true
}

// cloneEvent 返回事件的浅拷贝（Payload map 独立复制）。
func cloneEvent(e *event.Event) *event.Event {
	if e == nil {
		return nil
	}
	out := &event.Event{
		EventID:      e.EventID,
		EventType:    e.EventType,
		Timestamp:    e.Timestamp,
		SourceNodeID: e.SourceNodeID,
		TraceID:      e.TraceID,
	}
	if e.Payload != nil {
		out.Payload = make(map[string]any, len(e.Payload))
		for k, v := range e.Payload {
			out.Payload[k] = v
		}
	}
	return out
}

// 编译期断言：MemoryEventStore 实现 EventStore 与 Cleaner 接口。
var (
	_ EventStore = (*MemoryEventStore)(nil)
	_ Cleaner    = (*MemoryEventStore)(nil)
)
