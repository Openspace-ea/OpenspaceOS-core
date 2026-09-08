package event

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventType 表示事件的类型枚举。
type EventType string

// Event 是 Openspace OS 事件总线的通用事件结构。
//
// 对齐《Openspace OS 接口契约文档 v1.0》的事件头定义，
// Payload 字段以 map[string]any 承载，可通过 SetPayload/GetPayload
// 与强类型 Payload 结构体互转。
type Event struct {
	// EventID 事件唯一标识。
	EventID string `json:"eventId"`
	// EventType 事件类型。
	EventType EventType `json:"eventType"`
	// Timestamp 事件产生时间。
	Timestamp time.Time `json:"timestamp"`
	// SourceNodeID 事件来源 Node ID。
	SourceNodeID string `json:"sourceNodeId"`
	// TraceID 链路追踪 ID。
	TraceID string `json:"traceId"`
	// Payload 事件载荷。
	Payload map[string]any `json:"payload"`
}

// SetPayload 将任意 Payload 结构体序列化后写入事件的 Payload 字段。
//
// 传入的 payload 应为事件类型对应的强类型 Payload 结构体（或其指针）。
func (e *Event) SetPayload(payload any) error {
	if payload == nil {
		return fmt.Errorf("payload 不能为 nil")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("解析 payload 为 map 失败: %w", err)
	}
	e.Payload = m
	return nil
}

// GetPayload 将事件 Payload 字段反序列化到目标结构体。
//
// target 应为事件类型对应的强类型 Payload 结构体的指针。
func (e *Event) GetPayload(target any) error {
	if target == nil {
		return fmt.Errorf("target 不能为 nil")
	}
	data, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("反序列化 payload 失败: %w", err)
	}
	return nil
}
