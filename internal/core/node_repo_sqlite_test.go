package core

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB 创建内存 SQLite 数据库并完成表结构初始化（用于测试）。
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := InitDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestNode 构造一个合法的测试 Node。
func newTestNode(id string, nt model.NodeType, status string) *model.Node {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	return &model.Node{
		NodeID:           id,
		NodeType:         nt,
		Name:             "测试节点-" + id,
		Status:           status,
		OwnerCommunityID: "comm-001",
		CreatedAt:        now,
		UpdatedAt:        now,
		Properties: map[string]any{
			"noradId": "25544",
		},
	}
}

// TestNodeRepository_CRUD 校验 Node 的完整 CRUD 流程（SQLite 与 PostgreSQL 双驱动）。
func TestNodeRepository_CRUD(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		ctx := context.Background()
		node := newTestNode("sat-001", model.NodeTypeSatellite, "Active")

		require.NoError(t, repo.Create(ctx, node))

		got, err := repo.Get(ctx, "sat-001")
		require.NoError(t, err)
		assert.Equal(t, node.NodeID, got.NodeID)
		assert.Equal(t, node.NodeType, got.NodeType)
		assert.Equal(t, node.Name, got.Name)
		assert.Equal(t, node.Status, got.Status)
		assert.Equal(t, node.OwnerCommunityID, got.OwnerCommunityID)
		assert.True(t, got.CreatedAt.Equal(node.CreatedAt))
		assert.True(t, got.UpdatedAt.Equal(node.UpdatedAt))
		assert.Equal(t, "25544", got.Properties["noradId"])

		got.Status = "Standby"
		got.Name = "更新后名称"
		got.UpdatedAt = got.UpdatedAt.Add(time.Hour)
		require.NoError(t, repo.Update(ctx, got))

		updated, err := repo.Get(ctx, "sat-001")
		require.NoError(t, err)
		assert.Equal(t, "Standby", updated.Status)
		assert.Equal(t, "更新后名称", updated.Name)
		assert.True(t, updated.UpdatedAt.Equal(got.UpdatedAt))

		require.NoError(t, repo.Delete(ctx, "sat-001"))

		_, err = repo.Get(ctx, "sat-001")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
	})
}

// TestNodeRepository_Get_NotFound 校验查询不存在的节点返回 ErrNotFound。
func TestNodeRepository_Get_NotFound(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		_, err := repo.Get(context.Background(), "not-exist")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
	})
}

// TestNodeRepository_Create_Duplicate 校验重复创建返回错误。
func TestNodeRepository_Create_Duplicate(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		ctx := context.Background()
		node := newTestNode("sat-001", model.NodeTypeSatellite, "Active")
		require.NoError(t, repo.Create(ctx, node))

		err := repo.Create(ctx, node)
		require.Error(t, err)
	})
}

// TestNodeRepository_Update_NotFound 校验更新不存在的节点返回 ErrNotFound。
func TestNodeRepository_Update_NotFound(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		node := newTestNode("not-exist", model.NodeTypeSatellite, "Active")
		err := repo.Update(context.Background(), node)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
	})
}

// TestNodeRepository_List_Filter 校验按 NodeType / CommunityID / Status 过滤与分页。
func TestNodeRepository_List_Filter(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		ctx := context.Background()

		now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		nodes := []*model.Node{
			{NodeID: "sat-1", NodeType: model.NodeTypeSatellite, Name: "sat1", Status: "Active", OwnerCommunityID: "comm-a", CreatedAt: now, UpdatedAt: now},
			{NodeID: "sat-2", NodeType: model.NodeTypeSatellite, Name: "sat2", Status: "Standby", OwnerCommunityID: "comm-b", CreatedAt: now.Add(time.Second), UpdatedAt: now},
			{NodeID: "gs-1", NodeType: model.NodeTypeGroundStation, Name: "gs1", Status: "Active", OwnerCommunityID: "comm-a", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now},
			{NodeID: "task-1", NodeType: model.NodeTypeTask, Name: "task1", Status: "Running", OwnerCommunityID: "comm-a", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now},
		}
		for _, n := range nodes {
			require.NoError(t, repo.Create(ctx, n))
		}

		sats, err := repo.List(ctx, ListOptions{NodeType: model.NodeTypeSatellite})
		require.NoError(t, err)
		assert.Len(t, sats, 2)

		commA, err := repo.List(ctx, ListOptions{OwnerCommunityID: "comm-a"})
		require.NoError(t, err)
		assert.Len(t, commA, 3)

		active, err := repo.List(ctx, ListOptions{Status: "Active"})
		require.NoError(t, err)
		assert.Len(t, active, 2)

		commAActive, err := repo.List(ctx, ListOptions{OwnerCommunityID: "comm-a", Status: "Active"})
		require.NoError(t, err)
		assert.Len(t, commAActive, 2)

		page1, err := repo.List(ctx, ListOptions{Limit: 2, Offset: 0})
		require.NoError(t, err)
		assert.Len(t, page1, 2)
		page2, err := repo.List(ctx, ListOptions{Limit: 2, Offset: 2})
		require.NoError(t, err)
		assert.Len(t, page2, 2)

		all, err := repo.List(ctx, ListOptions{})
		require.NoError(t, err)
		assert.Len(t, all, 4)
	})
}

// TestNodeRepository_Properties_RoundTrip 校验 Properties 的 JSON 往返。
func TestNodeRepository_Properties_RoundTrip(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		ctx := context.Background()

		node := newTestNode("sat-001", model.NodeTypeSatellite, "Active")
		node.Properties = map[string]any{
			"noradId": "25544",
			"orbit": map[string]any{
				"inclination": 51.6,
			},
			"capabilities": []any{"imaging", "comm"},
		}
		require.NoError(t, repo.Create(ctx, node))

		got, err := repo.Get(ctx, "sat-001")
		require.NoError(t, err)
		assert.Equal(t, "25544", got.Properties["noradId"])
		orbit, ok := got.Properties["orbit"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, 51.6, orbit["inclination"])
	})
}

// TestNodeRepository_NilNode 校验传入 nil 返回错误。
func TestNodeRepository_NilNode(t *testing.T) {
	runNodeRepoTests(t, func(t *testing.T, repo NodeRepository) {
		ctx := context.Background()
		require.Error(t, repo.Create(ctx, nil))
		require.Error(t, repo.Update(ctx, nil))
	})
}