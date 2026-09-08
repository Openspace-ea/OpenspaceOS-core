package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrClientNotFound 表示按 ID 查询时未找到对应客户端。
var ErrClientNotFound = errors.New("客户端不存在")

// clientSchemaSQL 是 clients 表的建表 SQL（SQLite 与 PostgreSQL 通用，字段均以 TEXT 存储）。
const clientSchemaSQL = `
CREATE TABLE IF NOT EXISTS clients (
    client_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    module_name TEXT NOT NULL DEFAULT '',
    community_id TEXT NOT NULL DEFAULT '',
    roles TEXT NOT NULL DEFAULT '[]',
    api_key_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_clients_name ON clients(name);
CREATE INDEX IF NOT EXISTS idx_clients_community ON clients(community_id);
CREATE INDEX IF NOT EXISTS idx_clients_status ON clients(status);
`

// ClientStore 是机器接入方（Client）的持久化抽象接口。
//
// 提供 Client 的 CRUD 与 API Key 查询能力。可通过 SQLite / PostgreSQL 实现。
type ClientStore interface {
	// InitSchema 创建 clients 表与索引（幂等）。
	InitSchema(ctx context.Context) error
	// Create 创建一个 Client。clientID 已存在时返回错误。
	Create(ctx context.Context, client *Client) error
	// GetByID 按客户端 ID 查询。不存在时返回 ErrClientNotFound。
	GetByID(ctx context.Context, clientID string) (*Client, error)
	// List 列出所有客户端。
	List(ctx context.Context) ([]*Client, error)
	// Update 更新一个客户端。不存在时返回 ErrClientNotFound。
	Update(ctx context.Context, client *Client) error
	// Delete 按客户端 ID 删除。
	Delete(ctx context.Context, clientID string) error
	// Close 释放资源（可为空操作）。
	Close() error
}

// SQLiteClientStore 是基于 database/sql 的 ClientStore 实现。
//
// Roles 以 JSON 数组字符串存储，时间以 RFC3339Nano 字符串存储；
// 通过 rebind 同时支持 SQLite（`?`）与 PostgreSQL（`$N`）。
type SQLiteClientStore struct {
	db     *sql.DB
	rebind func(string) string
}

// NewSQLiteClientStore 创建基于 SQLite 的 ClientStore。
func NewSQLiteClientStore(db *sql.DB) *SQLiteClientStore {
	return &SQLiteClientStore{db: db, rebind: func(s string) string { return s }}
}

// NewPostgresClientStore 创建基于 PostgreSQL 的 ClientStore。
func NewPostgresClientStore(db *sql.DB) *SQLiteClientStore {
	return &SQLiteClientStore{db: db, rebind: func(s string) string { return rebindPostgres(s) }}
}

// InitSchema 创建 clients 表与索引（幂等）。
func (s *SQLiteClientStore) InitSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, s.rebind(clientSchemaSQL)); err != nil {
		return fmt.Errorf("创建 clients 表失败: %w", err)
	}
	return nil
}

// Create 插入一个 Client。
func (s *SQLiteClientStore) Create(ctx context.Context, c *Client) error {
	if c == nil {
		return errors.New("client 不能为 nil")
	}
	if c.ClientID == "" {
		return errors.New("clientId 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rolesJSON, err := marshalRoles(c.Roles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.rebind(`
INSERT INTO clients (client_id, name, module_name, community_id, roles, api_key_hash, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.ClientID,
		c.Name,
		c.ModuleName,
		c.CommunityID,
		rolesJSON,
		c.APIKeyHash,
		c.Status,
		c.CreatedAt.UTC().Format(time.RFC3339Nano),
		c.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入客户端失败: %w", err)
	}
	return nil
}

// GetByID 按客户端 ID 查询。
func (s *SQLiteClientStore) GetByID(ctx context.Context, clientID string) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, s.rebind(`
SELECT client_id, name, module_name, community_id, roles, api_key_hash, status, created_at, updated_at
FROM clients WHERE client_id = ?`), clientID)
	return scanClient(row)
}

// List 列出所有客户端，按创建时间升序排列。
func (s *SQLiteClientStore) List(ctx context.Context) ([]*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(`
SELECT client_id, name, module_name, community_id, roles, api_key_hash, status, created_at, updated_at
FROM clients ORDER BY created_at ASC, client_id ASC`))
	if err != nil {
		return nil, fmt.Errorf("查询客户端列表失败: %w", err)
	}
	defer rows.Close()

	var result []*Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描客户端行失败: %w", err)
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历客户端列表失败: %w", err)
	}
	return result, nil
}

// Update 更新一个客户端。
func (s *SQLiteClientStore) Update(ctx context.Context, c *Client) error {
	if c == nil {
		return errors.New("client 不能为 nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rolesJSON, err := marshalRoles(c.Roles)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.rebind(`
UPDATE clients
SET name = ?, module_name = ?, community_id = ?, roles = ?, api_key_hash = ?, status = ?, updated_at = ?
WHERE client_id = ?`),
		c.Name,
		c.ModuleName,
		c.CommunityID,
		rolesJSON,
		c.APIKeyHash,
		c.Status,
		c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		c.ClientID,
	)
	if err != nil {
		return fmt.Errorf("更新客户端失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: clientId=%s", ErrClientNotFound, c.ClientID)
	}
	return nil
}

// Delete 按客户端 ID 删除。
func (s *SQLiteClientStore) Delete(ctx context.Context, clientID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, s.rebind("DELETE FROM clients WHERE client_id = ?"), clientID)
	if err != nil {
		return fmt.Errorf("删除客户端失败: %w", err)
	}
	return nil
}

// Close 释放资源。当前不持有独立连接，为空操作。
func (s *SQLiteClientStore) Close() error {
	return nil
}

// clientRowScanner 是 *sql.Row 和 *sql.Rows 共同实现的接口。
type clientRowScanner interface {
	Scan(dest ...any) error
}

// scanClient 从 *sql.Row 或 *sql.Rows 扫描出一个 Client。
func scanClient(s clientRowScanner) (*Client, error) {
	var (
		clientID    string
		name        string
		moduleName  string
		communityID string
		rolesStr    string
		apiKeyHash  string
		status      string
		createdStr  string
		updatedStr  string
	)
	if err := s.Scan(
		&clientID,
		&name,
		&moduleName,
		&communityID,
		&rolesStr,
		&apiKeyHash,
		&status,
		&createdStr,
		&updatedStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrClientNotFound
		}
		return nil, fmt.Errorf("扫描客户端行失败: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, fmt.Errorf("解析 createdAt 失败: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("解析 updatedAt 失败: %w", err)
	}
	roles, err := unmarshalRoles(rolesStr)
	if err != nil {
		return nil, err
	}

	return &Client{
		ClientID:    clientID,
		Name:        name,
		ModuleName:  moduleName,
		CommunityID: communityID,
		Roles:       roles,
		APIKeyHash:  apiKeyHash,
		Status:      status,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// 编译期断言。
var _ ClientStore = (*SQLiteClientStore)(nil)