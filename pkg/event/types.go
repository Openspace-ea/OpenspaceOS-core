package event

import "time"

// Phase 1 Core 产生的事件类型（共 12 个）。
const (
	// EventTelemetryReceived 收到新的遥测数据。
	EventTelemetryReceived EventType = "TelemetryReceived"
	// EventStateUpdated Node 状态发生变化。
	EventStateUpdated EventType = "StateUpdated"
	// EventNodeRegistered 新 Node 注册。
	EventNodeRegistered EventType = "NodeRegistered"
	// EventNodeUpdated Node 信息更新。
	EventNodeUpdated EventType = "NodeUpdated"
	// EventNodeDeleted Node 删除。
	EventNodeDeleted EventType = "NodeDeleted"
	// EventRelationshipCreated 新关系创建。
	EventRelationshipCreated EventType = "RelationshipCreated"
	// EventRelationshipDeleted 关系删除。
	EventRelationshipDeleted EventType = "RelationshipDeleted"
	// EventTaskStatusChanged 任务状态变更。
	EventTaskStatusChanged EventType = "TaskStatusChanged"
	// EventTaskScheduled 任务调度完成。
	EventTaskScheduled EventType = "TaskScheduled"
	// EventHealthAlarm 健康告警。
	EventHealthAlarm EventType = "HealthAlarm"
	// EventCommandSent 遥控指令已发送。
	EventCommandSent EventType = "CommandSent"
	// EventCommandAcked 遥控指令已确认。
	EventCommandAcked EventType = "CommandAcked"
)

// 外部事件类型（共 2 个），Core 仅校验和流转。
const (
	// EventConjunctionAlert 检测到碰撞预警（商业模块产生）。
	EventConjunctionAlert EventType = "ConjunctionAlert"
	// EventResourceMatchCompleted 资源匹配完成（资源对接平台产生）。
	EventResourceMatchCompleted EventType = "ResourceMatchCompleted"
)

// TelemetryReceivedPayload 是 TelemetryReceived 事件的 Payload。
type TelemetryReceivedPayload struct {
	// SatelliteID 卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// Timestamp 遥测时间戳。
	Timestamp time.Time `json:"timestamp"`
	// Parameters 遥测参数键值对。
	Parameters map[string]any `json:"parameters"`
	// Quality 遥测质量。
	Quality string `json:"quality"`
}

// ConjunctionAlertPayload 是 ConjunctionAlert 事件的 Payload。
type ConjunctionAlertPayload struct {
	// PrimarySatelliteID 主卫星 ID。
	PrimarySatelliteID string `json:"primarySatelliteId"`
	// SecondaryObjectID 次要对象 ID。
	SecondaryObjectID string `json:"secondaryObjectId"`
	// Probability 碰撞概率。
	Probability float64 `json:"probability"`
	// TimeOfClosestApproach 最近接近时刻。
	TimeOfClosestApproach time.Time `json:"timeOfClosestApproach"`
	// ImpactAssessment 影响评估。
	ImpactAssessment ImpactAssessment `json:"impactAssessment"`
}

// ImpactAssessment 碰撞影响评估。
type ImpactAssessment struct {
	// AffectedTasks 受影响的任务 ID 列表。
	AffectedTasks []string `json:"affectedTasks"`
	// RiskLevel 风险等级。
	RiskLevel string `json:"riskLevel"`
}

// ResourceMatchCompletedPayload 是 ResourceMatchCompleted 事件的 Payload。
type ResourceMatchCompletedPayload struct {
	// TaskID 任务 ID。
	TaskID string `json:"taskId"`
	// SatelliteID 卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// ResourceIDs 匹配到的资源 ID 列表。
	ResourceIDs []string `json:"resourceIds"`
	// MatchScore 匹配分数。
	MatchScore float64 `json:"matchScore"`
}

// StateUpdatedPayload 是 StateUpdated 事件的 Payload。
type StateUpdatedPayload struct {
	// NodeID 节点 ID。
	NodeID string `json:"nodeId"`
	// OldStatus 变更前状态。新节点注册时为空。
	OldStatus string `json:"oldStatus,omitempty"`
	// NewStatus 变更后状态。
	NewStatus string `json:"newStatus"`
}

// NodeRegisteredPayload 是 NodeRegistered 事件的 Payload。
type NodeRegisteredPayload struct {
	// NodeID 节点 ID。
	NodeID string `json:"nodeId"`
	// NodeType 节点类型。
	NodeType string `json:"nodeType"`
}

// NodeUpdatedPayload 是 NodeUpdated 事件的 Payload。
type NodeUpdatedPayload struct {
	// NodeID 节点 ID。
	NodeID string `json:"nodeId"`
	// Changes 变更字段键值对。
	Changes map[string]any `json:"changes"`
}

// NodeDeletedPayload 是 NodeDeleted 事件的 Payload。
type NodeDeletedPayload struct {
	// NodeID 节点 ID。
	NodeID string `json:"nodeId"`
}

// RelationshipCreatedPayload 是 RelationshipCreated 事件的 Payload。
type RelationshipCreatedPayload struct {
	// RelID 关系 ID。
	RelID string `json:"relId"`
	// FromNodeID 起始节点 ID。
	FromNodeID string `json:"fromNodeId"`
	// ToNodeID 目标节点 ID。
	ToNodeID string `json:"toNodeId"`
	// RelType 关系类型。
	RelType string `json:"relType"`
}

// RelationshipDeletedPayload 是 RelationshipDeleted 事件的 Payload。
type RelationshipDeletedPayload struct {
	// RelID 关系 ID。
	RelID string `json:"relId"`
}

// TaskStatusChangedPayload 是 TaskStatusChanged 事件的 Payload。
type TaskStatusChangedPayload struct {
	// TaskID 任务 ID。
	TaskID string `json:"taskId"`
	// OldStatus 变更前状态。新任务创建时为空。
	OldStatus string `json:"oldStatus,omitempty"`
	// NewStatus 变更后状态。
	NewStatus string `json:"newStatus"`
}

// TaskScheduledPayload 是 TaskScheduled 事件的 Payload。
type TaskScheduledPayload struct {
	// TaskID 任务 ID。
	TaskID string `json:"taskId"`
	// SatelliteID 卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// ScheduledAt 调度时间。
	ScheduledAt time.Time `json:"scheduledAt"`
}

// HealthAlarmPayload 是 HealthAlarm 事件的 Payload。
type HealthAlarmPayload struct {
	// NodeID 节点 ID。
	NodeID string `json:"nodeId"`
	// Severity 严重程度。
	Severity string `json:"severity"`
	// Message 告警信息。
	Message string `json:"message"`
}

// CommandSentPayload 是 CommandSent 事件的 Payload。
type CommandSentPayload struct {
	// CommandID 指令 ID。
	CommandID string `json:"commandId"`
	// SatelliteID 卫星 ID。
	SatelliteID string `json:"satelliteId"`
	// CommandType 指令类型。
	CommandType string `json:"commandType"`
}

// CommandAckedPayload 是 CommandAcked 事件的 Payload。
type CommandAckedPayload struct {
	// CommandID 指令 ID。
	CommandID string `json:"commandId"`
	// Success 是否成功。
	Success bool `json:"success"`
	// Message 确认信息。
	Message string `json:"message"`
}
