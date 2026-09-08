package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// traceIDLogAttr 是日志中 traceID 字段的键名。
const traceIDLogAttr = "traceId"

// traceContextKey 用于在 context 中存储 traceID（仅用于日志 handler，不暴露）。
type traceContextKey struct{}

// traceHandler 包装 slog.Handler，在每条日志记录中自动注入 traceId 字段。
//
// 从 context 中提取 OTel SpanContext 的 TraceID，若存在则添加到日志记录的属性中。
// 当 context 中没有有效的 span 时，traceId 字段不出现（避免写入空值）。
type traceHandler struct {
	handler slog.Handler
}

// Enabled 委托给底层 handler。
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle 在日志记录中注入 traceId 后委托给底层 handler。
func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	// 从 context 中提取 OTel traceID
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() && sc.IsValid() {
		record.AddAttrs(slog.String(traceIDLogAttr, sc.TraceID().String()))
	}
	return h.handler.Handle(ctx, record)
}

// WithAttrs 委托给底层 handler。
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{handler: h.handler.WithAttrs(attrs)}
}

// WithGroup 委托给底层 handler。
func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{handler: h.handler.WithGroup(name)}
}

// TraceLogger 返回一个在日志中自动注入 traceId 的 Logger。
//
// 该 Logger 通过包装 slog.Handler 实现：每次记录日志时，
// 从 context 中提取 OTel TraceID 并添加为 "traceId" 字段。
// 确保所有日志记录在存在 trace 时都包含 traceId，便于链路追踪关联。
//
// 使用方式：
//
//	logger = observability.TraceLogger(logger)
//	logger.InfoContext(ctx, "处理请求", "path", r.URL.Path)
func TraceLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return slog.New(&traceHandler{handler: logger.Handler()})
}
