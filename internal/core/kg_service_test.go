package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureBus 是测试用的消息总线，记录所有发布的事件以便断言。
//
// 不进行 Schema 校验，仅用于验证 KGService 的事件发布逻辑。
type captureBus struct {
	mu     sync.Mutex
	events []*event.Event
}

func (b *captureBus) Publish(_ context.Context, e *event.Event) error {
	if e == nil {
		return errNilEvent
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return nil
}

func (b *captureBus) Subscribe(SubscribeOptions) (<-chan *event.Event, func()) {
	return nil, func() {}
}

func (b *captureBus) Replay(context.Context, ReplayOptions) ([]*event.Event, error) {
	return nil, nil
}

func (b *captureBus) Close() error { return nil }

func (b *captureBus) Ping(context.Context) error { return nil }

// Events 返回已发布事件的副本。
func (b *captureBus) Events() []*event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*event.Event, len(b.events))
	copy(out, b.events)
	return out
}

// Reset 清空已记录的事件。
func (b *captureBus) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = nil
}

// EventsByType 返回指定类型的事件。
func (b *captureBus) EventsByType(et event.EventType) []*event.Event {
	var result []*event.Event
	for _, e := range b.Events() {
		if e.EventType == et {
			result = append(result, e)
		}
	}
	return result
}

// newKGService 创建用于测试的 KGService（使用内存 SQLite + 捕获型总线）。
func newKGService(t *testing.T) (*KGService, *captureBus, *SQLiteNodeRepository, *SQLiteRelationshipRepository) {
	t.Helper()
	db := newTestDB(t)
	nodeRepo := NewSQLiteNodeRepository(db)
	relRepo := NewSQLiteRelationshipRepository(db)
	bus := &captureBus{}
	svc := NewKGService(nodeRepo, relRepo, bus, nil)
	return svc, bus, nodeRepo, relRepo
}

// TestKGService_RegisterNode 校验注册节点后可查询，且 NodeRegistered 与 StateUpdated 事件被发布。
func TestKGService_RegisterNode(t *testing.T) {
	svc, bus, _, _ := newKGService(t)
	ctx := context.Background()

	node := &model.Node{
		NodeID:           "sat-001",
		NodeType:         model.NodeTypeSatellite,
		Name:             "测试卫星",
		Status:           "Active",
		OwnerCommunityID: "comm-001",
		Properties:       map[string]any{"noradId": "25544"},
	}
	require.NoError(t, svc.RegisterNode(ctx, node))

	// 节点可查询
	got, err := svc.GetNode(ctx, "sat-001")
	require.NoError(t, err)
	assert.Equal(t, "sat-001", got.NodeID)
	assert.Equal(t, "Active", got.Status)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Equal(t, "25544", got.Properties["noradId"])

	// NodeRegistered 事件
	regEvents := bus.EventsByType(event.EventNodeRegistered)
	require.Len(t, regEvents, 1)
	assert.Equal(t, "sat-001", regEvents[0].SourceNodeID)
	assert.NotEmpty(t, regEvents[0].EventID)
	assert.False(t, regEvents[0].Timestamp.IsZero())

	var regPayload event.NodeRegisteredPayload
	require.NoError(t, regEvents[0].GetPayload(&regPayload))
	assert.Equal(t, "sat-001", regPayload.NodeID)
	assert.Equal(t, "Satellite", regPayload.NodeType)

	// StateUpdated 事件（新建节点 OldStatus 为空）
	stateEvents := bus.EventsByType(event.EventStateUpdated)
	require.Len(t, stateEvents, 1)
	var statePayload event.StateUpdatedPayload
	require.NoError(t, stateEvents[0].GetPayload(&statePayload))
	assert.Equal(t, "sat-001", statePayload.NodeID)
	assert.Equal(t, "", statePayload.OldStatus)
	assert.Equal(t, "Active", statePayload.NewStatus)
}

// TestKGService_RegisterNode_ValidateFail 校验校验失败时不创建节点也不发布事件。
func TestKGService_RegisterNode_ValidateFail(t *testing.T) {
	svc, bus, _, _ := newKGService(t)
	ctx := context.Background()

	// 缺少必填字段
	node := &model.Node{NodeID: "sat-001", NodeType: model.NodeTypeSatellite}
	err := svc.RegisterNode(ctx, node)
	require.Error(t, err)
	assert.Empty(t, bus.Events())
}

// TestKGService_UpdateNode 校验更新节点后发布 StateUpdated 与 NodeUpdated 事件。
func TestKGService_UpdateNode(t *testing.T) {
	svc, bus, _, _ := newKGService(t)
	ctx := context.Background()

	// 先注册
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	bus.Reset() // 清空注册阶段的事件

	// 更新状态
	updated, err := svc.UpdateNode(ctx, "sat-001", map[string]any{
		"status": "Standby",
		"name":   "更新后卫星",
	})
	require.NoError(t, err)
	assert.Equal(t, "Standby", updated.Status)
	assert.Equal(t, "更新后卫星", updated.Name)

	// 验证持久化
	got, err := svc.GetNode(ctx, "sat-001")
	require.NoError(t, err)
	assert.Equal(t, "Standby", got.Status)
	assert.Equal(t, "更新后卫星", got.Name)

	// StateUpdated 事件
	stateEvents := bus.EventsByType(event.EventStateUpdated)
	require.Len(t, stateEvents, 1)
	var statePayload event.StateUpdatedPayload
	require.NoError(t, stateEvents[0].GetPayload(&statePayload))
	assert.Equal(t, "Active", statePayload.OldStatus)
	assert.Equal(t, "Standby", statePayload.NewStatus)

	// NodeUpdated 事件
	updEvents := bus.EventsByType(event.EventNodeUpdated)
	require.Len(t, updEvents, 1)
	var updPayload event.NodeUpdatedPayload
	require.NoError(t, updEvents[0].GetPayload(&updPayload))
	assert.Equal(t, "sat-001", updPayload.NodeID)
	assert.Equal(t, "Standby", updPayload.Changes["status"])
}

// TestKGService_UpdateNode_StatusUnchanged 校验状态未变化时不发布 StateUpdated 事件。
func TestKGService_UpdateNode_StatusUnchanged(t *testing.T) {
	svc, bus, _, _ := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	bus.Reset()

	// 仅更新 name，不改 status
	_, err := svc.UpdateNode(ctx, "sat-001", map[string]any{"name": "新名称"})
	require.NoError(t, err)

	// 不应有 StateUpdated 事件
	assert.Empty(t, bus.EventsByType(event.EventStateUpdated))
	// 应有 NodeUpdated 事件
	assert.Len(t, bus.EventsByType(event.EventNodeUpdated), 1)
}

// TestKGService_UpdateNode_NotFound 校验更新不存在的节点返回错误。
func TestKGService_UpdateNode_NotFound(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	_, err := svc.UpdateNode(context.Background(), "not-exist", map[string]any{"name": "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// TestKGService_DeleteNode 校验删除节点后发布 NodeDeleted 事件，且相关关系也被删除。
func TestKGService_DeleteNode(t *testing.T) {
	svc, bus, _, relRepo := newKGService(t)
	ctx := context.Background()

	// 准备节点与关系
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "gs-001", NodeType: model.NodeTypeGroundStation,
		Name: "地面站", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-1", FromNodeID: "sat-001", ToNodeID: "gs-001",
		RelType: model.RelControlledBy, CreatedAt: time.Now(),
	}))
	bus.Reset()

	// 删除节点
	require.NoError(t, svc.DeleteNode(ctx, "sat-001"))

	// NodeDeleted 事件
	delEvents := bus.EventsByType(event.EventNodeDeleted)
	require.Len(t, delEvents, 1)
	var delPayload event.NodeDeletedPayload
	require.NoError(t, delEvents[0].GetPayload(&delPayload))
	assert.Equal(t, "sat-001", delPayload.NodeID)

	// 节点已删除
	_, err := svc.GetNode(ctx, "sat-001")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))

	// 相关关系已删除
	out, err := relRepo.ListOutgoing(ctx, "sat-001", "")
	require.NoError(t, err)
	assert.Empty(t, out)
	in, err := relRepo.ListIncoming(ctx, "sat-001", "")
	require.NoError(t, err)
	assert.Empty(t, in)
}

// TestKGService_CreateRelationship 校验创建关系后可查询。
func TestKGService_CreateRelationship(t *testing.T) {
	svc, _, _, relRepo := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "gs-001", NodeType: model.NodeTypeGroundStation,
		Name: "地面站", Status: "Active", OwnerCommunityID: "comm-001",
	}))

	rel := &model.Relationship{
		RelID: "rel-1", FromNodeID: "sat-001", ToNodeID: "gs-001",
		RelType: model.RelControlledBy,
	}
	require.NoError(t, svc.CreateRelationship(ctx, rel))

	// 验证可查询
	out, err := relRepo.ListOutgoing(ctx, "sat-001", "")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "rel-1", out[0].RelID)
	assert.False(t, out[0].CreatedAt.IsZero())
}

// TestKGService_CreateRelationship_NodeNotFound 校验节点不存在时创建关系失败。
func TestKGService_CreateRelationship_NodeNotFound(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))

	err := svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-1", FromNodeID: "sat-001", ToNodeID: "not-exist",
		RelType: model.RelControlledBy, CreatedAt: time.Now(),
	})
	require.Error(t, err)
}

// TestKGService_CreateRelationship_ValidateFail 校验关系校验失败时返回错误。
func TestKGService_CreateRelationship_ValidateFail(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	err := svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "", FromNodeID: "sat-001", ToNodeID: "gs-001",
		RelType: model.RelControlledBy, CreatedAt: time.Now(),
	})
	require.Error(t, err)
}

// TestKGService_QueryRelationships 校验按方向查询关系。
func TestKGService_QueryRelationships(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "comm-001", NodeType: model.NodeTypeCommunity,
		Name: "社区", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-1", FromNodeID: "comm-001", ToNodeID: "sat-001",
		RelType: model.RelOwns, CreatedAt: time.Now(),
	}))

	// out 方向
	out, err := svc.QueryRelationships(ctx, "comm-001", "out", "")
	require.NoError(t, err)
	assert.Len(t, out, 1)

	// in 方向
	in, err := svc.QueryRelationships(ctx, "sat-001", "in", "")
	require.NoError(t, err)
	assert.Len(t, in, 1)

	// both 方向
	both, err := svc.QueryRelationships(ctx, "sat-001", "both", "")
	require.NoError(t, err)
	assert.Len(t, both, 1)
}

// TestKGService_FullScenario 校验完整场景：
//
//	注册 Community → Satellite → GroundStation，
//	建立 belongsTo / controlledBy 关系，通过 Traverse 查询完整关系图。
func TestKGService_FullScenario(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	// 注册节点
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "comm-001", NodeType: model.NodeTypeCommunity,
		Name: "测试社区", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "测试卫星", Status: "Active", OwnerCommunityID: "comm-001",
		Properties: map[string]any{"noradId": "25544"},
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "gs-001", NodeType: model.NodeTypeGroundStation,
		Name: "测试地面站", Status: "Active", OwnerCommunityID: "comm-001",
	}))

	// 建立关系：sat-001 belongsTo comm-001, sat-001 controlledBy gs-001
	require.NoError(t, svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-belong", FromNodeID: "sat-001", ToNodeID: "comm-001",
		RelType: model.RelBelongsTo, CreatedAt: time.Now(),
	}))
	require.NoError(t, svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-control", FromNodeID: "sat-001", ToNodeID: "gs-001",
		RelType: model.RelControlledBy, CreatedAt: time.Now(),
	}))

	// 查询 sat-001 的出边
	out, err := svc.QueryRelationships(ctx, "sat-001", "out", "")
	require.NoError(t, err)
	assert.Len(t, out, 2)

	// Traverse 从 comm-001 出发，方向 in（反向查找归属关系）
	inRels, err := svc.TraverseGraph(ctx, "comm-001", TraverseOptions{Direction: "in", MaxDepth: 1})
	require.NoError(t, err)
	require.Len(t, inRels, 1)
	assert.Equal(t, "rel-belong", inRels[0].RelID)

	// Traverse 从 gs-001 出发，方向 in
	inGs, err := svc.TraverseGraph(ctx, "gs-001", TraverseOptions{Direction: "in", MaxDepth: 1})
	require.NoError(t, err)
	require.Len(t, inGs, 1)
	assert.Equal(t, "rel-control", inGs[0].RelID)

	// 更新卫星状态
	updated, err := svc.UpdateNode(ctx, "sat-001", map[string]any{"status": "Standby"})
	require.NoError(t, err)
	assert.Equal(t, "Standby", updated.Status)

	// 列出所有卫星
	sats, err := svc.ListNodes(ctx, ListOptions{NodeType: model.NodeTypeSatellite})
	require.NoError(t, err)
	require.Len(t, sats, 1)
	assert.Equal(t, "Standby", sats[0].Status)

	// 删除卫星，其相关关系也应被删除
	require.NoError(t, svc.DeleteNode(ctx, "sat-001"))

	// comm-001 的入边应已清空
	inAfter, err := svc.QueryRelationships(ctx, "comm-001", "in", "")
	require.NoError(t, err)
	assert.Empty(t, inAfter)

	// gs-001 的入边应已清空
	inGsAfter, err := svc.QueryRelationships(ctx, "gs-001", "in", "")
	require.NoError(t, err)
	assert.Empty(t, inGsAfter)
}

// TestKGService_DeleteRelationship 校验删除关系。
func TestKGService_DeleteRelationship(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "gs-001", NodeType: model.NodeTypeGroundStation,
		Name: "地面站", Status: "Active", OwnerCommunityID: "comm-001",
	}))
	require.NoError(t, svc.CreateRelationship(ctx, &model.Relationship{
		RelID: "rel-1", FromNodeID: "sat-001", ToNodeID: "gs-001",
		RelType: model.RelControlledBy, CreatedAt: time.Now(),
	}))

	require.NoError(t, svc.DeleteRelationship(ctx, "rel-1"))

	out, err := svc.QueryRelationships(ctx, "sat-001", "out", "")
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestKGService_ListNodes 校验通过 service 列出节点。
func TestKGService_ListNodes(t *testing.T) {
	svc, _, _, _ := newKGService(t)
	ctx := context.Background()

	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "sat-001", NodeType: model.NodeTypeSatellite,
		Name: "卫星1", Status: "Active", OwnerCommunityID: "comm-a",
	}))
	require.NoError(t, svc.RegisterNode(ctx, &model.Node{
		NodeID: "gs-001", NodeType: model.NodeTypeGroundStation,
		Name: "地面站1", Status: "Active", OwnerCommunityID: "comm-b",
	}))

	all, err := svc.ListNodes(ctx, ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	sats, err := svc.ListNodes(ctx, ListOptions{NodeType: model.NodeTypeSatellite})
	require.NoError(t, err)
	assert.Len(t, sats, 1)
	assert.Equal(t, "sat-001", sats[0].NodeID)
}

// TestNewKGService_NilLogger 校验 nil logger 使用默认值。
func TestNewKGService_NilLogger(t *testing.T) {
	db := newTestDB(t)
	svc := NewKGService(NewSQLiteNodeRepository(db), NewSQLiteRelationshipRepository(db), &captureBus{}, nil)
	assert.NotNil(t, svc.logger)
}
