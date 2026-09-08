package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestHTTPMiddleware_CreatesSpan 测试 HTTP 中间件为每个请求创建 span。
//
// 验证：
//   - span 名称为 "HTTP方法 路径"
//   - span 中记录了 HTTP 方法、路径、状态码等属性
func TestHTTPMiddleware_CreatesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	// 创建测试 handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// 包装中间件
	mw := HTTPMiddleware("test-service")
	server := httptest.NewServer(mw(handler))
	defer server.Close()

	// 发送请求
	resp, err := http.Get(server.URL + "/api/v1/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	// 验证 span 已创建
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /api/v1/test", spans[0].Name)

	// 验证 span 属性
	attrs := make(map[string]interface{})
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	assert.Equal(t, "GET", attrs["http.method"])
	assert.Equal(t, "/api/v1/test", attrs["http.target"])
	assert.Equal(t, int64(200), attrs["http.status_code"])
}

// TestHTTPMiddleware_TraceIDInHeader 测试 traceID 出现在响应 header 中。
//
// 验证：
//   - OTel 启用时，X-Trace-Id header 包含有效的 traceID
//   - traceID 与 span 中的 traceID 一致
func TestHTTPMiddleware_TraceIDInHeader(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := HTTPMiddleware("test-service")
	server := httptest.NewServer(mw(handler))
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	// 验证 X-Trace-Id header 存在且非空
	traceID := resp.Header.Get(traceIDHeader)
	assert.NotEmpty(t, traceID, "X-Trace-Id header 应非空")

	// 验证 span 中的 traceID 与 header 一致
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	spanTraceID := spans[0].SpanContext.TraceID().String()
	assert.Equal(t, spanTraceID, traceID, "响应 header 中的 traceID 应与 span 一致")
}

// TestHTTPMiddleware_NoopMode 测试 OTel 未启用时中间件仍正常工作。
//
// 验证：
//   - X-Trace-Id header 仍存在（使用 UUID 作为后备）
//   - 请求正常处理
func TestHTTPMiddleware_NoopMode(t *testing.T) {
	// 确保 OTel 为 noop 模式
	shutdown, _ := Init(OtelConfig{Enabled: false})
	defer shutdown()

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mw := HTTPMiddleware("test-service")
	server := httptest.NewServer(mw(handler))
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.True(t, handlerCalled, "handler 应被调用")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// noop 模式下 X-Trace-Id 应仍存在（UUID）
	traceID := resp.Header.Get(traceIDHeader)
	assert.NotEmpty(t, traceID, "noop 模式下 X-Trace-Id 应仍存在")
}

// TestHTTPMiddleware_ErrorStatusCode 测试中间件记录错误状态码。
//
// 验证：
//   - 当 handler 返回 4xx/5xx 时，span 标记为 Error 状态
func TestHTTPMiddleware_ErrorStatusCode(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mw := HTTPMiddleware("test-service")
	server := httptest.NewServer(mw(handler))
	defer server.Close()

	resp, err := http.Get(server.URL + "/error")
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// 验证 span 状态为 Error
	assert.Equal(t, codes.Error, spans[0].Status.Code)

	// 验证状态码属性
	attrs := make(map[string]interface{})
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	assert.Equal(t, int64(500), attrs["http.status_code"])
}

// TestHTTPMiddleware_PassesContext 测试中间件将 span context 注入到请求 context。
//
// 验证：
//   - 下游 handler 能从 r.Context() 中获取到 traceID
//   - context 中的 traceID 与响应 header 一致
func TestHTTPMiddleware_PassesContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	originalTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(originalTP)

	var ctxTraceID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 从 context 中提取 traceID（与 core.traceIDFromContext 相同逻辑）
		sc := trace.SpanContextFromContext(r.Context())
		if sc.HasTraceID() && sc.IsValid() {
			ctxTraceID = sc.TraceID().String()
		}
		w.WriteHeader(http.StatusOK)
	})
	mw := HTTPMiddleware("test-service")
	server := httptest.NewServer(mw(handler))
	defer server.Close()

	resp, err := http.Get(server.URL + "/ctx-test")
	require.NoError(t, err)
	defer resp.Body.Close()

	// 验证 context 中有 traceID
	assert.NotEmpty(t, ctxTraceID, "context 中应包含 traceID")

	// 验证与响应 header 一致
	headerTraceID := resp.Header.Get(traceIDHeader)
	assert.Equal(t, ctxTraceID, headerTraceID)
}
