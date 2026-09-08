package core

import (
	"context"
	"testing"

	"github.com/openspace-os/openspace-os-core/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateSQLiteToPostgres_ImportAndIdempotent 校验 SQLite→PostgreSQL 导入与其幂等性。
//
// 需要 TEST_POSTGRES_DSN（否则跳过）。导入两次：
//   第一次应全部"新建"；第二次应全部"跳过"（ON CONFLICT DO NOTHING 幂等）。
func TestMigrateSQLiteToPostgres_ImportAndIdempotent(t *testing.T) {
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN 未设置，跳过导入迁移测试")
	}

	ctx := context.Background()

	// 1) 构造 SQLite 源数据
	src, err := InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })
	srcNodeRepo := NewSQLiteNodeRepository(src)
	srcRelRepo := NewSQLiteRelationshipRepository(src)

	for _, n := range []*model.Node{
		newTestNode("sat-1", model.NodeTypeSatellite, "Active"),
		newTestNode("gs-1", model.NodeTypeGroundStation, "Active"),
		newTestNode("comm-1", model.NodeTypeCommunity, "Active"),
	} {
		require.NoError(t, srcNodeRepo.Create(ctx, n))
	}
	require.NoError(t, srcRelRepo.Create(ctx, newTestRel("rel-1", "comm-1", "sat-1", model.RelOwns)))
	require.NoError(t, srcRelRepo.Create(ctx, newTestRel("rel-2", "sat-1", "gs-1", model.RelControlledBy)))

	// 2) PostgreSQL 目标
	dst := newTestPostgresDB(t, dsn)
	cleanCoreTables(t, dst)

	// 3) 首次导入：全部新建
	stats, err := MigrateSQLiteToPostgres(ctx, src, dst, MigrationOptions{BatchSize: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.NodesNew, "节点应全部新建")
	assert.Equal(t, int64(0), stats.NodesSkipped)
	assert.Equal(t, int64(2), stats.RelsNew, "关系应全部新建")
	assert.Equal(t, int64(0), stats.RelsSkipped)

	// 目标库可读（双驱动同一读取逻辑）
	dstNodeRepo := NewPostgresNodeRepository(dst)
	nodes, err := dstNodeRepo.List(ctx, ListOptions{})
	require.NoError(t, err)
	assert.Len(t, nodes, 3)
	sat1, err := dstNodeRepo.Get(ctx, "sat-1")
	require.NoError(t, err)
	assert.Equal(t, "Active", sat1.Status)
	assert.Equal(t, "25544", sat1.Properties["noradId"])

	// 4) 二次导入：应全部跳过（幂等）
	stats2, err := MigrateSQLiteToPostgres(ctx, src, dst, MigrationOptions{BatchSize: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats2.NodesNew)
	assert.Equal(t, int64(3), stats2.NodesSkipped)
	assert.Equal(t, int64(0), stats2.RelsNew)
	assert.Equal(t, int64(2), stats2.RelsSkipped)
}

// TestMigrateSQLiteToPostgres_EmptySource 校验空源不报错且计数为零。
func TestMigrateSQLiteToPostgres_EmptySource(t *testing.T) {
	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN 未设置，跳过导入迁移测试")
	}

	ctx := context.Background()
	src, err := InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	dst := newTestPostgresDB(t, dsn)
	cleanCoreTables(t, dst)

	stats, err := MigrateSQLiteToPostgres(ctx, src, dst, MigrationOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.NodesNew+stats.NodesSkipped+stats.RelsNew+stats.RelsSkipped)
}