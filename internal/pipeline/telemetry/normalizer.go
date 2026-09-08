package telemetry

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// Normalize 将 TelemetryFrame 标准化为 TelemetryReceived 事件。
//
// 转换流程：
//  1. 生成唯一 EventID（UUID）
//  2. 设置 EventType = EventTelemetryReceived
//  3. Timestamp 取当前时间（事件产生时间）
//  4. SourceNodeID = frame.SatelliteID
//  5. Payload 设置为 TelemetryReceivedPayload
//  6. 在 Payload 中附加 logicalShard 分片信息（shardKey = satelliteId）
//
// communityId 当前未接入 KG 查询，留空；后续可扩展注入 KGService 填充。
func Normalize(frame *plugin.TelemetryFrame) (*event.Event, error) {
	if frame == nil {
		return nil, fmt.Errorf("frame 不能为 nil")
	}
	if frame.SatelliteID == "" {
		return nil, fmt.Errorf("frame.SatelliteID 不能为空")
	}

	payload := event.TelemetryReceivedPayload{
		SatelliteID: frame.SatelliteID,
		Timestamp:   frame.Timestamp,
		Parameters:  frame.Parameters,
		Quality:     frame.Quality,
	}
	if payload.Quality == "" {
		payload.Quality = "unknown"
	}
	// 确保 Parameters 非 nil，避免 Schema 校验失败
	if payload.Parameters == nil {
		payload.Parameters = map[string]any{}
	}

	e := &event.Event{
		EventID:      uuid.NewString(),
		EventType:    event.EventTelemetryReceived,
		Timestamp:    time.Now(),
		SourceNodeID: frame.SatelliteID,
	}
	if err := e.SetPayload(payload); err != nil {
		return nil, fmt.Errorf("设置事件 payload 失败: %w", err)
	}

	// 附加逻辑分片信息（以 map 形式存储，保持 Payload 类型一致性）
	e.Payload["logicalShard"] = map[string]any{
		"satelliteId": frame.SatelliteID,
		"communityId": "", // 暂未接入 KG，留空
	}

	return e, nil
}
