package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// newTestServer 创建用于测试的 gRPC 进程内服务器，使用 bufconn 和内存 SQLite。
// 返回客户端实例与清理函数。
func newTestServer(t *testing.T) (KGServiceClient, func()) {
	t.Helper()

	db, err := core.InitDB(":memory:")
	require.NoError(t, err)

	nodeRepo := core.NewSQLiteNodeRepository(db)
	relRepo := core.NewSQLiteRelationshipRepository(db)
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)

	kg := core.NewKGService(nodeRepo, relRepo, bus, nil)
	kg.SetDB(db)

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	srv := grpclib.NewServer()
	RegisterKGServiceServer(srv, newKGServer(kg, bus, registry, nil))
	go func() {
		_ = srv.Serve(lis)
	}()

	conn, err := grpclib.DialContext(context.Background(), "bufnet",
		grpclib.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpclib.WithTransportCredentials(insecure.NewCredentials()),
		grpclib.WithDefaultCallOptions(grpclib.CallContentSubtype("aos")),
	)
	require.NoError(t, err)

	client := NewKGServiceClient(conn)

	cleanup := func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = bus.Close()
		_ = db.Close()
	}

	return client, cleanup
}

// TestNodeCRUD 测试 Node 的完整 CRUD 流程
func TestNodeCRUD(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// Create
	createResp, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId:           "sat-1",
			NodeType:         "Satellite",
			Name:             "测试卫星",
			Status:           "active",
			OwnerCommunityId: "comm-1",
			Properties:       map[string]string{"orbit": "LEO"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "sat-1", createResp.NodeId)
	assert.Equal(t, "Satellite", createResp.NodeType)
	assert.Equal(t, "测试卫星", createResp.Name)
	assert.Equal(t, "active", createResp.Status)
	assert.NotEmpty(t, createResp.CreatedAt)
	assert.NotEmpty(t, createResp.UpdatedAt)
	assert.Equal(t, "LEO", createResp.Properties["orbit"])

	// Get
	getResp, err := client.GetNode(ctx, &GetNodeRequest{NodeId: "sat-1"})
	require.NoError(t, err)
	assert.Equal(t, "测试卫星", getResp.Name)
	assert.Equal(t, "Satellite", getResp.NodeType)

	// Update
	updateResp, err := client.UpdateNode(ctx, &UpdateNodeRequest{
		NodeId:  "sat-1",
		Changes: map[string]string{"status": "inactive", "name": "更新卫星"},
	})
	require.NoError(t, err)
	assert.Equal(t, "inactive", updateResp.Status)
	assert.Equal(t, "更新卫星", updateResp.Name)

	// 验证更新已持久化
	getResp, err = client.GetNode(ctx, &GetNodeRequest{NodeId: "sat-1"})
	require.NoError(t, err)
	assert.Equal(t, "inactive", getResp.Status)
	assert.Equal(t, "更新卫星", getResp.Name)

	// List
	listResp, err := client.ListNodes(ctx, &ListNodesRequest{NodeType: "Satellite"})
	require.NoError(t, err)
	assert.Len(t, listResp.Nodes, 1)

	// 按状态过滤
	listResp, err = client.ListNodes(ctx, &ListNodesRequest{Status: "inactive"})
	require.NoError(t, err)
	assert.Len(t, listResp.Nodes, 1)

	listResp, err = client.ListNodes(ctx, &ListNodesRequest{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, listResp.Nodes, 0)

	// Delete
	_, err = client.DeleteNode(ctx, &DeleteNodeRequest{NodeId: "sat-1"})
	require.NoError(t, err)

	// 验证已删除
	_, err = client.GetNode(ctx, &GetNodeRequest{NodeId: "sat-1"})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestCreateNodeValidation 测试创建 Node 时的参数校验
func TestCreateNodeValidation(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	tests := []struct {
		name string
		req  *CreateNodeRequest
		code codes.Code
	}{
		{
			name: "nodeId 为空",
			req:  &CreateNodeRequest{Node: &Node{NodeType: "Satellite", Name: "n", Status: "s", OwnerCommunityId: "c"}},
			code: codes.InvalidArgument,
		},
		{
			name: "nodeType 为空",
			req:  &CreateNodeRequest{Node: &Node{NodeId: "n1", Name: "n", Status: "s", OwnerCommunityId: "c"}},
			code: codes.InvalidArgument,
		},
		{
			name: "未知的 nodeType",
			req:  &CreateNodeRequest{Node: &Node{NodeId: "n1", NodeType: "Unknown", Name: "n", Status: "s", OwnerCommunityId: "c"}},
			code: codes.InvalidArgument,
		},
		{
			name: "name 为空",
			req:  &CreateNodeRequest{Node: &Node{NodeId: "n1", NodeType: "Satellite", Status: "s", OwnerCommunityId: "c"}},
			code: codes.InvalidArgument,
		},
		{
			name: "node 为 nil",
			req:  &CreateNodeRequest{},
			code: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateNode(ctx, tt.req)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.code, st.Code())
		})
	}
}

// TestGetNodeNotFound 测试查询不存在的 Node
func TestGetNodeNotFound(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, err := client.GetNode(ctx, &GetNodeRequest{NodeId: "non-existent"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestDeleteNodeNotFound 测试删除不存在的 Node
func TestDeleteNodeNotFound(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, err := client.DeleteNode(ctx, &DeleteNodeRequest{NodeId: "non-existent"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestRelationshipAndGraph 测试关系创建与图遍历查询
func TestRelationshipAndGraph(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 创建两个节点
	_, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId: "sat-1", NodeType: "Satellite", Name: "卫星", Status: "active", OwnerCommunityId: "comm-1",
		},
	})
	require.NoError(t, err)

	_, err = client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId: "gs-1", NodeType: "GroundStation", Name: "地面站", Status: "active", OwnerCommunityId: "comm-1",
		},
	})
	require.NoError(t, err)

	// 创建关系
	relResp, err := client.CreateRelationship(ctx, &CreateRelationshipRequest{
		FromNodeId: "sat-1",
		ToNodeId:   "gs-1",
		RelType:    "controlledBy",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, relResp.RelId)
	assert.Equal(t, "controlledBy", relResp.RelType)
	assert.Equal(t, "sat-1", relResp.FromNodeId)
	assert.Equal(t, "gs-1", relResp.ToNodeId)

	// 图遍历（出边）
	graphResp, err := client.QueryGraph(ctx, &QueryGraphRequest{
		NodeId: "sat-1", Direction: "out", MaxDepth: 1,
	})
	require.NoError(t, err)
	assert.Len(t, graphResp.Relationships, 1)
	assert.Equal(t, "gs-1", graphResp.Relationships[0].ToNodeId)

	// 图遍历（入边）
	graphResp, err = client.QueryGraph(ctx, &QueryGraphRequest{
		NodeId: "gs-1", Direction: "in", MaxDepth: 1,
	})
	require.NoError(t, err)
	assert.Len(t, graphResp.Relationships, 1)
	assert.Equal(t, "sat-1", graphResp.Relationships[0].FromNodeId)

	// 删除关系
	_, err = client.DeleteRelationship(ctx, &DeleteRelationshipRequest{RelId: relResp.RelId})
	require.NoError(t, err)

	// 验证关系已删除
	graphResp, err = client.QueryGraph(ctx, &QueryGraphRequest{
		NodeId: "sat-1", Direction: "out", MaxDepth: 1,
	})
	require.NoError(t, err)
	assert.Len(t, graphResp.Relationships, 0)
}

// TestCreateRelationshipValidation 测试创建关系时的参数校验
func TestCreateRelationshipValidation(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 先创建一个节点用于测试
	_, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{NodeId: "n1", NodeType: "Satellite", Name: "n1", Status: "s", OwnerCommunityId: "c"},
	})
	require.NoError(t, err)
	_, err = client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{NodeId: "n2", NodeType: "Satellite", Name: "n2", Status: "s", OwnerCommunityId: "c"},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		req  *CreateRelationshipRequest
		code codes.Code
	}{
		{name: "fromNodeId 为空", req: &CreateRelationshipRequest{ToNodeId: "n2", RelType: "controlledBy"}, code: codes.InvalidArgument},
		{name: "toNodeId 为空", req: &CreateRelationshipRequest{FromNodeId: "n1", RelType: "controlledBy"}, code: codes.InvalidArgument},
		{name: "relType 为空", req: &CreateRelationshipRequest{FromNodeId: "n1", ToNodeId: "n2"}, code: codes.InvalidArgument},
		{name: "未知 relType", req: &CreateRelationshipRequest{FromNodeId: "n1", ToNodeId: "n2", RelType: "unknown"}, code: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateRelationship(ctx, tt.req)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.code, st.Code())
		})
	}
}

// TestReplayEvents 测试事件回放
func TestReplayEvents(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 创建节点生成事件（NodeRegistered + StateUpdated）
	_, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId: "sat-1", NodeType: "Satellite", Name: "卫星", Status: "active", OwnerCommunityId: "comm-1",
		},
	})
	require.NoError(t, err)

	// 回放该节点的所有事件
	replayResp, err := client.ReplayEvents(ctx, &ReplayEventsRequest{
		SourceNodeId: "sat-1",
	})
	require.NoError(t, err)
	assert.Len(t, replayResp.Events, 2)

	// 验证事件类型（按时间排序：NodeRegistered 在前，StateUpdated 在后）
	assert.Equal(t, "NodeRegistered", replayResp.Events[0].EventType)
	assert.Equal(t, "StateUpdated", replayResp.Events[1].EventType)

	// 验证事件属性
	assert.Equal(t, "sat-1", replayResp.Events[0].SourceNodeId)
	assert.NotEmpty(t, replayResp.Events[0].EventId)
	assert.NotEmpty(t, replayResp.Events[0].Timestamp)

	// 按事件类型过滤回放
	replayResp, err = client.ReplayEvents(ctx, &ReplayEventsRequest{
		EventTypes: []string{"NodeRegistered"},
	})
	require.NoError(t, err)
	assert.Len(t, replayResp.Events, 1)
	assert.Equal(t, "NodeRegistered", replayResp.Events[0].EventType)
}

// TestListSchemas 测试列出 Schema
func TestListSchemas(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	resp, err := client.ListSchemas(ctx, &ListSchemasRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Schemas)

	// 验证已知事件类型存在
	eventTypes := make(map[string]bool)
	for _, s := range resp.Schemas {
		eventTypes[s.EventType] = true
		assert.NotEmpty(t, s.Version)
		assert.NotNil(t, s.Fields)
	}

	assert.True(t, eventTypes["NodeRegistered"])
	assert.True(t, eventTypes["StateUpdated"])
	assert.True(t, eventTypes["NodeDeleted"])
	assert.True(t, eventTypes["RelationshipCreated"])
}

// TestSubscribeEvents 测试事件订阅流
func TestSubscribeEvents(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 开始订阅（订阅所有事件类型）
	stream, err := client.SubscribeEvents(ctx, &SubscribeEventsRequest{})
	require.NoError(t, err)

	// 等待服务端完成订阅注册
	time.Sleep(200 * time.Millisecond)

	// 创建节点生成事件（NodeRegistered + StateUpdated）
	_, err = client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId: "sat-1", NodeType: "Satellite", Name: "卫星", Status: "active", OwnerCommunityId: "comm-1",
		},
	})
	require.NoError(t, err)

	// 接收事件（带超时）
	eventCh := make(chan *Event, 10)
	errCh := make(chan error, 1)
	go func() {
		for {
			e, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			eventCh <- e
		}
	}()

	var receivedEvents []*Event
	timeout := time.After(3 * time.Second)
	for len(receivedEvents) < 2 {
		select {
		case e := <-eventCh:
			receivedEvents = append(receivedEvents, e)
		case err := <-errCh:
			t.Fatalf("接收事件失败: %v", err)
		case <-timeout:
			t.Fatalf("接收事件超时，已收到 %d 个事件", len(receivedEvents))
		}
	}

	// 验证接收到的事件
	assert.Len(t, receivedEvents, 2)
	assert.Equal(t, "NodeRegistered", receivedEvents[0].EventType)
	assert.Equal(t, "StateUpdated", receivedEvents[1].EventType)
	assert.Equal(t, "sat-1", receivedEvents[0].SourceNodeId)
}

// TestSubscribeEventsWithFilter 测试带事件类型过滤的事件订阅
func TestSubscribeEventsWithFilter(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 订阅仅 NodeRegistered 事件
	stream, err := client.SubscribeEvents(ctx, &SubscribeEventsRequest{
		EventTypes: []string{"NodeRegistered"},
	})
	require.NoError(t, err)

	// 等待服务端完成订阅注册
	time.Sleep(200 * time.Millisecond)

	// 创建节点生成事件
	_, err = client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{
			NodeId: "sat-2", NodeType: "Satellite", Name: "卫星2", Status: "active", OwnerCommunityId: "comm-1",
		},
	})
	require.NoError(t, err)

	// 接收事件
	e, err := stream.Recv()
	if err != nil {
		t.Fatalf("接收事件失败: %v", err)
	}
	assert.Equal(t, "NodeRegistered", e.EventType)

	// 验证不会收到 StateUpdated 事件（因为有过滤器）
	select {
	case e, ok := <-recvAsync(stream):
		if ok {
			t.Fatalf("不应收到额外事件，但收到: eventType=%s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		// 预期超时，正常
	}
}

// recvAsync 将 stream.Recv 转为 channel
func recvAsync(stream KGService_SubscribeEventsClient) <-chan *Event {
	ch := make(chan *Event, 1)
	go func() {
		e, err := stream.Recv()
		if err != nil {
			close(ch)
			return
		}
		ch <- e
	}()
	return ch
}

// TestDeleteNodeCascadesRelationships 测试删除节点时级联删除关系
func TestDeleteNodeCascadesRelationships(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 创建两个节点
	_, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{NodeId: "sat-1", NodeType: "Satellite", Name: "sat", Status: "s", OwnerCommunityId: "c"},
	})
	require.NoError(t, err)
	_, err = client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{NodeId: "gs-1", NodeType: "GroundStation", Name: "gs", Status: "s", OwnerCommunityId: "c"},
	})
	require.NoError(t, err)

	// 创建关系
	rel, err := client.CreateRelationship(ctx, &CreateRelationshipRequest{
		FromNodeId: "sat-1", ToNodeId: "gs-1", RelType: "controlledBy",
	})
	require.NoError(t, err)

	// 删除节点
	_, err = client.DeleteNode(ctx, &DeleteNodeRequest{NodeId: "sat-1"})
	require.NoError(t, err)

	// 验证关系也已删除（通过查询 gs-1 的入边）
	graphResp, err := client.QueryGraph(ctx, &QueryGraphRequest{
		NodeId: "gs-1", Direction: "in", MaxDepth: 1,
	})
	require.NoError(t, err)
	assert.Len(t, graphResp.Relationships, 0)

	// 验证关系确实不存在
	_ = rel.RelId
}

// TestUpdateNodeValidation 测试更新 Node 时的参数校验
func TestUpdateNodeValidation(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// nodeId 为空
	_, err := client.UpdateNode(ctx, &UpdateNodeRequest{
		NodeId:  "",
		Changes: map[string]string{"name": "new"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// changes 为空
	_, err = client.UpdateNode(ctx, &UpdateNodeRequest{
		NodeId: "n1",
	})
	require.Error(t, err)
	st, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// 不存在的节点
	_, err = client.UpdateNode(ctx, &UpdateNodeRequest{
		NodeId:  "non-existent",
		Changes: map[string]string{"name": "new"},
	})
	require.Error(t, err)
	st, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestListNodesPagination 测试列表查询分页
func TestListNodesPagination(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 创建 3 个节点
	for i := 0; i < 3; i++ {
		_, err := client.CreateNode(ctx, &CreateNodeRequest{
			Node: &Node{
				NodeId:           "sat-" + string(rune('1'+i)),
				NodeType:         "Satellite",
				Name:             "卫星",
				Status:           "active",
				OwnerCommunityId: "comm-1",
			},
		})
		require.NoError(t, err)
	}

	// 查询全部
	resp, err := client.ListNodes(ctx, &ListNodesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Nodes, 3)

	// 分页查询：limit=2, offset=0
	resp, err = client.ListNodes(ctx, &ListNodesRequest{Limit: 2, Offset: 0})
	require.NoError(t, err)
	assert.Len(t, resp.Nodes, 2)

	// 分页查询：limit=2, offset=2
	resp, err = client.ListNodes(ctx, &ListNodesRequest{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, resp.Nodes, 1)
}

// TestEmptyResponse 测试 DeleteNode 和 DeleteRelationship 返回 Empty
func TestEmptyResponse(t *testing.T) {
	client, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 创建节点
	_, err := client.CreateNode(ctx, &CreateNodeRequest{
		Node: &Node{NodeId: "n1", NodeType: "Satellite", Name: "n", Status: "s", OwnerCommunityId: "c"},
	})
	require.NoError(t, err)

	// 删除节点，验证返回 Empty
	resp, err := client.DeleteNode(ctx, &DeleteNodeRequest{NodeId: "n1"})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	_, ok := any(resp).(*emptypb.Empty)
	assert.True(t, ok)
}
