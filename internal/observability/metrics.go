package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// meterName 是 observability 包使用的 meter 名称。
const meterName = "github.com/openspace-os/openspace-os-core/observability"

// CoreMetrics 定义 Openspace OS Core 的核心可观测性指标。
//
// 所有指标均通过 OTel Meter API 创建。当 OTel 未启用时，
// 全局 MeterProvider 为 noop，创建的 instrument 为 noop 实现，
// 调用 Add/Record 等方法为空操作，不会产生性能开销。
type CoreMetrics struct {
	// EventPublishQPS 事件发布计数器。
	EventPublishQPS metric.Int64Counter
	// EventDispatchLatency 事件派发耗时直方图（秒）。
	EventDispatchLatency metric.Float64Histogram
	// TelemetryIngressQPS 遥测入口消息计数器。
	TelemetryIngressQPS metric.Int64Counter
	// TelemetryParseLatency 遥测解析耗时直方图（秒）。
	TelemetryParseLatency metric.Float64Histogram
	// CommandSendLatency 指令发送耗时直方图（秒）。
	CommandSendLatency metric.Float64Histogram
	// CommandSuccessTotal 指令成功计数器。
	CommandSuccessTotal metric.Int64Counter
	// CommandTotal 指令总发送计数器。
	CommandTotal metric.Int64Counter
	// PluginLoadCount 插件加载数量增减计数器。
	PluginLoadCount metric.Int64UpDownCounter
}

// NewCoreMetrics 创建并注册核心 metrics 到全局 MeterProvider。
//
// 返回的 CoreMetrics 中的所有 instrument 已注册完成，
// 调用方可直接使用 Add/Record 方法记录指标。
func NewCoreMetrics() (*CoreMetrics, error) {
	meter := otel.GetMeterProvider().Meter(meterName)

	cm := &CoreMetrics{}
	var err error

	// 事件发布 QPS 计数器
	cm.EventPublishQPS, err = meter.Int64Counter(
		"openspace_os.event.publish.count",
		metric.WithDescription("事件发布总数"),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 EventPublishQPS 失败: %w", err)
	}

	// 事件派发延迟直方图
	cm.EventDispatchLatency, err = meter.Float64Histogram(
		"openspace_os.event.dispatch.latency",
		metric.WithDescription("事件派发耗时"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 EventDispatchLatency 失败: %w", err)
	}

	// 遥测入口 QPS 计数器
	cm.TelemetryIngressQPS, err = meter.Int64Counter(
		"openspace_os.telemetry.ingress.count",
		metric.WithDescription("遥测入口消息总数"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 TelemetryIngressQPS 失败: %w", err)
	}

	// 遥测解析延迟直方图
	cm.TelemetryParseLatency, err = meter.Float64Histogram(
		"openspace_os.telemetry.parse.latency",
		metric.WithDescription("遥测解析耗时"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 TelemetryParseLatency 失败: %w", err)
	}

	// 指令发送延迟直方图
	cm.CommandSendLatency, err = meter.Float64Histogram(
		"openspace_os.command.send.latency",
		metric.WithDescription("指令发送耗时"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 CommandSendLatency 失败: %w", err)
	}

	// 指令成功计数器
	cm.CommandSuccessTotal, err = meter.Int64Counter(
		"openspace_os.command.success.count",
		metric.WithDescription("指令成功发送总数"),
		metric.WithUnit("{command}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 CommandSuccessTotal 失败: %w", err)
	}

	// 指令总发送计数器
	cm.CommandTotal, err = meter.Int64Counter(
		"openspace_os.command.total.count",
		metric.WithDescription("指令发送总数（含成功与失败）"),
		metric.WithUnit("{command}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 CommandTotal 失败: %w", err)
	}

	// 插件加载数量增减计数器
	cm.PluginLoadCount, err = meter.Int64UpDownCounter(
		"openspace_os.plugin.load.count",
		metric.WithDescription("当前已加载插件数量"),
		metric.WithUnit("{plugin}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 PluginLoadCount 失败: %w", err)
	}

	return cm, nil
}

// RecordCommandResult 记录一次指令发送结果（成功或失败）。
//
// 同时更新 CommandTotal 与 CommandSuccessTotal（仅成功时）。
func (cm *CoreMetrics) RecordCommandResult(ctx context.Context, success bool, attrs ...metric.AddOption) {
	cm.CommandTotal.Add(ctx, 1, attrs...)
	if success {
		cm.CommandSuccessTotal.Add(ctx, 1, attrs...)
	}
}
