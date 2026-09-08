package core

import (
	"database/sql"
	"fmt"

	// 匿名导入 modernc.org/sqlite 驱动，driverName 为 "sqlite"。
	_ "modernc.org/sqlite"
)

// nodeSchemaSQL 是 nodes 表与索引的建表 SQL。
const nodeSchemaSQL = `
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
`

// relSchemaSQL 是 relationships 表与索引的建表 SQL。
const relSchemaSQL = `
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

// InitDB 初始化 SQLite 数据库，创建所有必要的表和索引。
//
// dbPath 为 SQLite 数据库文件路径；为 ":memory:" 时创建内存数据库。
// 会开启 WAL 模式与外键约束，并创建 nodes 和 relationships 表。
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite 数据库失败: %w", err)
	}

	// 限制连接数，避免 SQLite 并发写锁冲突。
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("执行 PRAGMA 失败 (%s): %w", p, err)
		}
	}

	if _, err := db.Exec(nodeSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("创建 nodes 表失败: %w", err)
	}
	if _, err := db.Exec(relSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("创建 relationships 表失败: %w", err)
	}
	return db, nil
}
