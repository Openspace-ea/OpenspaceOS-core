package core

import (
	"context"
	"database/sql"
	"fmt"

	// pgx stdlib 作为 PostgreSQL 的 database/sql 驱动，driverName 为 "pgx"。
	_ "github.com/jackc/pgx/v5/stdlib"
)

// postgresSchemaSQL 是 PostgreSQL 下 nodes 与 relationships 表的建表 SQL。
//
// 字段结构与 SQLite 版本保持一致（时间与 properties 均以 TEXT 存储），
// 这样现有 scanNode / scanRelationship / marshal·unmarshal 逻辑可原样复用。
const postgresSchemaSQL = `
CREATE TABLE IF NOT EXISTS nodes (
    node_id TEXT PRIMARY KEY,
    node_type TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    owner_community_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    shard_key TEXT NOT NULL DEFAULT '',
    federation_id TEXT NOT NULL DEFAULT '',
    properties TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_nodes_type ON nodes(node_type);
CREATE INDEX IF NOT EXISTS idx_nodes_community ON nodes(owner_community_id);
CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status);
CREATE INDEX IF NOT EXISTS idx_nodes_shard ON nodes(shard_key);
CREATE INDEX IF NOT EXISTS idx_nodes_federation ON nodes(federation_id);

CREATE TABLE IF NOT EXISTS relationships (
    rel_id TEXT PRIMARY KEY,
    from_node_id TEXT NOT NULL,
    to_node_id TEXT NOT NULL,
    rel_type TEXT NOT NULL,
    properties TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rels_from ON relationships(from_node_id);
CREATE INDEX IF NOT EXISTS idx_rels_to ON relationships(to_node_id);
CREATE INDEX IF NOT EXISTS idx_rels_type ON relationships(rel_type);
`

// InitPostgresDB 初始化 PostgreSQL 数据库连接并创建必要的表与索引。
//
// dsn 为 PostgreSQL 连接串，例如：
//
//	postgres://user:pass@localhost:5432/db?sslmode=disable
//
// 会创建 nodes 与 relationships 表（幂等），并开启连接池。
func InitPostgresDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 postgres 数据库失败: %w", err)
	}

	// 连接池配置：最小并发数 2、最大 20。写入依赖该池支撑并发事务。
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres 数据库失败: %w", err)
	}

	if _, err := db.ExecContext(ctx, postgresSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("创建 postgres 表失败: %w", err)
	}
	return db, nil
}