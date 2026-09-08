package model

import "time"

// NodeType 表示 Node 的类型枚举。
type NodeType string

const (
	// NodeTypeSatellite 卫星节点。
	NodeTypeSatellite NodeType = "Satellite"
	// NodeTypeGroundStation 地面站节点。
	NodeTypeGroundStation NodeType = "GroundStation"
	// NodeTypeTask 任务节点。
	NodeTypeTask NodeType = "Task"
	// NodeTypeCommunity 社区节点。
	NodeTypeCommunity NodeType = "Community"
	// NodeTypeMission 任务编排节点。
	NodeTypeMission NodeType = "Mission"

	// 以下为预留类型，供后续阶段扩展使用。

	// NodeTypeAntenna 天线节点。
	NodeTypeAntenna NodeType = "Antenna"
	// NodeTypePayload 有效载荷节点。
	NodeTypePayload NodeType = "Payload"
	// NodeTypeTelemetry 遥测节点。
	NodeTypeTelemetry NodeType = "Telemetry"
	// NodeTypeCommand 遥控指令节点。
	NodeTypeCommand NodeType = "Command"
	// NodeTypeAlarm 告警节点。
	NodeTypeAlarm NodeType = "Alarm"
)

// Node 是 Openspace OS 通用节点基础结构体，对齐《Openspace OS 接口契约文档 v1.0》的通用属性。
//
// 各具体 Node 类型（Satellite、GroundStation 等）的特有属性通过 Properties 字段承载，
// 并由对应的 *Properties 结构体进行强类型化封装。
type Node struct {
	// NodeID 全局唯一标识，必填。
	NodeID string `json:"nodeId"`
	// NodeType Node 类型，必填。
	NodeType NodeType `json:"nodeType"`
	// Name 名称，必填。
	Name string `json:"name"`
	// Status 当前状态，必填。
	Status string `json:"status"`
	// OwnerCommunityID 所属 Community ID，必填。
	OwnerCommunityID string `json:"ownerCommunityId"`
	// CreatedAt 创建时间，必填。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 更新时间，必填。
	UpdatedAt time.Time `json:"updatedAt"`
	// ShardKey 分片键，选填（预留）。
	ShardKey string `json:"shardKey,omitempty"`
	// FederationID 联邦标识，选填（预留）。
	FederationID string `json:"federationId,omitempty"`
	// Properties 类型特有属性的扩展字段。
	Properties map[string]any `json:"properties,omitempty"`
}

// Valid 判断 NodeType 是否为已定义的枚举值。
func (nt NodeType) Valid() bool {
	switch nt {
	case NodeTypeSatellite,
		NodeTypeGroundStation,
		NodeTypeTask,
		NodeTypeCommunity,
		NodeTypeMission,
		NodeTypeAntenna,
		NodeTypePayload,
		NodeTypeTelemetry,
		NodeTypeCommand,
		NodeTypeAlarm:
		return true
	}
	return false
}
