package storage

// DefaultMigrations 返回存储层的默认迁移链。
//
// 迁移以 SQLite 兼容的 DDL 编写（不使用占位符，字段多用 TEXT/INTEGER，
// 两驱动均可执行）。共同协商：时间以 RFC3339Nano 文本存储，与存量表一致。
//
// 迁移编号要约定了用途，后续新增迁移请在末尾追加更高版本号：
//
//	1: usage 用量统计表（Task 1.8 / Task 3）
func DefaultMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "create_usage_table",
			Up: `
CREATE TABLE IF NOT EXISTS usage (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL DEFAULT '',
    client_id     TEXT NOT NULL DEFAULT '',
    endpoint      TEXT NOT NULL DEFAULT '',
    operation     TEXT NOT NULL DEFAULT '',
    unit          TEXT NOT NULL DEFAULT '',
    count         INTEGER NOT NULL DEFAULT 0,
    bucket_start  TEXT NOT NULL,
    bucket_end    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_tenant  ON usage(tenant_id);
CREATE INDEX IF NOT EXISTS idx_usage_client  ON usage(client_id);
CREATE INDEX IF NOT EXISTS idx_usage_bucket  ON usage(bucket_start, bucket_end);
CREATE INDEX IF NOT EXISTS idx_usage_tenant_bucket ON usage(tenant_id, bucket_start);
`,
		},
	}
}