package core

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"

	// 用于从 context 提取 OTel TraceID。
	"go.opentelemetry.io/otel/trace"
)

// publishBufferSize 是内部派发 channel 的缓冲区大小。
const publishBufferSize = 1024

// defaultCleanupInterval 是后台清理过期事件的默认间隔。
const defaultCleanupInterval = time.Hour

// LocalBus 是 MessageBus 的本地进程内实现。
//
// 提供异步事件派发、按 eventType 路由、Schema 校验、
// 事件持久化、背压（丢弃策略）与 TTL 清理等能力。
type LocalBus struct {
	store    EventStore
	registry *event.SchemaRegistry
	logger   *slog.Logger

	mu          sync.RWMutex
	subscribers map[uint64]*subscriber
	nextSubID   uint64

	publishCh chan *event.Event // 内部派发 channel
	done      chan struct{}     // 关闭信号
	wg        sync.WaitGroup    // 等待后台 goroutine 退出

	droppedCount    atomic.Int64 // 背压丢弃事件计数
	cleanupInterval time.Duration
	closeOnce       sync.Once
}

// subscriber 是一个事件订阅者。
type subscriber struct {
	id         uint64
	eventTypes map[event.EventType]bool // 为空表示订阅所有事件
	ch         chan *event.Event
}

// matches 判断订阅者是否订阅指定事件类型。
func (s *subscriber) matches(et event.EventType) bool {
	if len(s.eventTypes) == 0 {
		return true
	}
	return s.eventTypes[et]
}

// NewLocalBus 创建本地进程内事件总线。
//
//   - store 为 nil 时使用内存 EventStore（不持久化，仅用于测试）
//   - registry 为 nil 时使用 event.NewSchemaRegistry()
//   - logger 为 nil 时使用 slog.Default()
func NewLocalBus(store EventStore, registry *event.SchemaRegistry, logger *slog.Logger) *LocalBus {
	if store == nil {
		store = NewMemoryEventStore(StoreConfig{})
	}
	if registry == nil {
		registry = event.NewSchemaRegistry()
	}
	if logger == nil {
		logger = slog.Default()
	}

	b := &LocalBus{
		store:           store,
		registry:        registry,
		logger:          logger,
		subscribers:     make(map[uint64]*subscriber),
		publishCh:       make(chan *event.Event, publishBufferSize),
		done:            make(chan struct{}),
		cleanupInterval: defaultCleanupInterval,
	}

	// 启动派发 goroutine
	b.wg.Add(1)
	go b.dispatchLoop()

	// 启动后台清理 goroutine（仅当 store 实现 Cleaner 时）
	b.wg.Add(1)
	go b.cleanupLoop()

	return b
}

// Publish 发布一个事件到总线。
//
// 流程：提取 traceId → Schema 校验 → 持久化 → 写入派发 channel。
// 持久化成功后才派发给订阅者，保证事件不丢失。
func (b *LocalBus) Publish(ctx context.Context, e *event.Event) error {
	if e == nil {
		return errNilEvent
	}

	// 检查总线是否已关闭
	select {
	case <-b.done:
		return errBusClosed
	default:
	}

	// traceId 透传：如果事件中没有 traceId，从 context 中提取（如有 OTel trace）
	if e.TraceID == "" {
		if tid := traceIDFromContext(ctx); tid != "" {
			e.TraceID = tid
		}
	}

	// Schema 校验
	if err := b.registry.Validate(e); err != nil {
		return err
	}

	// 持久化（持久化成功后才派发）
	if err := b.store.Append(ctx, e); err != nil {
		return err
	}

	// 写入派发 channel，异步派发给订阅者
	select {
	case b.publishCh <- e:
		return nil
	case <-b.done:
		return errBusClosed
	}
}

// Subscribe 订阅事件。
//
// opts.EventTypes 为空表示订阅所有事件。
// 返回订阅 channel 与取消订阅函数。
func (b *LocalBus) Subscribe(opts SubscribeOptions) (<-chan *event.Event, func()) {
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = defaultSubscribeBufferSize
	}

	typeFilter := make(map[event.EventType]bool, len(opts.EventTypes))
	for _, et := range opts.EventTypes {
		typeFilter[et] = true
	}

	b.mu.Lock()
	id := b.nextSubID
	b.nextSubID++
	sub := &subscriber{
		id:         id,
		eventTypes: typeFilter,
		ch:         make(chan *event.Event, bufSize),
	}
	b.subscribers[id] = sub
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
	}
	return sub.ch, unsubscribe
}

// Replay 回放历史事件。
//
// 从 EventStore 查询历史事件，派发给当前活跃的订阅者，并返回查询结果。
// 回放的事件不会重新持久化。
func (b *LocalBus) Replay(ctx context.Context, opts ReplayOptions) ([]*event.Event, error) {
	events, err := b.store.Query(ctx, QueryOptions{
		EventTypes:   opts.EventTypes,
		SourceNodeID: opts.SourceNodeID,
		StartTime:    opts.StartTime,
		EndTime:      opts.EndTime,
		Limit:        opts.Limit,
	})
	if err != nil {
		return nil, err
	}

	// 派发给当前活跃的订阅者（不重新持久化）
	for _, e := range events {
		b.fanout(e)
	}
	return events, nil
}

// Ping 进程内总线恒可达，返回 nil（T6.3）。
func (b *LocalBus) Ping(ctx context.Context) error {
	return nil
}

// Close 关闭总线，释放资源。
func (b *LocalBus) Close() error {
	var storeErr error
	b.closeOnce.Do(func() {
		close(b.done)
		b.wg.Wait()
		storeErr = b.store.Close()
	})
	return storeErr
}

// DroppedCount 返回因背压被丢弃的事件总数（用于监控与测试）。
func (b *LocalBus) DroppedCount() int64 {
	return b.droppedCount.Load()
}

// dispatchLoop 是事件派发主循环。
//
// 从 publishCh 读取事件并分发给匹配的订阅者。
// 同一 eventType 的事件按写入 publishCh 的顺序派发，保证顺序性。
func (b *LocalBus) dispatchLoop() {
	defer b.wg.Done()
	for {
		select {
		case e := <-b.publishCh:
			b.fanout(e)
		case <-b.done:
			// 关闭后排空 publishCh 中剩余的事件
			for {
				select {
				case e := <-b.publishCh:
					b.fanout(e)
				default:
					return
				}
			}
		}
	}
}

// fanout 将事件分发给所有匹配的订阅者。
//
// 采用非阻塞发送：如果订阅者 channel 缓冲区满，丢弃事件并计数。
func (b *LocalBus) fanout(e *event.Event) {
	b.mu.RLock()
	subs := make([]*subscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		subs = append(subs, s)
	}
	b.mu.RUnlock()

	for _, s := range subs {
		if !s.matches(e.EventType) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.droppedCount.Add(1)
			b.logger.Warn("订阅者 channel 已满，丢弃事件",
				"eventId", e.EventID,
				"eventType", string(e.EventType),
			)
		}
	}
}

// cleanupLoop 是后台清理过期事件的循环。
//
// 仅当 store 实现 Cleaner 接口时生效。
func (b *LocalBus) cleanupLoop() {
	defer b.wg.Done()
	cleaner, ok := b.store.(Cleaner)
	if !ok {
		return
	}

	ticker := time.NewTicker(b.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			n, err := cleaner.Cleanup(ctx)
			cancel()
			if err != nil {
				b.logger.Error("清理过期事件失败", "error", err)
			} else if n > 0 {
				b.logger.Info("清理过期事件", "deleted", n)
			}
		case <-b.done:
			return
		}
	}
}

// traceIDFromContext 从 context 中提取 OTel TraceID。
//
// 如果 context 中包含 OTel span，返回其 TraceID 的十六进制字符串；
// 否则返回空字符串。
func traceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() && sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

// 编译期断言：LocalBus 实现 MessageBus 接口。
var _ MessageBus = (*LocalBus)(nil)
