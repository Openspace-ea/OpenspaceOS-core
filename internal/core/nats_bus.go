package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATS 事件总线相关的默认配置常量。
const (
	// defaultStreamName 是 JetStream 权威事件流的名称。
	defaultStreamName = "events"
	// defaultStreamSubjects 是存放到 stream 的 subject 模式。
	defaultStreamSubjects = "events.>"
	// defaultEventSubjectsPrefix 是事件 subject 前缀。
	defaultEventSubjectsPrefix = "events."
	// defaultMaxStreamAge 是 JetStream stream 事件保留时长（对应原 TTL 语义）。
	defaultMaxStreamAge = 7 * 24 * time.Hour
	// defaultPushBufferSize 是订阅 channel 缓冲大小。
	defaultPushBufferSize = 256
	// defaultAckWait 是消息确认等待时长。
	defaultAckWait = 10 * time.Second
	// defaultMaxDeliver 是投递失败重试次数。
	defaultMaxDeliver = 10
)

// jetStreamConfig 是 NATSBus 的配置（经 applyDefaults 填充默认值）。
type jetStreamConfig struct {
	StreamName     string
	MaxStreamAge   time.Duration
	PushBufferSize int
	AckWait        time.Duration
	MaxDeliver     int
}

func (c jetStreamConfig) applyDefaults() jetStreamConfig {
	if c.StreamName == "" {
		c.StreamName = defaultStreamName
	}
	if c.MaxStreamAge <= 0 {
		c.MaxStreamAge = defaultMaxStreamAge
	}
	if c.PushBufferSize <= 0 {
		c.PushBufferSize = defaultPushBufferSize
	}
	if c.AckWait <= 0 {
		c.AckWait = defaultAckWait
	}
	if c.MaxDeliver <= 0 {
		c.MaxDeliver = defaultMaxDeliver
	}
	return c
}

// NATSBus 是基于 NATS JetStream 的分布式 MessageBus 实现。
//
// 与 LocalBus 对齐 MessageBus 接口：
//   - 权威事件流：Publish 将事件写入 JetStream stream（持久化），JetStream 保证不丢失（T4.2）；
//   - 实时订阅：Subscribe 为每个订阅者创建独立 push consumer，实时接收新事件（T4.4 多订阅者）；
//   - 历史回放：Replay 通过 pull consumer 顺序遍历并按条件过滤（T4.3）；
//   - Schema 校验：发布前经 registry.Validate（T4.6）。
type NATSBus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	stream   jetstream.Stream
	cfg      jetStreamConfig
	registry *event.SchemaRegistry
	logger   *slog.Logger

	mu          sync.Mutex
	subscribers map[uint64]*natsSubscriber
	nextSubID   uint64
	wg          sync.WaitGroup
	done        chan struct{}
	closed      bool
	closeOnce   sync.Once
}

// natsSubscriber 表示一个 JetStream push consumer 订阅。
type natsSubscriber struct {
	id         uint64
	eventTypes map[event.EventType]bool
	ch         chan *event.Event
	iter       jetstream.MessagesContext
}

// NewNATSBus 创建基于 NATS JetStream 的事件总线。
//
// url 例如 "nats://localhost:4222"。registry 为 nil 时使用默认注册中心。
// 创建时会建立 stream（不存在则自动创建）。
func NewNATSBus(url string, registry *event.SchemaRegistry, logger *slog.Logger, opts ...NATSBusOption) (*NATSBus, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if registry == nil {
		registry = event.NewSchemaRegistry()
	}

	b := &NATSBus{
		cfg:         jetStreamConfig{}.applyDefaults(),
		registry:    registry,
		logger:      logger,
		subscribers: make(map[uint64]*natsSubscriber),
		done:        make(chan struct{}),
	}
	for _, o := range opts {
		o(&b.cfg)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("连接 NATS 失败 (%s): %w", url, err)
	}
	b.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("创建 JetStream context 失败: %w", err)
	}
	b.js = js

	if err := b.ensureStream(); err != nil {
		nc.Close()
		return nil, err
	}
	return b, nil
}

// NATSBusOption 配置 NATSBus 的选项函数。
type NATSBusOption func(*jetStreamConfig)

// WithStreamName 设置 JetStream stream 名称。
func WithStreamName(name string) NATSBusOption {
	return func(c *jetStreamConfig) { c.StreamName = name }
}

// WithMaxStreamAge 设置 stream 保留时长。
func WithMaxStreamAge(d time.Duration) NATSBusOption {
	return func(c *jetStreamConfig) { c.MaxStreamAge = d }
}

// ensureStream 确保目标 stream 存在；不存在则自动创建。
func (b *NATSBus) ensureStream() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := jetstream.StreamConfig{
		Name:      b.cfg.StreamName,
		Subjects:  []string{defaultStreamSubjects},
		MaxAge:    b.cfg.MaxStreamAge,
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Discard:   jetstream.DiscardOld,
	}

	si, err := b.js.Stream(ctx, b.cfg.StreamName)
	if err != nil {
		if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return fmt.Errorf("获取 JetStream stream 失败: %w", err)
		}
		s, serr := b.js.CreateStream(ctx, cfg)
		if serr != nil {
			return fmt.Errorf("创建 JetStream stream 失败: %w", serr)
		}
		b.stream = s
		b.logger.Info("已创建 JetStream 事件流", "stream", b.cfg.StreamName)
		return nil
	}
	b.stream = si
	b.logger.Info("已连接 JetStream 事件流", "stream", b.cfg.StreamName)
	return nil
}

// subjectForEvent 返回事件对应的 subject。事件类型作为 subject 末尾 token，用作路由。
func subjectForEvent(e *event.Event) string {
	return defaultEventSubjectsPrefix + cleanSubjectToken(string(e.EventType))
}

// cleanSubjectToken 将事件类型转换为安全的 subject token。
func cleanSubjectToken(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '.' || r == '>' || r == '*' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// eventToMsgData 将事件序列化为 JetStream 消息数据（JSON）。
func eventToMsgData(e *event.Event) ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("序列化事件失败: %w", err)
	}
	return data, nil
}

// msgDataToEvent 将 JetStream 消息数据解码为事件。
func msgDataToEvent(data []byte) (*event.Event, error) {
	var e event.Event
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("反序列化事件失败: %w", err)
	}
	return &e, nil
}

// Publish 发布一个事件到总线。
//
// 流程：traceId 透传 → Schema 校验 → 写入 JetStream stream（持久化）。
// JetStream 在持久化完成后才确认，保证事件不丢失（T4.2）。
func (b *NATSBus) Publish(ctx context.Context, e *event.Event) error {
	if e == nil {
		return errNilEvent
	}
	if b.closed {
		return errBusClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// traceId 透传
	if e.TraceID == "" {
		if tid := traceIDFromContext(ctx); tid != "" {
			e.TraceID = tid
		}
	}

	// Schema 校验（T4.6：保留发布前校验）
	if err := b.registry.Validate(e); err != nil {
		return err
	}

	data, err := eventToMsgData(e)
	if err != nil {
		return err
	}

	hdrs := nats.Header{}
	hdrs.Set("Timestamp", e.Timestamp.UTC().Format(time.RFC3339Nano))
	hdrs.Set("EventType", string(e.EventType))
	hdrs.Set("SourceNodeId", e.SourceNodeID)

	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := b.js.PublishMsg(pubCtx, &nats.Msg{
		Subject: subjectForEvent(e),
		Header:  hdrs,
		Data:    data,
	}); err != nil {
		return fmt.Errorf("发布事件到 JetStream 失败: %w", err)
	}
	return nil
}

// Subscribe 订阅事件。
//
// 为每个订阅者创建独立 push consumer（DeliverNew：只收新事件），
// 并由独立 goroutine 将事件写入该订阅者的 channel（T4.4 多订阅者隔离）。
// 事件类型过滤在消费后进行（subject 已按类型路由，仅投递匹配类型）。
func (b *NATSBus) Subscribe(opts SubscribeOptions) (<-chan *event.Event, func()) {
	if b.closed {
		return nil, func() {}
	}

	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = b.cfg.PushBufferSize
	}
	typeFilter := make(map[event.EventType]bool, len(opts.EventTypes))
	for _, et := range opts.EventTypes {
		typeFilter[et] = true
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, func() {}
	}
	id := b.nextSubID
	b.nextSubID++
	ch := make(chan *event.Event, bufSize)
	sub := &natsSubscriber{id: id, eventTypes: typeFilter, ch: ch}
	b.subscribers[id] = sub
	b.mu.Unlock()

	// 创建 push consumer 并启动消费 goroutine
	if err := b.startConsumer(sub); err != nil {
		b.logger.Error("创建订阅 consumer 失败", "subId", id, "error", err)
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
		close(ch)
		return nil, func() {}
	}

	unsubscribe := func() {
		// 幂等取消
		b.mu.Lock()
		if _, ok := b.subscribers[id]; !ok {
			b.mu.Unlock()
			return
		}
		delete(b.subscribers, id)
		ite := sub.iter
		b.mu.Unlock()
		if ite != nil {
			ite.Stop()
		}
		close(ch)
	}
	return ch, unsubscribe
}

// startConsumer 为订阅者创建 push consumer 并启动消费 goroutine。
func (b *NATSBus) startConsumer(sub *natsSubscriber) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cons, err := b.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "",
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
		MaxDeliver:    b.cfg.MaxDeliver,
	})
	if err != nil {
		return fmt.Errorf("创建 push consumer 失败: %w", err)
	}

	iter, err := cons.Messages()
	if err != nil {
		return fmt.Errorf("初始化消费迭代器失败: %w", err)
	}
	sub.iter = iter

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer iter.Stop()
		for {
			select {
			case <-b.closeNotifier():
				return
			default:
			}
			msg, err := iter.Next()
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return
			}
			if err != nil {
				return
			}
			e, derr := msgDataToEvent(msg.Data())
			if derr != nil {
				continue
			}
			// 类型过滤（subject 本身已按类型路由，双保险）
			if len(sub.eventTypes) > 0 && !sub.eventTypes[e.EventType] {
				continue
			}
			select {
			case sub.ch <- e:
			case <-b.closeNotifier():
				return
			}
		}
	}()

	return nil
}

// closeNotifier 返回在总线关闭时关闭的 channel，供消费 goroutine 结束判断。
func (b *NATSBus) closeNotifier() <-chan struct{} {
	return b.done
}

// Replay 回放历史事件。
//
// 通过 JetStream pull consumer 顺序遍历 stream 历史消息，按 opts 条件过滤后返回。
// 使用 FetchNoWait 逐批拉取：只取当前已落库的消息，读尽立即返回（不会因等待新消息而阻塞）。
func (b *NATSBus) Replay(ctx context.Context, opts ReplayOptions) ([]*event.Event, error) {
	if b.closed {
		return nil, errBusClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cons, err := b.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:        "",
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckNonePolicy,
		MaxDeliver:     1,
	})
	if err != nil {
		return nil, fmt.Errorf("创建回放 consumer 失败: %w", err)
	}

	var out []*event.Event
	limit := opts.Limit
	// 每批拉取数量：未设上限时一次尽量拉全（FetchNoWait 不做超时等待）
	batch := 1000
	if limit > 0 && limit < batch {
		batch = limit
	}

	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		msgs, err := cons.FetchNoWait(batch)
		if err != nil {
			break
		}
		n := 0
		for msg := range msgs.Messages() {
			n++
			e, derr := msgDataToEvent(msg.Data())
			if derr != nil {
				continue
			}
			if !matchReplay(e, opts) {
				continue
			}
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if err := msgs.Error(); err != nil {
			break
		}
		// 本批未取到任何消息，说明历史已读尽
		if n == 0 {
			break
		}
	}
	return out, nil
}

// matchReplay 判断事件是否匹配回放条件。
func matchReplay(e *event.Event, opts ReplayOptions) bool {
	if len(opts.EventTypes) > 0 {
		found := false
		for _, et := range opts.EventTypes {
			if e.EventType == et {
				found = true
				break
			}
		}
		if !found {
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

// Ping 检查 NATS 连接是否可达，用于健康检查（T6.3）。
//
// 返回 nil 表示已连接；连接已关闭或掉线时返回错误。
func (b *NATSBus) Ping(ctx context.Context) error {
	if b.nc == nil || !b.nc.IsConnected() {
		return fmt.Errorf("NATS 未连接")
	}
	return nil
}

// Close 关闭总线，释放资源。
func (b *NATSBus) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.mu.Unlock()
		close(b.done)
		b.wg.Wait()
		if b.nc != nil {
			b.nc.Close()
		}
	})
	return nil
}

var _ MessageBus = (*NATSBus)(nil)