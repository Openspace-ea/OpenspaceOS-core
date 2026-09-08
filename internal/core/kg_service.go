package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// HookTrigger 定义 Node 生命周期钩子触发接口。
//
// plugin.Manager 实现了此接口，KGService 在 Node 变更时调用对应方法，
// 使已注册的 NodeLifecycleHook 插件能够感知节点变化。
type HookTrigger interface {
	TriggerNodeRegistered(node *model.Node)
	TriggerNodeUpdated(node *model.Node, changes map[string]any)
	TriggerNodeDeleted(nodeID string)
}

// KGService 是 Knowledge Graph 的领域服务。
//
// 封装 Node 与 Relationship 的业务逻辑，并在变更时自动发布事件到 MessageBus。
type KGService struct {
	nodeRepo    NodeRepository
	relRepo     RelationshipRepository
	bus         MessageBus
	logger      *slog.Logger
	hookTrigger HookTrigger // 可选：插件生命周期钩子触发器
	db          *sql.DB     // 可选：用于跨仓储事务
	rebind      rebindFunc  // 事务 SQL 的占位符适配，默认 SQLite 原样
}

// NewKGService 创建 KG Service。
//
// logger 为 nil 时使用 slog.Default()。
func NewKGService(nodeRepo NodeRepository, relRepo RelationshipRepository, bus MessageBus, logger *slog.Logger) *KGService {
	if logger == nil {
		logger = slog.Default()
	}
	return &KGService{
		nodeRepo: nodeRepo,
		relRepo:  relRepo,
		bus:      bus,
		logger:   logger,
		rebind:   rebindIdentity,
	}
}

// SetHookTrigger 注入插件生命周期钩子触发器。
//
// 注入后，KGService 在 Node 注册/更新/删除时会调用对应触发方法，
// 使已注册的 NodeLifecycleHook 插件能够感知节点变化。
func (s *KGService) SetHookTrigger(ht HookTrigger) {
	s.hookTrigger = ht
}

// SetDB 注入数据库连接，用于跨仓储事务操作（如 DeleteNode 时级联删除关系）。
//
// 未注入时 DeleteNode 退化为非事务模式（先删关系再删节点）。
func (s *KGService) SetDB(db *sql.DB) {
	s.db = db
}

// SetRebind 设置事务 SQL 的占位符适配函数（默认 SQLite 原样）。
//
// PostgreSQL 场景应传入 rebindPostgres，使事务中使用的 DELETE 语句占位符匹配 PG。
func (s *KGService) SetRebind(fn rebindFunc) {
	if fn != nil {
		s.rebind = fn
	}
}

// RegisterNode 注册一个新 Node。
//
// 流程：设置时间戳 → 校验 → 持久化 → 发布 NodeRegistered 与 StateUpdated 事件。
func (s *KGService) RegisterNode(ctx context.Context, node *model.Node) error {
	if node == nil {
		return fmt.Errorf("node 不能为 nil")
	}

	// 服务层负责设置时间戳；Validate 要求 createdAt/updatedAt 非零。
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now

	if err := node.Validate(); err != nil {
		return fmt.Errorf("节点校验失败: %w", err)
	}

	if err := s.nodeRepo.Create(ctx, node); err != nil {
		return fmt.Errorf("创建节点失败: %w", err)
	}

	// 发布 NodeRegistered 事件
	if err := s.publishEvent(ctx, event.EventNodeRegistered, node.NodeID,
		event.NodeRegisteredPayload{NodeID: node.NodeID, NodeType: string(node.NodeType)}); err != nil {
		s.logger.Error("发布 NodeRegistered 事件失败", "nodeId", node.NodeID, "error", err)
		return fmt.Errorf("发布 NodeRegistered 事件失败: %w", err)
	}

	// 发布 StateUpdated 事件（新建节点 OldStatus 为空）
	if err := s.publishEvent(ctx, event.EventStateUpdated, node.NodeID,
		event.StateUpdatedPayload{NodeID: node.NodeID, OldStatus: "", NewStatus: node.Status}); err != nil {
		s.logger.Error("发布 StateUpdated 事件失败", "nodeId", node.NodeID, "error", err)
		return fmt.Errorf("发布 StateUpdated 事件失败: %w", err)
	}

	// 触发插件生命周期钩子
	if s.hookTrigger != nil {
		s.hookTrigger.TriggerNodeRegistered(node)
	}

	return nil
}

// UpdateNode 按变更字段更新 Node。
//
// 流程：获取现有节点 → 应用变更 → 持久化 → 发布 StateUpdated（如状态变化）与 NodeUpdated 事件。
// 返回更新后的 Node。
func (s *KGService) UpdateNode(ctx context.Context, nodeID string, changes map[string]any) (*model.Node, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("changes 不能为空")
	}

	node, err := s.nodeRepo.Get(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("获取节点失败: %w", err)
	}

	oldStatus := node.Status
	applyChanges(node, changes)
	node.UpdatedAt = time.Now()

	if err := s.nodeRepo.Update(ctx, node); err != nil {
		return nil, fmt.Errorf("更新节点失败: %w", err)
	}

	// 状态变化时发布 StateUpdated 事件
	if node.Status != oldStatus {
		if err := s.publishEvent(ctx, event.EventStateUpdated, nodeID,
			event.StateUpdatedPayload{NodeID: nodeID, OldStatus: oldStatus, NewStatus: node.Status}); err != nil {
			s.logger.Error("发布 StateUpdated 事件失败", "nodeId", nodeID, "error", err)
			return nil, fmt.Errorf("发布 StateUpdated 事件失败: %w", err)
		}
	}

	// 发布 NodeUpdated 事件
	if err := s.publishEvent(ctx, event.EventNodeUpdated, nodeID,
		event.NodeUpdatedPayload{NodeID: nodeID, Changes: changes}); err != nil {
		s.logger.Error("发布 NodeUpdated 事件失败", "nodeId", nodeID, "error", err)
		return nil, fmt.Errorf("发布 NodeUpdated 事件失败: %w", err)
	}

	// 触发插件生命周期钩子
	if s.hookTrigger != nil {
		s.hookTrigger.TriggerNodeUpdated(node, changes)
	}

	return node, nil
}

// DeleteNode 删除 Node，同时清理该节点相关的所有关系。
//
// 流程：删除相关关系 → 删除节点 → 发布 NodeDeleted 事件 → 触发钩子。
// 若注入了 *sql.DB，关系删除与节点删除在同一个数据库事务中执行，保证原子性。
func (s *KGService) DeleteNode(ctx context.Context, nodeID string) error {
	if s.db != nil {
		return s.deleteNodeInTx(ctx, nodeID)
	}
	return s.deleteNodeNoTx(ctx, nodeID)
}

// deleteNodeInTx 在事务中删除节点及其所有关系。
func (s *KGService) deleteNodeInTx(ctx context.Context, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 在事务中删除出边和入边关系
	if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM relationships WHERE from_node_id = ? OR to_node_id = ?"), nodeID, nodeID); err != nil {
		return fmt.Errorf("事务删除关系失败: %w", err)
	}

	// 在事务中删除节点
	res, err := tx.ExecContext(ctx, s.rebind("DELETE FROM nodes WHERE node_id = ?"), nodeID)
	if err != nil {
		return fmt.Errorf("事务删除节点失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: nodeId=%s", ErrNotFound, nodeID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	// 发布 NodeDeleted 事件
	if err := s.publishEvent(ctx, event.EventNodeDeleted, nodeID,
		event.NodeDeletedPayload{NodeID: nodeID}); err != nil {
		s.logger.Error("发布 NodeDeleted 事件失败", "nodeId", nodeID, "error", err)
		return fmt.Errorf("发布 NodeDeleted 事件失败: %w", err)
	}

	// 触发插件生命周期钩子
	if s.hookTrigger != nil {
		s.hookTrigger.TriggerNodeDeleted(nodeID)
	}

	return nil
}

// deleteNodeNoTx 非事务模式删除节点（回退方案，用于未注入 db 的场景）。
func (s *KGService) deleteNodeNoTx(ctx context.Context, nodeID string) error {
	// 删除相关关系（先查询再逐条删除）
	if err := s.deleteRelationshipsOfNode(ctx, nodeID); err != nil {
		return err
	}

	if err := s.nodeRepo.Delete(ctx, nodeID); err != nil {
		return fmt.Errorf("删除节点失败: %w", err)
	}

	if err := s.publishEvent(ctx, event.EventNodeDeleted, nodeID,
		event.NodeDeletedPayload{NodeID: nodeID}); err != nil {
		s.logger.Error("发布 NodeDeleted 事件失败", "nodeId", nodeID, "error", err)
		return fmt.Errorf("发布 NodeDeleted 事件失败: %w", err)
	}

	// 触发插件生命周期钩子
	if s.hookTrigger != nil {
		s.hookTrigger.TriggerNodeDeleted(nodeID)
	}

	return nil
}

// GetNode 查询单个 Node。
func (s *KGService) GetNode(ctx context.Context, nodeID string) (*model.Node, error) {
	return s.nodeRepo.Get(ctx, nodeID)
}

// ListNodes 按条件列出 Node。
func (s *KGService) ListNodes(ctx context.Context, opts ListOptions) ([]*model.Node, error) {
	return s.nodeRepo.List(ctx, opts)
}

// CreateRelationship 创建一条关系。
//
// 校验关系合法性，并校验 from/to 节点存在后持久化，然后发布 RelationshipCreated 事件。
func (s *KGService) CreateRelationship(ctx context.Context, rel *model.Relationship) error {
	if rel == nil {
		return fmt.Errorf("rel 不能为 nil")
	}

	// 服务层负责设置创建时间；Validate 要求 createdAt 非零。
	if rel.CreatedAt.IsZero() {
		rel.CreatedAt = time.Now()
	}

	if err := rel.Validate(); err != nil {
		return fmt.Errorf("关系校验失败: %w", err)
	}

	// 校验 from/to 节点存在
	if _, err := s.nodeRepo.Get(ctx, rel.FromNodeID); err != nil {
		return fmt.Errorf("起始节点不存在: %w", err)
	}
	if _, err := s.nodeRepo.Get(ctx, rel.ToNodeID); err != nil {
		return fmt.Errorf("目标节点不存在: %w", err)
	}

	if err := s.relRepo.Create(ctx, rel); err != nil {
		return fmt.Errorf("创建关系失败: %w", err)
	}

	// 发布 RelationshipCreated 事件
	if err := s.publishEvent(ctx, event.EventRelationshipCreated, rel.FromNodeID,
		event.RelationshipCreatedPayload{
			RelID:      rel.RelID,
			FromNodeID: rel.FromNodeID,
			ToNodeID:   rel.ToNodeID,
			RelType:    string(rel.RelType),
		}); err != nil {
		s.logger.Error("发布 RelationshipCreated 事件失败", "relId", rel.RelID, "error", err)
		return fmt.Errorf("发布 RelationshipCreated 事件失败: %w", err)
	}

	return nil
}

// DeleteRelationship 删除一条关系，并发布 RelationshipDeleted 事件。
func (s *KGService) DeleteRelationship(ctx context.Context, relID string) error {
	if err := s.relRepo.Delete(ctx, relID); err != nil {
		return fmt.Errorf("删除关系失败: %w", err)
	}

	// 发布 RelationshipDeleted 事件
	if err := s.publishEvent(ctx, event.EventRelationshipDeleted, relID,
		event.RelationshipDeletedPayload{RelID: relID}); err != nil {
		s.logger.Error("发布 RelationshipDeleted 事件失败", "relId", relID, "error", err)
		return fmt.Errorf("发布 RelationshipDeleted 事件失败: %w", err)
	}

	return nil
}

// QueryRelationships 按方向查询节点的关系。
//
// direction: "out" 查出边，"in" 查入边，"both" 查双向。
func (s *KGService) QueryRelationships(ctx context.Context, nodeID string, direction string, relType model.RelationshipType) ([]*model.Relationship, error) {
	switch direction {
	case "in":
		return s.relRepo.ListIncoming(ctx, nodeID, relType)
	case "both":
		out, err := s.relRepo.ListOutgoing(ctx, nodeID, relType)
		if err != nil {
			return nil, err
		}
		in, err := s.relRepo.ListIncoming(ctx, nodeID, relType)
		if err != nil {
			return nil, err
		}
		return append(out, in...), nil
	default: // "out"
		return s.relRepo.ListOutgoing(ctx, nodeID, relType)
	}
}

// TraverseGraph 从起始节点出发进行 BFS 图遍历。
func (s *KGService) TraverseGraph(ctx context.Context, startNodeID string, opts TraverseOptions) ([]*model.Relationship, error) {
	return s.relRepo.Traverse(ctx, startNodeID, opts)
}

// deleteRelationshipsOfNode 删除与节点相关的所有关系（出边与入边）。
func (s *KGService) deleteRelationshipsOfNode(ctx context.Context, nodeID string) error {
	out, err := s.relRepo.ListOutgoing(ctx, nodeID, "")
	if err != nil {
		return fmt.Errorf("查询出边关系失败: %w", err)
	}
	in, err := s.relRepo.ListIncoming(ctx, nodeID, "")
	if err != nil {
		return fmt.Errorf("查询入边关系失败: %w", err)
	}
	for _, rel := range out {
		if err := s.relRepo.Delete(ctx, rel.RelID); err != nil {
			return fmt.Errorf("删除关系失败 (relId=%s): %w", rel.RelID, err)
		}
	}
	for _, rel := range in {
		if err := s.relRepo.Delete(ctx, rel.RelID); err != nil {
			return fmt.Errorf("删除关系失败 (relId=%s): %w", rel.RelID, err)
		}
	}
	return nil
}

// publishEvent 发布一个事件到事件总线。
//
// 生成 EventID、设置 Timestamp、从 ctx 提取 traceId、填充 payload 后发布。
func (s *KGService) publishEvent(ctx context.Context, eventType event.EventType, sourceNodeID string, payload any) error {
	e := &event.Event{
		EventID:      uuid.NewString(),
		EventType:    eventType,
		Timestamp:    time.Now(),
		SourceNodeID: sourceNodeID,
		TraceID:      traceIDFromContext(ctx),
	}
	if err := e.SetPayload(payload); err != nil {
		return err
	}
	return s.bus.Publish(ctx, e)
}

// applyChanges 将 changes map 应用到 Node 的对应字段。
//
// 支持的 key（使用 JSON tag 命名风格）：name、status、shardKey、federationId、properties。
// 未知 key 被忽略。
func applyChanges(node *model.Node, changes map[string]any) {
	for k, v := range changes {
		switch k {
		case "name":
			if s, ok := v.(string); ok {
				node.Name = s
			}
		case "status":
			if s, ok := v.(string); ok {
				node.Status = s
			}
		case "shardKey":
			if s, ok := v.(string); ok {
				node.ShardKey = s
			}
		case "federationId":
			if s, ok := v.(string); ok {
				node.FederationID = s
			}
		case "properties":
			if m, ok := v.(map[string]any); ok {
				node.Properties = m
			}
		}
	}
}
