package core

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// postgresTestDSN 返回可选的 PostgreSQL 测试连接串（T1.7 双驱动测试门控）。
//
// 设置环境变量 TEST_POSTGRES_DSN 后，仓储"同一测试集"会额外以 PostgreSQL
// 后端运行一份；否则仅运行 SQLite 后端。
func postgresTestDSN() string {
	return os.Getenv("TEST_POSTGRES_DSN")
}

// nodeRepoCase 描述一个 NodeRepository 测试后端。
type nodeRepoCase struct {
	name string
	repo func(t *testing.T) NodeRepository
}

// relRepoCase 描述一个 RelationshipRepository 测试后端（共享同一个 DB 连接）。
type relRepoCase struct {
	name string
	repo func(t *testing.T) (NodeRepository, RelationshipRepository)
}

// newTestPostgresDB 初始化 PostgreSQL 连接并注册清理。
func newTestPostgresDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := InitPostgresDB(context.Background(), dsn)
	require.NoError(t, err, "连接 PostgreSQL 失败（TEST_POSTGRES_DSN 有效但不可达？）")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// cleanCoreTables 清空 nodes 与 relationships，使 PG 共享库上每个用例可独立重跑。
func cleanCoreTables(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	// 先删关系再删节点，规避逻辑外键顺序
	_, err := db.ExecContext(ctx, "DELETE FROM relationships")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "DELETE FROM nodes")
	require.NoError(t, err)
}

// nodeRepoDrivers 返回 node 仓储测试需运行的所有后端。SQLite 恒有；
// PostgreSQL 在设置 TEST_POSTGRES_DSN 时追加。
func nodeRepoDrivers(t *testing.T) []nodeRepoCase {
	t.Helper()
	cases := []nodeRepoCase{{name: "sqlite", repo: func(t *testing.T) NodeRepository {
		return NewSQLiteNodeRepository(newTestDB(t))
	}}}
	if dsn := postgresTestDSN(); dsn != "" {
		cases = append(cases, nodeRepoCase{name: "postgres", repo: func(t *testing.T) NodeRepository {
			db := newTestPostgresDB(t, dsn)
			cleanCoreTables(t, db)
			return NewPostgresNodeRepository(db)
		}})
	}
	return cases
}

// relRepoDrivers 返回 relationship 仓储测试需运行的所有后端。
func relRepoDrivers(t *testing.T) []relRepoCase {
	t.Helper()
	cases := []relRepoCase{{name: "sqlite", repo: func(t *testing.T) (NodeRepository, RelationshipRepository) {
		db := newTestDB(t)
		return NewSQLiteNodeRepository(db), NewSQLiteRelationshipRepository(db)
	}}}
	if dsn := postgresTestDSN(); dsn != "" {
		cases = append(cases, relRepoCase{name: "postgres", repo: func(t *testing.T) (NodeRepository, RelationshipRepository) {
			db := newTestPostgresDB(t, dsn)
			cleanCoreTables(t, db)
			return NewPostgresNodeRepository(db), NewPostgresRelationshipRepository(db)
		}})
	}
	return cases
}

// runNodeRepoTests 对每个后端运行给定的 NodeRepository 用例（同一测试集）。
func runNodeRepoTests(t *testing.T, fn func(t *testing.T, repo NodeRepository)) {
	t.Helper()
	for _, c := range nodeRepoDrivers(t) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			fn(t, c.repo(t))
		})
	}
}

// runRelRepoTests 对每个后端运行给定的 RelationshipRepository 用例（同一测试集）。
func runRelRepoTests(t *testing.T, fn func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository)) {
	t.Helper()
	for _, c := range relRepoDrivers(t) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			nodeRepo, relRepo := c.repo(t)
			fn(t, nodeRepo, relRepo)
		})
	}
}