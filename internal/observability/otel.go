package observability

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// OtelConfig 定义 OpenTelemetry 初始化所需的配置。
type OtelConfig struct {
	// Enabled 是否启用 OTel 可观测性导出。
	Enabled bool
	// Endpoint OTLP gRPC 端点地址，如 "localhost:4317"。
	Endpoint string
	// ServiceName 服务名称，用于标识本服务在链路追踪中的来源。
	ServiceName string
}

// 默认 OTLP 导出超时时间。
const defaultExportTimeout = 10 * time.Second

// Init 初始化 OpenTelemetry TracerProvider 与 MeterProvider。
//
// 当 Enabled 为 true 时，创建 OTLP gRPC exporter 并注册全局 Provider；
// 当 Enabled 为 false 时，不进行任何网络初始化，仅确保全局 Provider 为 noop，
// traceIDFromContext 等函数仍可安全调用（返回空 traceID）。
//
// 返回的 shutdown 函数用于优雅关闭所有 exporter 与 provider。
// 若初始化过程中发生错误，已创建的资源会被清理，并降级为 noop 模式（不返回错误，
// 仅记录日志），以保证服务启动不被 OTel 初始化失败阻断。
func Init(providerConfig OtelConfig) (shutdown func() error, err error) {
	// 空的 shutdown 函数，用于 disabled 或降级场景
	noopShutdown := func() error { return nil }

	if !providerConfig.Enabled {
		// 未启用时，确保全局使用 noop provider，不产生任何网络调用
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		otel.SetMeterProvider(metric.NewMeterProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return noopShutdown, nil
	}

	serviceName := providerConfig.ServiceName
	if serviceName == "" {
		serviceName = "openspace-os-core"
	}

	// 构建 Resource，标识本服务的名称与语义属性
	// 使用空 schema URL 避免与 resource.Default() 的 schema URL 冲突
	res, resErr := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("", semconv.ServiceName(serviceName)),
	)
	if resErr != nil {
		// Resource 构建失败不应阻断启动，降级为 noop
		slog.Warn("构建 OTel Resource 失败，降级为 noop 模式", "error", resErr)
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		otel.SetMeterProvider(metric.NewMeterProvider())
		return noopShutdown, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultExportTimeout)
	defer cancel()

	// 创建 Trace OTLP gRPC exporter
	traceExporter, traceErr := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(providerConfig.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if traceErr != nil {
		// Trace exporter 创建失败，降级为 noop tracer，但继续尝试初始化 meter
		slog.Warn("创建 OTel Trace exporter 失败，降级为 noop tracer", "error", traceErr, "endpoint", providerConfig.Endpoint)
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	} else {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
	}

	// 创建 Metric OTLP gRPC exporter
	metricExporter, metricErr := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(providerConfig.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if metricErr != nil {
		// Metric exporter 创建失败，降级为 noop meter
		slog.Warn("创建 OTel Metric exporter 失败，降级为 noop meter", "error", metricErr, "endpoint", providerConfig.Endpoint)
		otel.SetMeterProvider(metric.NewMeterProvider())
	} else {
		mp := metric.NewMeterProvider(
			metric.WithReader(metric.NewPeriodicReader(metricExporter,
				metric.WithInterval(30*time.Second),
			)),
			metric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
	}

	// 设置全局 TextMapPropagator，支持 W3C TraceContext + Baggage 透传
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// 构建 shutdown 函数，优雅关闭所有 provider 与 exporter
	shutdown = func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultExportTimeout)
		defer shutdownCancel()

		var traceErr, metricErr error
		tp := otel.GetTracerProvider()
		if closer, ok := tp.(interface{ Shutdown(context.Context) error }); ok {
			traceErr = closer.Shutdown(shutdownCtx)
		}
		mp := otel.GetMeterProvider()
		if closer, ok := mp.(interface{ Shutdown(context.Context) error }); ok {
			metricErr = closer.Shutdown(shutdownCtx)
		}
		if traceErr != nil && metricErr != nil {
			return fmt.Errorf("trace shutdown: %w; metric shutdown: %v", traceErr, metricErr)
		}
		if traceErr != nil {
			return fmt.Errorf("trace shutdown: %w", traceErr)
		}
		if metricErr != nil {
			return fmt.Errorf("metric shutdown: %w", metricErr)
		}
		return nil
	}

	return shutdown, nil
}

