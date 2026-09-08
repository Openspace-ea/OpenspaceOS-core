package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// defaultListLimit 是 List 的默认返回上限。
const defaultListLimit = 100

// SQLiteNodeRepository 是基于 database/sql 的 NodeRepository 实现。
//
// Properties 字段以 JSON 字符串存储。时间字段以 RFC3339Nano 字符串存储。
// 通过 rebind 字段适配不同驱动占位符：SQLite 原样、PostgreSQL 转换为 $N。
type SQLiteNodeRepository struct {
	db     *sql.DB
	rebind rebindFunc
}

// NewSQLiteNodeRepository 创建基于 SQLite 的 NodeRepository。
//
// 调用方需确保 db 已完成表结构初始化（参见 InitDB）。
func NewSQLiteNodeRepository(db *sql.DB) *SQLiteNodeRepository {
	return &SQLiteNodeRepository{db: db, rebind: rebindIdentity}
}

// NewPostgresNodeRepository 创建基于 PostgreSQL 的 NodeRepository。
//
// 复用同一套以 SQLite 占位符编写的 SQL，运行时转换为 PG 的 $N 占位符。
// 调用方需确保数据库已完成 PG 表结构初始化（参见 InitPostgresDB）。
func NewPostgresNodeRepository(db *sql.DB) *SQLiteNodeRepository {
	return &SQLiteNodeRepository{db: db, rebind: rebindPostgres}
}

// Create 插入一个 Node。如果 nodeID 已存在则返回错误。
func (r *SQLiteNodeRepository) Create(ctx context.Context, node *model.Node) error {
	if node == nil {
		return fmt.Errorf("node 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	propsJSON, err := marshalProperties(node.Properties)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, r.rebind(`
INSERT INTO nodes (node_id, node_type, name, status, owner_community_id, created_at, updated_at, shard_key, federation_id, properties)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		node.NodeID,
		string(node.NodeType),
		node.Name,
		node.Status,
		node.OwnerCommunityID,
		node.CreatedAt.UTC().Format(time.RFC3339Nano),
		node.UpdatedAt.UTC().Format(time.RFC3339Nano),
		node.ShardKey,
		node.FederationID,
		propsJSON,
	)
	if err != nil {
		return fmt.Errorf("插入节点失败: %w", err)
	}
	return nil
}

// Get 按节点 ID 查询 Node。不存在时返回 ErrNotFound。
func (r *SQLiteNodeRepository) Get(ctx context.Context, nodeID string) (*model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	row := r.db.QueryRowContext(ctx, r.rebind(`
SELECT node_id, node_type, name, status, owner_community_id, created_at, updated_at, shard_key, federation_id, properties
FROM nodes WHERE node_id = ?`), nodeID)

	node, err := scanNode(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: nodeId=%s", ErrNotFound, nodeID)
		}
		return nil, fmt.Errorf("查询节点失败: %w", err)
	}
	return node, nil
}

// Update 更新一个 Node，同时刷新 updated_at。
// 如果节点不存在则返回 ErrNotFound。
func (r *SQLiteNodeRepository) Update(ctx context.Context, node *model.Node) error {
	if node == nil {
		return fmt.Errorf("node 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	propsJSON, err := marshalProperties(node.Properties)
	if err != nil {
		return err
	}

	res, err := r.db.ExecContext(ctx, r.rebind(`
UPDATE nodes
SET node_type = ?, name = ?, status = ?, owner_community_id = ?, updated_at = ?, shard_key = ?, federation_id = ?, properties = ?
WHERE node_id = ?`),
		string(node.NodeType),
		node.Name,
		node.Status,
		node.OwnerCommunityID,
		node.UpdatedAt.UTC().Format(time.RFC3339Nano),
		node.ShardKey,
		node.FederationID,
		propsJSON,
		node.NodeID,
	)
	if err != nil {
		return fmt.Errorf("更新节点失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: nodeId=%s", ErrNotFound, node.NodeID)
	}
	return nil
}

// Delete 按节点 ID 删除 Node。
func (r *SQLiteNodeRepository) Delete(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, r.rebind("DELETE FROM nodes WHERE node_id = ?"), nodeID)
	if err != nil {
		return fmt.Errorf("删除节点失败: %w", err)
	}
	return nil
}

// List 按条件列出 Node，支持类型、社区、状态过滤与分页。
func (r *SQLiteNodeRepository) List(ctx context.Context, opts ListOptions) ([]*model.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var (
		where []string
		args  []any
	)
	if opts.NodeType != "" {
		where = append(where, "node_type = ?")
		args = append(args, string(opts.NodeType))
	}
	if opts.OwnerCommunityID != "" {
		where = append(where, "owner_community_id = ?")
		args = append(args, opts.OwnerCommunityID)
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, opts.Status)
	}

	query := "SELECT node_id, node_type, name, status, owner_community_id, created_at, updated_at, shard_key, federation_id, properties FROM nodes"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at ASC, node_id ASC"

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, opts.Offset)

	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询节点列表失败: %w", err)
	}
	defer rows.Close()

	var result []*model.Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描节点行失败: %w", err)
		}
		result = append(result, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点列表失败: %w", err)
	}
	return result, nil
}

// rowScanner 是 *sql.Row 和 *sql.Rows 共同实现的接口。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanNode 从 *sql.Row 或 *sql.Rows 扫描出一个 Node。
func scanNode(s rowScanner) (*model.Node, error) {
	var (
		nodeID           string
		nodeType         string
		name             string
		status           string
		ownerCommunityID string
		createdAtStr     string
		updatedAtStr     string
		shardKey         string
		federationID     string
		propertiesStr    string
	)
	if err := s.Scan(
		&nodeID,
		&nodeType,
		&name,
		&status,
		&ownerCommunityID,
		&createdAtStr,
		&updatedAtStr,
		&shardKey,
		&federationID,
		&propertiesStr,
	); err != nil {
		return nil, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return nil, fmt.Errorf("解析 createdAt 失败: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return nil, fmt.Errorf("解析 updatedAt 失败: %w", err)
	}

	props, err := unmarshalProperties(propertiesStr)
	if err != nil {
		return nil, err
	}

	return &model.Node{
		NodeID:           nodeID,
		NodeType:         model.NodeType(nodeType),
		Name:             name,
		Status:           status,
		OwnerCommunityID: ownerCommunityID,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		ShardKey:         shardKey,
		FederationID:     federationID,
		Properties:       props,
	}, nil
}

// marshalProperties 将 Properties map 序列化为 JSON 字符串。
// nil map 序列化为 "{}"。
func marshalProperties(props map[string]any) (string, error) {
	if len(props) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("序列化 properties 失败: %w", err)
	}
	return string(data), nil
}

// unmarshalProperties 将 JSON 字符串反序列化为 Properties map。
func unmarshalProperties(s string) (map[string]any, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("反序列化 properties 失败: %w", err)
	}
	return m, nil
}

// 编译期断言：SQLiteNodeRepository 实现 NodeRepository 接口。
var _ NodeRepository = (*SQLiteNodeRepository)(nil)
