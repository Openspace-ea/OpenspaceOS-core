package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// TestInit_Disabled 测试 OTel 未启用时的初始化。
//
// 验证：
//   - 不返回错误
//   - shutdown 函数可安全调用
//   - 全局 TracerProvider 为 noop（创建的 span 无有效 traceID）
func TestInit_Disabled(t *testing.T) {
	shutdown, err := Init(OtelConfig{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// shutdown 应可安全调用
	assert.NoError(t, shutdown())

	// noop tracer 创建的 span 无有效 traceID
	tracer := otel.GetTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	assert.False(t, span.SpanContext().HasTraceID())
	span.End()
}

// TestInit_Enabled_InvalidEndpoint 测试 OTel 启用但端点不可达时的降级行为。
//
// 验证：
//   - Init 不返回错误（不阻断启动）
//   - shutdown 函数可安全调用（端点不可达时 shutdown 可能返回错误，但不 panic）
func TestInit_Enabled_InvalidEndpoint(t *testing.T) {
	// 使用一个不可达的端点
	// OTLP gRPC exporter 创建是惰性的，不会立即失败
	// 但 shutdown 时尝试 flush 可能超时
	shutdown, err := Init(OtelConfig{
		Enabled:     true,
		Endpoint:    "localhost:19999", // 不可达端口
		ServiceName: "openspace-os-core-test",
	})
	// Init 本身不应返回错误
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// shutdown 调用应不 panic（端点不可达时可能返回错误，这是预期行为）
	_ = shutdown()
}

// TestNewCoreMetrics 测试 CoreMetrics 的创建与指标记录。
//
// 验证：
//   - 所有 instrument 均非 nil
//   - Add/Record 方法可安全调用（noop 模式下不 panic）
func TestNewCoreMetrics(t *testing.T) {
	// 确保 OTel 为 noop 模式（不产生网络调用）
	shutdown, _ := Init(OtelConfig{Enabled: false})
	defer shutdown()

	cm, err := NewCoreMetrics()
	require.NoError(t, err)
	require.NotNil(t, cm)

	// 所有 instrument 应非 nil（即使是 noop 实现）
	assert.NotNil(t, cm.EventPublishQPS)
	assert.NotNil(t, cm.EventDispatchLatency)
	assert.NotNil(t, cm.TelemetryIngressQPS)
	assert.NotNil(t, cm.TelemetryParseLatency)
	assert.NotNil(t, cm.CommandSendLatency)
	assert.NotNil(t, cm.CommandSuccessTotal)
	assert.NotNil(t, cm.CommandTotal)
	assert.NotNil(t, cm.PluginLoadCount)

	// 记录指标应不 panic
	ctx := context.Background()
	cm.EventPublishQPS.Add(ctx, 1)
	cm.EventDispatchLatency.Record(ctx, 0.001)
	cm.TelemetryIngressQPS.Add(ctx, 1)
	cm.TelemetryParseLatency.Record(ctx, 0.002)
	cm.CommandSendLatency.Record(ctx, 0.003)
	cm.CommandTotal.Add(ctx, 1)
	cm.CommandSuccessTotal.Add(ctx, 1)
	cm.PluginLoadCount.Add(ctx, 1)
	cm.PluginLoadCount.Add(ctx, -1)

	// RecordCommandResult 应同时更新 Total 和 Success
	cm.RecordCommandResult(ctx, true)
	cm.RecordCommandResult(ctx, false)
}

// TestTraceLogger 测试 TraceLogger 是否在日志中注入 traceId。
//
// 验证：
//   - 存在 OTel span 时，日志记录包含 traceId 字段
//   - 不存在 span 时，日志记录不包含 traceId 字段
func TestTraceLogger(t *testing.T) {
	// 使用真实的 TracerProvider（带内存 exporter）以便生成有效 traceID
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	// 创建带 trace 注入能力的 logger，输出到 buffer
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := TraceLogger(slog.New(handler))

	tracer := tp.Tracer("test-tracer")
	ctx, span := tracer.Start(context.Background(), "test-operation")
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	require.NotEmpty(t, traceID)

	// 在 span context 下记录日志
	logger.InfoContext(ctx, "测试日志消息", "key", "value")

	// 解析日志输出，验证包含 traceId
	var logRecord map[string]any
	err := json.Unmarshal(buf.Bytes(), &logRecord)
	require.NoError(t, err)

	assert.Equal(t, "测试日志消息", logRecord["msg"])
	assert.Equal(t, traceID, logRecord[traceIDLogAttr])
	assert.Equal(t, "value", logRecord["key"])

	// 验证无 span context 时不包含 traceId
	buf.Reset()
	logger.InfoContext(context.Background(), "无 span 的日志")

	var logRecord2 map[string]any
	err = json.Unmarshal(buf.Bytes(), &logRecord2)
	require.NoError(t, err)
	assert.Equal(t, "无 span 的日志", logRecord2["msg"])
	_, hasTraceID := logRecord2[traceIDLogAttr]
	assert.False(t, hasTraceID, "无 span 时不应包含 traceId")
}

// TestTraceLogger_NilLogger 测试 TraceLogger 传入 nil logger 时的行为。
func TestTraceLogger_NilLogger(t *testing.T) {
	// 不应 panic
	logger := TraceLogger(nil)
	require.NotNil(t, logger)

	// 应可安全记录日志
	logger.InfoContext(context.Background(), "test")
}

// TestTracedBus_Publish 测试 TracedBus 在 Publish 时创建 span。
func TestTracedBus_Publish(t *testing.T) {
	// 使用真实的 TracerProvider 以验证 span 创建
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	// 使用内存 EventStore 创建 LocalBus，然后包装为 TracedBus
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	defer bus.Close()

	tracedBus := NewTracedBus(bus)

	// 构造一个合法的事件
	e := &event.Event{
		EventID:      "evt-test-1",
		EventType:    event.EventTelemetryReceived,
		SourceNodeID: "node-1",
		Timestamp:    time.Now(),
	}
	require.NoError(t, e.SetPayload(event.TelemetryReceivedPayload{
		SatelliteID: "sat-1",
		Timestamp:   time.Now(),
		Parameters:  map[string]any{"temp": 36.5},
		Quality:     "good",
	}))

	// Publish 应创建 "event.publish" span
	err := tracedBus.Publish(context.Background(), e)
	require.NoError(t, err)

	// 验证 span 已生成
	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "应生成一个 event.publish span")
	assert.Equal(t, "event.publish", spans[0].Name)
}

// TestTracedBus_ImplementsMessageBus 验证 TracedBus 实现 MessageBus 接口。
func TestTracedBus_ImplementsMessageBus(t *testing.T) {
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	defer bus.Close()

	tracedBus := NewTracedBus(bus)

	// 编译期断言：TracedBus 实现 MessageBus 接口
	var _ core.MessageBus = tracedBus
}

// TestTraceHandler_WithErrorSpan 测试在 error span 下日志仍包含 traceId。
func TestTraceHandler_WithErrorSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := TraceLogger(slog.New(handler))

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "error-op")
	span.SetStatus(codes.Error, "test error")
	span.RecordError(assertError{"test error"})
	span.End()

	logger.ErrorContext(ctx, "操作失败")

	output := buf.String()
	assert.True(t, strings.Contains(output, span.SpanContext().TraceID().String()))
}

type assertError struct {
	msg string
}

func (e assertError) Error() string {
	return e.msg
}
