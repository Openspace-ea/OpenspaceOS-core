package usage

import (
	"context"
	"time"
)

// RecordIngest 记录一次遥测上报的用量计量（T5.6）。
//
// 同时计量帧数（UnitFrame）与字节数（UnitByte）两类最小单位；
// 主体（tenant/client）从 context 的 Claims 解析（未认证时归匿名档）。
// collector 或 metrics 为 nil 时对应能力自动跳过。
func RecordIngest(ctx context.Context, collector *Collector, metrics *Metrics, endpoint, operation string, frames, bytes int64) {
	if (collector == nil && metrics == nil) || (frames <= 0 && bytes <= 0) {
		return
	}
	tenantID, clientID := resolveTenant(ctx)
	at := time.Now()

	if frames > 0 {
		rc := Record{
			TenantID:  tenantID,
			ClientID:  clientID,
			Endpoint:  endpoint,
			Operation: operation,
			Unit:      UnitFrame,
			Count:     frames,
			At:        at,
		}
		if collector != nil {
			_ = collector.Record(ctx, rc)
		}
		if metrics != nil {
			metrics.RecordTotal(ctx, rc)
		}
	}
	if bytes > 0 {
		rc := Record{
			TenantID:  tenantID,
			ClientID:  clientID,
			Endpoint:  endpoint,
			Operation: operation,
			Unit:      UnitByte,
			Count:     bytes,
			At:        at,
		}
		if collector != nil {
			_ = collector.Record(ctx, rc)
		}
		if metrics != nil {
			metrics.RecordTotal(ctx, rc)
		}
	}
}