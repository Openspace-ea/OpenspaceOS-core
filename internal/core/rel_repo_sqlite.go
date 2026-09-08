package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// SQLiteRelationshipRepository 是基于 database/sql 的 RelationshipRepository 实现。
//
// 关系的 Properties 字段以 JSON 字符串存储。创建关系时会校验
// from/to 节点是否存在于 nodes 表中。通过 rebind 适配不同驱动占位符。
type SQLiteRelationshipRepository struct {
	db     *sql.DB
	rebind rebindFunc
}

// NewSQLiteRelationshipRepository 创建基于 SQLite 的 RelationshipRepository。
//
// 调用方需确保 db 已完成表结构初始化（参见 InitDB）。
func NewSQLiteRelationshipRepository(db *sql.DB) *SQLiteRelationshipRepository {
	return &SQLiteRelationshipRepository{db: db, rebind: rebindIdentity}
}

// NewPostgresRelationshipRepository 创建基于 PostgreSQL 的 RelationshipRepository。
//
// 复用同一套以 SQLite 占位符编写的 SQL，运行时转换为 PG 的 $N 占位符。
// 调用方需确保数据库已完成 PG 表结构初始化（参见 InitPostgresDB）。
func NewPostgresRelationshipRepository(db *sql.DB) *SQLiteRelationshipRepository {
	return &SQLiteRelationshipRepository{db: db, rebind: rebindPostgres}
}

// Create 创建一条关系。校验 from/to 节点是否存在。
func (r *SQLiteRelationshipRepository) Create(ctx context.Context, rel *model.Relationship) error {
	if rel == nil {
		return fmt.Errorf("rel 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 校验 from/to 节点存在
	if err := r.ensureNodeExists(ctx, rel.FromNodeID); err != nil {
		return err
	}
	if err := r.ensureNodeExists(ctx, rel.ToNodeID); err != nil {
		return err
	}

	propsJSON, err := marshalProperties(rel.Properties)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, r.rebind(`
INSERT INTO relationships (rel_id, from_node_id, to_node_id, rel_type, properties, created_at)
VALUES (?, ?, ?, ?, ?, ?)`),
		rel.RelID,
		rel.FromNodeID,
		rel.ToNodeID,
		string(rel.RelType),
		propsJSON,
		rel.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入关系失败: %w", err)
	}
	return nil
}

// Delete 按关系 ID 删除关系。
func (r *SQLiteRelationshipRepository) Delete(ctx context.Context, relID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, r.rebind("DELETE FROM relationships WHERE rel_id = ?"), relID)
	if err != nil {
		return fmt.Errorf("删除关系失败: %w", err)
	}
	return nil
}

// ListOutgoing 列出指定节点的出边关系。
// relType 为空时表示不限关系类型。
func (r *SQLiteRelationshipRepository) ListOutgoing(ctx context.Context, nodeID string, relType model.RelationshipType) ([]*model.Relationship, error) {
	return r.listByNode(ctx, "from_node_id", nodeID, relType)
}

// ListIncoming 列出指定节点的入边关系。
// relType 为空时表示不限关系类型。
func (r *SQLiteRelationshipRepository) ListIncoming(ctx context.Context, nodeID string, relType model.RelationshipType) ([]*model.Relationship, error) {
	return r.listByNode(ctx, "to_node_id", nodeID, relType)
}

// listByNode 按方向列段与节点 ID 查询关系。
func (r *SQLiteRelationshipRepository) listByNode(ctx context.Context, dirCol, nodeID string, relType model.RelationshipType) ([]*model.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var (
		where []string
		args  []any
	)
	where = append(where, dirCol+" = ?")
	args = append(args, nodeID)
	if relType != "" {
		where = append(where, "rel_type = ?")
		args = append(args, string(relType))
	}

	query := "SELECT rel_id, from_node_id, to_node_id, rel_type, properties, created_at FROM relationships WHERE " +
		strings.Join(where, " AND ") + " ORDER BY created_at ASC, rel_id ASC"

	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询关系失败: %w", err)
	}
	defer rows.Close()

	var result []*model.Relationship
	for rows.Next() {
		rel, err := scanRelationship(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描关系行失败: %w", err)
		}
		result = append(result, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历关系列表失败: %w", err)
	}
	return result, nil
}

// Traverse 从起始节点出发进行 BFS 图遍历，返回遍历过程中经过的所有关系。
//
// 使用循环+map 去重实现：以节点 ID 集合避免环路重复访问，
// 以关系 ID 集合避免重复收集同一关系。
func (r *SQLiteRelationshipRepository) Traverse(ctx context.Context, startNodeID string, opts TraverseOptions) ([]*model.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	direction := opts.Direction
	if direction == "" {
		direction = "out"
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}

	visited := map[string]bool{startNodeID: true}
	collected := map[string]bool{}
	var result []*model.Relationship
	frontier := []string{startNodeID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var nextFrontier []string
		for _, nodeID := range frontier {
			rels, err := r.queryForTraversal(ctx, nodeID, direction, opts.RelType)
			if err != nil {
				return nil, err
			}
			for _, rel := range rels {
				if collected[rel.RelID] {
					continue
				}
				collected[rel.RelID] = true
				result = append(result, rel)

				// 计算对端节点，继续向外扩展
				other := rel.ToNodeID
				if rel.FromNodeID != nodeID {
					other = rel.FromNodeID
				}
				if !visited[other] {
					visited[other] = true
					nextFrontier = append(nextFrontier, other)
				}
			}
		}
		frontier = nextFrontier
	}
	return result, nil
}

// queryForTraversal 按方向与节点 ID 查询用于遍历的关系。
func (r *SQLiteRelationshipRepository) queryForTraversal(ctx context.Context, nodeID, direction string, relType model.RelationshipType) ([]*model.Relationship, error) {
	var (
		where []string
		args  []any
	)
	switch direction {
	case "in":
		where = append(where, "to_node_id = ?")
		args = append(args, nodeID)
	case "both":
		where = append(where, "(from_node_id = ? OR to_node_id = ?)")
		args = append(args, nodeID, nodeID)
	default: // "out"
		where = append(where, "from_node_id = ?")
		args = append(args, nodeID)
	}
	if relType != "" {
		where = append(where, "rel_type = ?")
		args = append(args, string(relType))
	}

	query := "SELECT rel_id, from_node_id, to_node_id, rel_type, properties, created_at FROM relationships WHERE " +
		strings.Join(where, " AND ") + " ORDER BY created_at ASC, rel_id ASC"

	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("遍历查询关系失败: %w", err)
	}
	defer rows.Close()

	var result []*model.Relationship
	for rows.Next() {
		rel, err := scanRelationship(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描关系行失败: %w", err)
		}
		result = append(result, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历关系列表失败: %w", err)
	}
	return result, nil
}

// ensureNodeExists 校验节点是否存在于 nodes 表中。
func (r *SQLiteRelationshipRepository) ensureNodeExists(ctx context.Context, nodeID string) error {
	var cnt int
	err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM nodes WHERE node_id = ?"), nodeID).Scan(&cnt)
	if err != nil {
		return fmt.Errorf("校验节点存在性失败: %w", err)
	}
	if cnt == 0 {
		return fmt.Errorf("%w: nodeId=%s", ErrNotFound, nodeID)
	}
	return nil
}

// scanRelationship 从 rows 扫描出一条关系。
func scanRelationship(s rowScanner) (*model.Relationship, error) {
	var (
		relID         string
		fromNodeID    string
		toNodeID      string
		relType       string
		propertiesStr string
		createdAtStr  string
	)
	if err := s.Scan(&relID, &fromNodeID, &toNodeID, &relType, &propertiesStr, &createdAtStr); err != nil {
		return nil, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return nil, fmt.Errorf("解析 createdAt 失败: %w", err)
	}

	props, err := unmarshalProperties(propertiesStr)
	if err != nil {
		return nil, err
	}

	return &model.Relationship{
		RelID:      relID,
		FromNodeID: fromNodeID,
		ToNodeID:   toNodeID,
		RelType:    model.RelationshipType(relType),
		Properties: props,
		CreatedAt:  createdAt,
	}, nil
}

// 编译期断言：SQLiteRelationshipRepository 实现 RelationshipRepository 接口。
var _ RelationshipRepository = (*SQLiteRelationshipRepository)(nil)
