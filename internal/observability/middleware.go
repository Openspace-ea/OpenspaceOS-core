package observability

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// traceIDHeader 是响应中返回 traceID 的 HTTP header 名称。
const traceIDHeader = "X-Trace-Id"

// tracerName 是 observability 包使用的 tracer 名称。
const tracerName = "github.com/openspace-os/openspace-os-core/observability"

// HTTPMiddleware 为 chi 中间件，自动为每个 HTTP 请求创建 span。
//
// 功能：
//   - 为每个请求创建 span（名称为 "HTTP 方法 路径"，如 "GET /api/v1/nodes"）
//   - 记录 HTTP 方法、路径、状态码、耗时等属性
//   - 将 traceID 写入响应 header（X-Trace-Id）
//   - 当 OTel 未启用时，traceID 为随机生成的 UUID，保证日志可关联
func HTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.GetTracerProvider().Tracer(tracerName, trace.WithInstrumentationAttributes(
		attribute.String("service.name", serviceName),
	))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// span 名称：HTTP 方法 + 路径
			spanName := r.Method + " " + r.URL.Path

			// 创建 span，将 span context 注入到 request context
			ctx, span := tracer.Start(r.Context(), spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.target", r.URL.Path),
					attribute.String("http.scheme", schemeFromRequest(r)),
					attribute.String("http.host", r.Host),
					attribute.String("http.user_agent", r.UserAgent()),
				),
			)
			defer span.End()

			// 在调用 next 之前将 traceID 写入响应 header（header 必须在响应体写入前设置）
			writeTraceIDHeader(w, span)

			// 包装 ResponseWriter 以捕获状态码
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// 使用带 span context 的 ctx 继续处理请求
			r = r.WithContext(ctx)
			next.ServeHTTP(rw, r)

			// 记录响应属性
			elapsed := time.Since(start)
			span.SetAttributes(
				attribute.Int("http.status_code", rw.status),
				attribute.Float64("http.duration_ms", float64(elapsed.Microseconds())/1000.0),
			)
			if rw.status >= 400 {
				span.SetStatus(codes.Error, http.StatusText(rw.status))
				span.RecordError(httpError{status: rw.status, message: http.StatusText(rw.status)})
			}
		})
	}
}

// schemeFromRequest 从请求中提取 URL scheme。
func schemeFromRequest(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// writeTraceIDHeader 将 traceID 写入响应 header。
// 当 OTel 启用且 span 有效时，使用 OTel traceID；
// 否则生成一个随机 UUID 作为关联标识，确保 header 始终存在。
func writeTraceIDHeader(w http.ResponseWriter, span trace.Span) {
	sc := span.SpanContext()
	if sc.HasTraceID() && sc.IsValid() {
		w.Header().Set(traceIDHeader, sc.TraceID().String())
		return
	}
	// OTel 未启用时生成随机 UUID 用于日志关联
	w.Header().Set(traceIDHeader, uuid.NewString())
}

// httpError 是用于在 span 中记录 HTTP 错误的轻量错误类型。
type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string {
	return e.message
}

// responseWriter 包装 http.ResponseWriter 以捕获状态码。
type responseWriter struct {
	http.ResponseWriter
	status     int
	wroteHeader bool
}

// WriteHeader 捕获响应状态码。
func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

// Write 确保在首次 Write 时记录默认状态码 200。
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}
