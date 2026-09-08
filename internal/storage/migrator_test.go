package storage

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// 匿名导入 modernc.org/sqlite 驱动
	_ "modernc.org/sqlite"
)

// newTestMigrator 创建基于内存 SQLite 的迁移器。
func newTestMigrator(t *testing.T) (*Migrator, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	m := NewMigrator(db, WithMigrations(DefaultMigrations()))
	return m, db
}

// TestMigrate_CreatesUsageTable 验证迁移创建 usage 表及索引。
func TestMigrate_CreatesUsageTable(t *testing.T) {
	m, db := newTestMigrator(t)
	ctx := context.Background()

	require.NoError(t, m.Migrate(ctx))

	// usage 表应存在
	var tbl string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='usage'`).Scan(&tbl)
	require.NoError(t, err)
	assert.Equal(t, "usage", tbl)

	// schema_migrations 应记录版本 1
	var version int64
	var name string
	err = db.QueryRow(`SELECT version, name FROM schema_migrations`).Scan(&version, &name)
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)
	assert.Equal(t, "create_usage_table", name)
}

// TestMigrate_Idempotent 验证重复迁移是幂等的（版本表已记录）。
func TestMigrate_Idempotent(t *testing.T) {
	m, db := newTestMigrator(t)
	ctx := context.Background()

	require.NoError(t, m.Migrate(ctx))
	require.NoError(t, m.Migrate(ctx))
	require.NoError(t, m.Migrate(ctx))

	// 版本表应只有一行
	var cnt int
	err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt)
}

// TestMigrate_InsertUsageRow 验证迁移后可以正常写入 usage 表。
func TestMigrate_InsertUsageRow(t *testing.T) {
	m, db := newTestMigrator(t)
	ctx := context.Background()
	require.NoError(t, m.Migrate(ctx))

	_, err := db.ExecContext(ctx, `
INSERT INTO usage (id, tenant_id, client_id, endpoint, operation, unit, count, bucket_start, bucket_end)
VALUES ('u1', 'comm-a', 'c1', '/api/v1/nodes', 'create', 'call', 5, '2026-01-01T00:00:00Z', '2026-01-01T00:01:00Z')`)
	require.NoError(t, err)

	var count int64
	err = db.QueryRow(`SELECT count FROM usage WHERE id='u1'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
}