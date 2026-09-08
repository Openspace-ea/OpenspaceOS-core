package model

import (
	"errors"
	"fmt"
)

// Node 与 Relationship 校验过程中使用的哨兵错误。
var (
	errNodeIDEmpty         = errors.New("nodeId 不能为空")
	errNodeTypeEmpty       = errors.New("nodeType 不能为空")
	errNodeNameEmpty       = errors.New("name 不能为空")
	errNodeStatusEmpty     = errors.New("status 不能为空")
	errOwnerCommunityEmpty = errors.New("ownerCommunityId 不能为空")
	errCreatedAtZero       = errors.New("createdAt 不能为零值")
	errUpdatedAtZero       = errors.New("updatedAt 不能为零值")

	errRelIDEmpty      = errors.New("relId 不能为空")
	errFromNodeIDEmpty = errors.New("fromNodeId 不能为空")
	errToNodeIDEmpty   = errors.New("toNodeId 不能为空")
)

// errNodeTypeUnknown 返回表示未知 nodeType 的错误。
func errNodeTypeUnknown(nt NodeType) error {
	return fmt.Errorf("未知的 nodeType: %s", nt)
}

// errRelTypeUnknown 返回表示未知 relType 的错误。
func errRelTypeUnknown(rt RelationshipType) error {
	return fmt.Errorf("未知的 relType: %s", rt)
}

// Validate 校验 Node 的必填字段与类型有效性。
//
// 必填字段：nodeId、nodeType、name、status、ownerCommunityId。
// createdAt/updatedAt 在服务层设置，这里只校验非零。
// nodeType 必须为已定义的枚举值。
func (n *Node) Validate() error {
	if n.NodeID == "" {
		return errNodeIDEmpty
	}
	if n.NodeType == "" {
		return errNodeTypeEmpty
	}
	if !n.NodeType.Valid() {
		return errNodeTypeUnknown(n.NodeType)
	}
	if n.Name == "" {
		return errNodeNameEmpty
	}
	if n.Status == "" {
		return errNodeStatusEmpty
	}
	if n.OwnerCommunityID == "" {
		return errOwnerCommunityEmpty
	}
	if n.CreatedAt.IsZero() {
		return errCreatedAtZero
	}
	if n.UpdatedAt.IsZero() {
		return errUpdatedAtZero
	}
	return nil
}
