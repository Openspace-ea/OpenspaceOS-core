package usage

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName 是 usage 包使用的 OTel meter 名称。
const meterName = "openspace-os/usage"

// Metrics 是用量统计的 OpenTelemetry 指标（T3.3）。
//
// 指标名统一以 openspace_os.usage.* 为前缀，供实时观测与 Prometheus 抓取。
// OTel 未启用时这些 instrument 为 noop 实现，无额外开销。
type Metrics struct {
	// Total 计量记录总数（按 tenant/client/endpoint/operation/unit 维度）。
	Total metric.Int64Counter
	// Bytes 遥测流量字节数（UnitByte 类）。
	Bytes metric.Int64Counter
	// Dropped 因背压丢弃的计量记录数。
	Dropped metric.Int64Counter
}

// NewMetrics 创建并注册用量 metrics。
func NewMetrics() (*Metrics, error) {
	meter := otel.GetMeterProvider().Meter(meterName)
	m := &Metrics{}
	var err error

	m.Total, err = meter.Int64Counter(
		"openspace_os.usage.total",
		metric.WithDescription("用量计量记录总数（按租户/客户端/端点/操作/单位维度）"),
		metric.WithUnit("{count}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 usage.total 失败: %w", err)
	}

	m.Bytes, err = meter.Int64Counter(
		"openspace_os.usage.bytes",
		metric.WithDescription("用量计量遥测字节数"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 usage.bytes 失败: %w", err)
	}

	m.Dropped, err = meter.Int64Counter(
		"openspace_os.usage.dropped",
		metric.WithDescription("因背压丢弃的用量记录数"),
		metric.WithUnit("{count}"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 usage.dropped 失败: %w", err)
	}
	return m, nil
}

// RecordTotal 记录一次计量（按 r.Count 累加；为 0 时当作 1）。
func (m *Metrics) RecordTotal(ctx context.Context, r Record, opts ...metric.AddOption) {
	count := r.Count
	if count <= 0 {
		count = 1
	}
	attrs := metric.WithAttributes(
		attribute.String("tenant_id", r.TenantID),
		attribute.String("client_id", r.ClientID),
		attribute.String("endpoint", r.Endpoint),
		attribute.String("operation", r.Operation),
		attribute.String("unit", string(r.Unit)),
	)
	all := append([]metric.AddOption{attrs}, opts...)
	m.Total.Add(ctx, count, all...)
}

// RecordDropped 记录因背压丢弃的计量条数。
func (m *Metrics) RecordDropped(ctx context.Context, n int64) {
	if n > 0 {
		m.Dropped.Add(ctx, n)
	}
}