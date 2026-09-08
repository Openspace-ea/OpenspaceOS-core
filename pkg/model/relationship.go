package model

import "time"

// RelationshipType 表示 Node 之间关系的类型枚举。
type RelationshipType string

const (
	// RelBelongsTo 归属关系，Satellite -> Community。
	RelBelongsTo RelationshipType = "belongsTo"
	// RelControlledBy 受控关系，Satellite -> GroundStation。
	RelControlledBy RelationshipType = "controlledBy"
	// RelOwns 拥有关系，GroundStation -> Antenna / Community -> Node。
	RelOwns RelationshipType = "owns"
	// RelProvidesServiceTo 提供服务关系，GroundStation -> Satellite。
	RelProvidesServiceTo RelationshipType = "providesServiceTo"
	// RelExecutedBy 执行关系，Task -> Satellite。
	RelExecutedBy RelationshipType = "executedBy"
	// RelUsesResource 使用资源关系，Task -> GroundStation。
	RelUsesResource RelationshipType = "usesResource"
	// RelMemberOf 成员关系，Community -> Community。
	RelMemberOf RelationshipType = "memberOf"
	// RelContains 包含关系，Mission -> Task。
	RelContains RelationshipType = "contains"
	// RelTargets 目标关系，Mission -> Satellite。
	RelTargets RelationshipType = "targets"
)

// Relationship 表示两个 Node 之间的关系。
type Relationship struct {
	// RelID 关系唯一标识。
	RelID string `json:"relId"`
	// FromNodeID 起始 Node ID。
	FromNodeID string `json:"fromNodeId"`
	// ToNodeID 目标 Node ID。
	ToNodeID string `json:"toNodeId"`
	// RelType 关系类型。
	RelType RelationshipType `json:"relType"`
	// Properties 关系特有属性的扩展字段。
	Properties map[string]any `json:"properties,omitempty"`
	// CreatedAt 创建时间。
	CreatedAt time.Time `json:"createdAt"`
}

// Valid 判断 RelationshipType 是否为已定义的枚举值。
func (rt RelationshipType) Valid() bool {
	switch rt {
	case RelBelongsTo,
		RelControlledBy,
		RelOwns,
		RelProvidesServiceTo,
		RelExecutedBy,
		RelUsesResource,
		RelMemberOf,
		RelContains,
		RelTargets:
		return true
	}
	return false
}

// Validate 校验 Relationship 的必填字段与关系类型有效性。
func (r *Relationship) Validate() error {
	if r.RelID == "" {
		return errRelIDEmpty
	}
	if r.FromNodeID == "" {
		return errFromNodeIDEmpty
	}
	if r.ToNodeID == "" {
		return errToNodeIDEmpty
	}
	if !r.RelType.Valid() {
		return errRelTypeUnknown(r.RelType)
	}
	if r.CreatedAt.IsZero() {
		return errCreatedAtZero
	}
	return nil
}
