package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRel 构造一个合法的测试 Relationship。
func newTestRel(relID, from, to string, rt model.RelationshipType) *model.Relationship {
	return &model.Relationship{
		RelID:      relID,
		FromNodeID: from,
		ToNodeID:   to,
		RelType:    rt,
		CreatedAt:  time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Properties: map[string]any{"note": "test"},
	}
}

// TestRelationshipRepository_CRUD 校验关系的完整 CRUD 流程（SQLite 与 PostgreSQL 双驱动）。
func TestRelationshipRepository_CRUD(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-1", model.NodeTypeSatellite, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("gs-1", model.NodeTypeGroundStation, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("comm-1", model.NodeTypeCommunity, "Active")))

		rel1 := newTestRel("rel-1", "sat-1", "gs-1", model.RelControlledBy)
		rel2 := newTestRel("rel-2", "comm-1", "sat-1", model.RelOwns)
		require.NoError(t, relRepo.Create(ctx, rel1))
		require.NoError(t, relRepo.Create(ctx, rel2))

		outSat1, err := relRepo.ListOutgoing(ctx, "sat-1", "")
		require.NoError(t, err)
		assert.Len(t, outSat1, 1)
		assert.Equal(t, "rel-1", outSat1[0].RelID)
		assert.Equal(t, "test", outSat1[0].Properties["note"])

		inSat1, err := relRepo.ListIncoming(ctx, "sat-1", "")
		require.NoError(t, err)
		assert.Len(t, inSat1, 1)
		assert.Equal(t, "rel-2", inSat1[0].RelID)

		outSat1Controlled, err := relRepo.ListOutgoing(ctx, "sat-1", model.RelControlledBy)
		require.NoError(t, err)
		assert.Len(t, outSat1Controlled, 1)

		outSat1Owns, err := relRepo.ListOutgoing(ctx, "sat-1", model.RelOwns)
		require.NoError(t, err)
		assert.Len(t, outSat1Owns, 0)

		require.NoError(t, relRepo.Delete(ctx, "rel-1"))
		outAfter, err := relRepo.ListOutgoing(ctx, "sat-1", "")
		require.NoError(t, err)
		assert.Len(t, outAfter, 0)
	})
}

// TestRelationshipRepository_Create_NodeNotFound 校验创建关系时节点不存在返回 ErrNotFound。
func TestRelationshipRepository_Create_NodeNotFound(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-1", model.NodeTypeSatellite, "Active")))

		rel := newTestRel("rel-1", "sat-1", "not-exist", model.RelControlledBy)
		err := relRepo.Create(ctx, rel)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))

		rel2 := newTestRel("rel-2", "not-exist", "sat-1", model.RelControlledBy)
		err = relRepo.Create(ctx, rel2)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
	})
}

// TestRelationshipRepository_Traverse_BFS 校验 BFS 遍历结果。
//
// 图结构：
//
//	comm-1 --owns--> sat-1 --controlledBy--> gs-1 --providesServiceTo--> sat-2
func TestRelationshipRepository_Traverse_BFS(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		for _, n := range []*model.Node{
			newTestNode("comm-1", model.NodeTypeCommunity, "Active"),
			newTestNode("sat-1", model.NodeTypeSatellite, "Active"),
			newTestNode("gs-1", model.NodeTypeGroundStation, "Active"),
			newTestNode("sat-2", model.NodeTypeSatellite, "Active"),
		} {
			require.NoError(t, nodeRepo.Create(ctx, n))
		}

		rels := []*model.Relationship{
			newTestRel("rel-1", "comm-1", "sat-1", model.RelOwns),
			newTestRel("rel-2", "sat-1", "gs-1", model.RelControlledBy),
			newTestRel("rel-3", "gs-1", "sat-2", model.RelProvidesServiceTo),
		}
		for _, r := range rels {
			require.NoError(t, relRepo.Create(ctx, r))
		}

		depth1, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{Direction: "out", MaxDepth: 1})
		require.NoError(t, err)
		assert.Len(t, depth1, 1)
		assert.Equal(t, "rel-1", depth1[0].RelID)

		depth2, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{Direction: "out", MaxDepth: 2})
		require.NoError(t, err)
		assert.Len(t, depth2, 2)
		relIDs := relIDsOf(depth2)
		assert.ElementsMatch(t, []string{"rel-1", "rel-2"}, relIDs)

		depth3, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{Direction: "out", MaxDepth: 3})
		require.NoError(t, err)
		assert.Len(t, depth3, 3)
		relIDs = relIDsOf(depth3)
		assert.ElementsMatch(t, []string{"rel-1", "rel-2", "rel-3"}, relIDs)

		depthDefault, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{})
		require.NoError(t, err)
		assert.Len(t, depthDefault, 1)
	})
}

// TestRelationshipRepository_Traverse_DefaultDirection 校验默认方向为 out。
func TestRelationshipRepository_Traverse_DefaultDirection(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-1", model.NodeTypeSatellite, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("gs-1", model.NodeTypeGroundStation, "Active")))
		require.NoError(t, relRepo.Create(ctx, newTestRel("rel-1", "sat-1", "gs-1", model.RelControlledBy)))

		out, err := relRepo.Traverse(ctx, "gs-1", TraverseOptions{MaxDepth: 1})
		require.NoError(t, err)
		assert.Len(t, out, 0)

		in, err := relRepo.Traverse(ctx, "gs-1", TraverseOptions{Direction: "in", MaxDepth: 1})
		require.NoError(t, err)
		assert.Len(t, in, 1)
		assert.Equal(t, "rel-1", in[0].RelID)

		both, err := relRepo.Traverse(ctx, "gs-1", TraverseOptions{Direction: "both", MaxDepth: 1})
		require.NoError(t, err)
		assert.Len(t, both, 1)
	})
}

// TestRelationshipRepository_Traverse_RelTypeFilter 校验遍历时按关系类型过滤。
func TestRelationshipRepository_Traverse_RelTypeFilter(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		require.NoError(t, nodeRepo.Create(ctx, newTestNode("comm-1", model.NodeTypeCommunity, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-1", model.NodeTypeSatellite, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-2", model.NodeTypeSatellite, "Active")))

		require.NoError(t, relRepo.Create(ctx, newTestRel("rel-1", "comm-1", "sat-1", model.RelOwns)))
		require.NoError(t, relRepo.Create(ctx, newTestRel("rel-2", "comm-1", "sat-2", model.RelBelongsTo)))

		ownsOnly, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{
			Direction: "out",
			MaxDepth:  1,
			RelType:   model.RelOwns,
		})
		require.NoError(t, err)
		assert.Len(t, ownsOnly, 1)
		assert.Equal(t, "rel-1", ownsOnly[0].RelID)
	})
}

// TestRelationshipRepository_Traverse_Cycle 校验存在环路时不会死循环。
//
//	comm-1 --owns--> sat-1 --memberOf--> comm-1（形成环）
func TestRelationshipRepository_Traverse_Cycle(t *testing.T) {
	runRelRepoTests(t, func(t *testing.T, nodeRepo NodeRepository, relRepo RelationshipRepository) {
		ctx := context.Background()

		require.NoError(t, nodeRepo.Create(ctx, newTestNode("comm-1", model.NodeTypeCommunity, "Active")))
		require.NoError(t, nodeRepo.Create(ctx, newTestNode("sat-1", model.NodeTypeSatellite, "Active")))

		require.NoError(t, relRepo.Create(ctx, newTestRel("rel-1", "comm-1", "sat-1", model.RelOwns)))
		require.NoError(t, relRepo.Create(ctx, newTestRel("rel-2", "sat-1", "comm-1", model.RelMemberOf)))

		result, err := relRepo.Traverse(ctx, "comm-1", TraverseOptions{Direction: "out", MaxDepth: 10})
		require.NoError(t, err)
		assert.Len(t, result, 2)
		relIDs := relIDsOf(result)
		assert.ElementsMatch(t, []string{"rel-1", "rel-2"}, relIDs)
	})
}

// relIDsOf 提取关系的 RelID 列表。
func relIDsOf(rels []*model.Relationship) []string {
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		out = append(out, r.RelID)
	}
	return out
}