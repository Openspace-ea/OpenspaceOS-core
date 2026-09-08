package core

import (
	"context"
	"errors"

	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// ErrNotFound 表示按主键查询时未找到对应记录。
//
// 上层可通过 errors.Is(err, ErrNotFound) 判断是否为"不存在"场景。
var ErrNotFound = errors.New("记录不存在")

// NodeRepository 是 Node 数据的持久化抽象接口。
//
// 提供 Node 的 CRUD 与列表查询能力。当前提供 SQLite 实现，
// 后续可通过实现此接口替换为其他存储后端。
type NodeRepository interface {
	// Create 创建一个 Node。如果 nodeID 已存在则返回错误。
	Create(ctx context.Context, node *model.Node) error
	// Get 按节点 ID 查询 Node。不存在时返回 ErrNotFound。
	Get(ctx context.Context, nodeID string) (*model.Node, error)
	// Update 更新一个 Node，同时刷新 updated_at。
	// 如果节点不存在则返回 ErrNotFound。
	Update(ctx context.Context, node *model.Node) error
	// Delete 按节点 ID 删除 Node。
	Delete(ctx context.Context, nodeID string) error
	// List 按条件列出 Node，支持类型、社区、状态过滤与分页。
	List(ctx context.Context, opts ListOptions) ([]*model.Node, error)
}

// ListOptions 是 NodeRepository.List 的查询选项。
//
// 各过滤字段为零值时表示不限制。
type ListOptions struct {
	// NodeType 按节点类型过滤，为空表示不限。
	NodeType model.NodeType
	// OwnerCommunityID 按所属社区 ID 过滤，为空表示不限。
	OwnerCommunityID string
	// Status 按状态过滤，为空表示不限。
	Status string
	// Limit 最大返回数量，0 表示使用默认值 100。
	Limit int
	// Offset 分页偏移量。
	Offset int
}

// RelationshipRepository 是关系数据的持久化抽象接口。
//
// 提供关系的增删查与图遍历能力。
type RelationshipRepository interface {
	// Create 创建一条关系。校验 from/to 节点是否存在。
	Create(ctx context.Context, rel *model.Relationship) error
	// Delete 按关系 ID 删除关系。
	Delete(ctx context.Context, relID string) error
	// ListOutgoing 列出指定节点的出边关系。
	// relType 为空时表示不限关系类型。
	ListOutgoing(ctx context.Context, nodeID string, relType model.RelationshipType) ([]*model.Relationship, error)
	// ListIncoming 列出指定节点的入边关系。
	// relType 为空时表示不限关系类型。
	ListIncoming(ctx context.Context, nodeID string, relType model.RelationshipType) ([]*model.Relationship, error)
	// Traverse 从起始节点出发进行 BFS 图遍历，返回遍历过程中经过的所有关系。
	Traverse(ctx context.Context, startNodeID string, opts TraverseOptions) ([]*model.Relationship, error)
}

// TraverseOptions 是图遍历的选项。
type TraverseOptions struct {
	// RelType 限定关系类型，为空表示不限。
	RelType model.RelationshipType
	// Direction 遍历方向："out"、"in"、"both"，默认 "out"。
	Direction string
	// MaxDepth 最大遍历深度，默认 1。
	MaxDepth int
}
