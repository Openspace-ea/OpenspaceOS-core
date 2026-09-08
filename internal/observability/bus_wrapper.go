package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// TracedBus 包装 core.MessageBus，为 Publish 与事件派发添加 OTel trace span。
//
// 由于不能修改 internal/core/ 下已有文件，通过此 wrapper 在 observability 包中
// 以组合方式实现 trace 埋点。TracedBus 实现了 core.MessageBus 接口，
// 可透明替换原始的 LocalBus。
type TracedBus struct {
	bus    core.MessageBus
	tracer trace.Tracer
}

// NewTracedBus 创建带 trace 埋点的事件总线包装器。
//
// inner 为原始的 MessageBus（如 *core.LocalBus）。
func NewTracedBus(inner core.MessageBus) *TracedBus {
	return &TracedBus{
		bus:    inner,
		tracer: otel.GetTracerProvider().Tracer(tracerName),
	}
}

// Publish 发布事件，创建 "event.publish" span 并记录 eventType。
func (b *TracedBus) Publish(ctx context.Context, e *event.Event) error {
	ctx, span := b.tracer.Start(ctx, "event.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("event.type", string(e.EventType)),
			attribute.String("event.id", e.EventID),
			attribute.String("event.source", e.SourceNodeID),
		),
	)
	defer span.End()

	start := time.Now()
	err := b.bus.Publish(ctx, e)
	span.SetAttributes(
		attribute.Float64("event.publish.duration_ms", float64(time.Since(start).Microseconds())/1000.0),
	)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// Subscribe 订阅事件，返回的 channel 中的每个事件都会被创建 "event.dispatch" span。
//
// 通过包装返回的 channel，在消费事件时自动创建 dispatch span，
// 记录 eventType 与 dispatch 耗时。
func (b *TracedBus) Subscribe(opts core.SubscribeOptions) (<-chan *event.Event, func()) {
	innerCh, unsub := b.bus.Subscribe(opts)

	// 创建包装 channel，异步转发事件并创建 dispatch span
	wrappedCh := make(chan *event.Event, defaultTracedBusBufferSize)

	go func() {
		defer close(wrappedCh)
		for e := range innerCh {
			// 为每个派发的事件创建 span
			_, span := b.tracer.Start(context.Background(), "event.dispatch",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("event.type", string(e.EventType)),
					attribute.String("event.id", e.EventID),
					attribute.String("event.source", e.SourceNodeID),
				),
			)
			start := time.Now()

			// 转发事件到包装 channel
			select {
			case wrappedCh <- e:
			default:
				// 包装 channel 满，丢弃（与 LocalBus 背压策略一致）
			}

			span.SetAttributes(
				attribute.Float64("event.dispatch.duration_ms", float64(time.Since(start).Microseconds())/1000.0),
			)
			span.End()
		}
	}()

	return wrappedCh, unsub
}

// Replay 回放历史事件，委托给内部 bus。
func (b *TracedBus) Replay(ctx context.Context, opts core.ReplayOptions) ([]*event.Event, error) {
	ctx, span := b.tracer.Start(ctx, "event.replay",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()
	return b.bus.Replay(ctx, opts)
}

// Close 关闭总线，委托给内部 bus。
func (b *TracedBus) Close() error {
	return b.bus.Close()
}

// Ping 检查后台依赖连通性，委托给内部 bus（T6.3）。
func (b *TracedBus) Ping(ctx context.Context) error {
	return b.bus.Ping(ctx)
}

// 编译期断言：TracedBus 实现 MessageBus 接口。
var _ core.MessageBus = (*TracedBus)(nil)

// defaultTracedBusBufferSize 是 TracedBus 包装 channel 的缓冲区大小。
const defaultTracedBusBufferSize = 256
