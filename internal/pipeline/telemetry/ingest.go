package telemetry

import (
	"context"
	"fmt"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// batchFlushThreshold 是 IngestFrames 单次批量发布脚本的帧数阈值。
//
// 语义上用于 gRPC 客户端流等场景的"批量写放大"控制：达到该阈值即 flush 一次，
// 避免一次性累积过多导致内存或阻塞（T5.4）。
const batchFlushThreshold = 512

// IngestFrames 批量接收遥测帧并发布为事件（T5.1/5.2/5.4 批量入口）。
//
// 逐帧执行：标准化（Normalize）→ 发布到总线（Publish，内含 Schema 校验）。
// 单帧失败不中断，继续处理后续帧；仅累加 DropCount 指标。
// 返回成功发布的帧数；若所有帧均失败返回错误。
func (p *Pipeline) IngestFrames(ctx context.Context, frames []plugin.TelemetryFrame) (int, error) {
	if len(frames) == 0 {
		return 0, fmt.Errorf("未提供遥测帧")
	}
	accepted := 0
	for i := range frames {
		e, err := Normalize(&frames[i])
		if err != nil {
			p.logger.Error("遥测帧标准化失败", "index", i, "satelliteId", frames[i].SatelliteID, "error", err)
			p.metrics.DropCount.Add(1)
			continue
		}
		if err := p.bus.Publish(ctx, e); err != nil {
			p.logger.Error("遥测事件发布失败", "eventId", e.EventID, "error", err)
			p.metrics.DropCount.Add(1)
			continue
		}
		accepted++
		p.logger.Debug("遥测事件已发布", "eventId", e.EventID, "satelliteId", frames[i].SatelliteID)
	}
	if accepted == 0 {
		return 0, fmt.Errorf("没有可接受的遥测帧")
	}
	return accepted, nil
}