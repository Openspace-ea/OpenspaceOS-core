package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/internal/usage"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// KGServiceServer 是 Openspace OS Core gRPC 服务接口，覆盖知识图谱、事件总线与 Schema 管理。
type KGServiceServer interface {
	// CreateNode 创建 Node
	CreateNode(ctx context.Context, req *CreateNodeRequest) (*Node, error)
	// GetNode 查询 Node
	GetNode(ctx context.Context, req *GetNodeRequest) (*Node, error)
	// UpdateNode 更新 Node
	UpdateNode(ctx context.Context, req *UpdateNodeRequest) (*Node, error)
	// DeleteNode 删除 Node
	DeleteNode(ctx context.Context, req *DeleteNodeRequest) (*emptypb.Empty, error)
	// ListNodes 列表查询 Node
	ListNodes(ctx context.Context, req *ListNodesRequest) (*ListNodesResponse, error)
	// CreateRelationship 建立关系
	CreateRelationship(ctx context.Context, req *CreateRelationshipRequest) (*Relationship, error)
	// DeleteRelationship 删除关系
	DeleteRelationship(ctx context.Context, req *DeleteRelationshipRequest) (*emptypb.Empty, error)
	// QueryGraph 图遍历
	QueryGraph(ctx context.Context, req *QueryGraphRequest) (*QueryGraphResponse, error)
	// ReplayEvents 事件回放
	ReplayEvents(ctx context.Context, req *ReplayEventsRequest) (*ReplayEventsResponse, error)
	// ListSchemas 列出 Schema
	ListSchemas(ctx context.Context, req *ListSchemasRequest) (*ListSchemasResponse, error)
	// SubscribeEvents 事件订阅（服务端流）
	SubscribeEvents(req *SubscribeEventsRequest, stream KGService_SubscribeEventsServer) error
	// IngestTelemetry 遥测批量上报（客户端流，T5.2）
	IngestTelemetry(stream KGService_IngestTelemetryServer) error
}

// UnimplementedKGServiceServer 返回未实现错误，用于前向兼容
type UnimplementedKGServiceServer struct{}

func (*UnimplementedKGServiceServer) CreateNode(context.Context, *CreateNodeRequest) (*Node, error) {
	return nil, status.Error(codes.Unimplemented, "CreateNode 未实现")
}
func (*UnimplementedKGServiceServer) GetNode(context.Context, *GetNodeRequest) (*Node, error) {
	return nil, status.Error(codes.Unimplemented, "GetNode 未实现")
}
func (*UnimplementedKGServiceServer) UpdateNode(context.Context, *UpdateNodeRequest) (*Node, error) {
	return nil, status.Error(codes.Unimplemented, "UpdateNode 未实现")
}
func (*UnimplementedKGServiceServer) DeleteNode(context.Context, *DeleteNodeRequest) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "DeleteNode 未实现")
}
func (*UnimplementedKGServiceServer) ListNodes(context.Context, *ListNodesRequest) (*ListNodesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListNodes 未实现")
}
func (*UnimplementedKGServiceServer) CreateRelationship(context.Context, *CreateRelationshipRequest) (*Relationship, error) {
	return nil, status.Error(codes.Unimplemented, "CreateRelationship 未实现")
}
func (*UnimplementedKGServiceServer) DeleteRelationship(context.Context, *DeleteRelationshipRequest) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "DeleteRelationship 未实现")
}
func (*UnimplementedKGServiceServer) QueryGraph(context.Context, *QueryGraphRequest) (*QueryGraphResponse, error) {
	return nil, status.Error(codes.Unimplemented, "QueryGraph 未实现")
}
func (*UnimplementedKGServiceServer) ReplayEvents(context.Context, *ReplayEventsRequest) (*ReplayEventsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ReplayEvents 未实现")
}
func (*UnimplementedKGServiceServer) ListSchemas(context.Context, *ListSchemasRequest) (*ListSchemasResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListSchemas 未实现")
}
func (*UnimplementedKGServiceServer) SubscribeEvents(*SubscribeEventsRequest, KGService_SubscribeEventsServer) error {
	return status.Error(codes.Unimplemented, "SubscribeEvents 未实现")
}
func (*UnimplementedKGServiceServer) IngestTelemetry(KGService_IngestTelemetryServer) error {
	return status.Error(codes.Unimplemented, "IngestTelemetry 未实现")
}

// KGService_SubscribeEventsServer 是事件订阅服务端流接口
type KGService_SubscribeEventsServer interface {
	Send(*Event) error
	grpc.ServerStream
}

// KGService_IngestTelemetryServer 是遥测批量上报客户端流接口
type KGService_IngestTelemetryServer interface {
	SendAndClose(*IngestTelemetryResponse) error
	Recv() (*TelemetryFrame, error)
	grpc.ServerStream
}

// kgServer 是 KGServiceServer 的实现，持有领域服务、事件总线与 Schema 注册中心
type kgServer struct {
	UnimplementedKGServiceServer
	kg       *core.KGService
	bus      core.MessageBus
	registry *event.SchemaRegistry
	logger   *slog.Logger

	// 遥测 ingest 依赖（T5.2/5.6）：流水线用于入总线；usage 用于流量计量。
	pipeline        *telemetry.Pipeline
	usageCollector  *usage.Collector
	usageMetrics    *usage.Metrics
}

// newKGServer 创建 gRPC 服务实现
func newKGServer(kg *core.KGService, bus core.MessageBus, registry *event.SchemaRegistry, logger *slog.Logger) *kgServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &kgServer{
		kg:       kg,
		bus:      bus,
		registry: registry,
		logger:   logger,
	}
}

// setTelemetryIngress 注入遥测流水线与用量计量，供 IngestTelemetry 使用（T5.2/5.6）。
func (s *kgServer) setTelemetryIngress(p *telemetry.Pipeline, collector *usage.Collector, metrics *usage.Metrics) {
	s.pipeline = p
	s.usageCollector = collector
	s.usageMetrics = metrics
}

// CreateNode 创建 Node
func (s *kgServer) CreateNode(ctx context.Context, req *CreateNodeRequest) (*Node, error) {
	if req.Node == nil {
		return nil, status.Error(codes.InvalidArgument, "node 不能为空")
	}
	node := nodeFromProto(req.Node)

	// 基本校验
	if node.NodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId 不能为空")
	}
	if node.NodeType == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeType 不能为空")
	}
	if !node.NodeType.Valid() {
		return nil, status.Error(codes.InvalidArgument, "未知的 nodeType: "+string(node.NodeType))
	}
	if node.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name 不能为空")
	}
	if node.Status == "" {
		return nil, status.Error(codes.InvalidArgument, "status 不能为空")
	}
	if node.OwnerCommunityID == "" {
		return nil, status.Error(codes.InvalidArgument, "ownerCommunityId 不能为空")
	}

	if err := s.kg.RegisterNode(ctx, node); err != nil {
		return nil, toStatusError(err)
	}
	return nodeToProto(node), nil
}

// GetNode 查询 Node
func (s *kgServer) GetNode(ctx context.Context, req *GetNodeRequest) (*Node, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId 不能为空")
	}
	node, err := s.kg.GetNode(ctx, req.NodeId)
	if err != nil {
		return nil, toStatusError(err)
	}
	return nodeToProto(node), nil
}

// UpdateNode 更新 Node
func (s *kgServer) UpdateNode(ctx context.Context, req *UpdateNodeRequest) (*Node, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId 不能为空")
	}
	if len(req.Changes) == 0 {
		return nil, status.Error(codes.InvalidArgument, "changes 不能为空")
	}
	changes := stringMapToAnyMap(req.Changes)
	node, err := s.kg.UpdateNode(ctx, req.NodeId, changes)
	if err != nil {
		return nil, toStatusError(err)
	}
	return nodeToProto(node), nil
}

// DeleteNode 删除 Node
func (s *kgServer) DeleteNode(ctx context.Context, req *DeleteNodeRequest) (*emptypb.Empty, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId 不能为空")
	}
	// 先检查节点是否存在，不存在则返回 NotFound
	if _, err := s.kg.GetNode(ctx, req.NodeId); err != nil {
		return nil, toStatusError(err)
	}
	if err := s.kg.DeleteNode(ctx, req.NodeId); err != nil {
		return nil, toStatusError(err)
	}
	return &emptypb.Empty{}, nil
}

// ListNodes 列表查询 Node
func (s *kgServer) ListNodes(ctx context.Context, req *ListNodesRequest) (*ListNodesResponse, error) {
	opts := core.ListOptions{
		NodeType:         model.NodeType(req.NodeType),
		OwnerCommunityID: req.OwnerCommunityId,
		Status:           req.Status,
		Limit:            int(req.Limit),
		Offset:           int(req.Offset),
	}
	nodes, err := s.kg.ListNodes(ctx, opts)
	if err != nil {
		return nil, toStatusError(err)
	}
	protoNodes := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		protoNodes = append(protoNodes, nodeToProto(n))
	}
	return &ListNodesResponse{Nodes: protoNodes}, nil
}

// CreateRelationship 建立关系
func (s *kgServer) CreateRelationship(ctx context.Context, req *CreateRelationshipRequest) (*Relationship, error) {
	if req.FromNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "fromNodeId 不能为空")
	}
	if req.ToNodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "toNodeId 不能为空")
	}
	if req.RelType == "" {
		return nil, status.Error(codes.InvalidArgument, "relType 不能为空")
	}
	if !model.RelationshipType(req.RelType).Valid() {
		return nil, status.Error(codes.InvalidArgument, "未知的 relType: "+req.RelType)
	}

	rel := &model.Relationship{
		RelID:      uuid.NewString(),
		FromNodeID: req.FromNodeId,
		ToNodeID:   req.ToNodeId,
		RelType:    model.RelationshipType(req.RelType),
		Properties: stringMapToAnyMap(req.Properties),
	}

	if err := s.kg.CreateRelationship(ctx, rel); err != nil {
		return nil, toStatusError(err)
	}
	return relToProto(rel), nil
}

// DeleteRelationship 删除关系
func (s *kgServer) DeleteRelationship(ctx context.Context, req *DeleteRelationshipRequest) (*emptypb.Empty, error) {
	if req.RelId == "" {
		return nil, status.Error(codes.InvalidArgument, "relId 不能为空")
	}
	if err := s.kg.DeleteRelationship(ctx, req.RelId); err != nil {
		return nil, toStatusError(err)
	}
	return &emptypb.Empty{}, nil
}

// QueryGraph 图遍历
func (s *kgServer) QueryGraph(ctx context.Context, req *QueryGraphRequest) (*QueryGraphResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "nodeId 不能为空")
	}
	direction := req.Direction
	if direction == "" {
		direction = "out"
	}
	opts := core.TraverseOptions{
		RelType:   model.RelationshipType(req.RelType),
		Direction: direction,
	}
	if req.MaxDepth > 0 {
		opts.MaxDepth = int(req.MaxDepth)
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 1
	}

	rels, err := s.kg.TraverseGraph(ctx, req.NodeId, opts)
	if err != nil {
		return nil, toStatusError(err)
	}
	protoRels := make([]*Relationship, 0, len(rels))
	for _, r := range rels {
		protoRels = append(protoRels, relToProto(r))
	}
	return &QueryGraphResponse{Relationships: protoRels}, nil
}

// ReplayEvents 事件回放
func (s *kgServer) ReplayEvents(ctx context.Context, req *ReplayEventsRequest) (*ReplayEventsResponse, error) {
	opts := core.ReplayOptions{
		SourceNodeID: req.SourceNodeId,
		Limit:        int(req.Limit),
	}

	// 转换事件类型
	for _, t := range req.EventTypes {
		t = strings.TrimSpace(t)
		if t != "" {
			opts.EventTypes = append(opts.EventTypes, event.EventType(t))
		}
	}

	// 解析时间范围
	if req.StartTime != "" {
		t, err := time.Parse(time.RFC3339, req.StartTime)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "startTime 格式无效，需 RFC3339: "+err.Error())
		}
		opts.StartTime = &t
	}
	if req.EndTime != "" {
		t, err := time.Parse(time.RFC3339, req.EndTime)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "endTime 格式无效，需 RFC3339: "+err.Error())
		}
		opts.EndTime = &t
	}

	events, err := s.bus.Replay(ctx, opts)
	if err != nil {
		return nil, toStatusError(err)
	}
	protoEvents := make([]*Event, 0, len(events))
	for _, e := range events {
		protoEvents = append(protoEvents, eventToProto(e))
	}
	return &ReplayEventsResponse{Events: protoEvents}, nil
}

// ListSchemas 列出 Schema
func (s *kgServer) ListSchemas(ctx context.Context, req *ListSchemasRequest) (*ListSchemasResponse, error) {
	schemas := s.registry.ListSchemas()
	protoSchemas := make([]*Schema, 0, len(schemas))
	for _, sc := range schemas {
		protoSchemas = append(protoSchemas, schemaToProto(sc))
	}
	return &ListSchemasResponse{Schemas: protoSchemas}, nil
}

// SubscribeEvents 事件订阅（服务端流）
func (s *kgServer) SubscribeEvents(req *SubscribeEventsRequest, stream KGService_SubscribeEventsServer) error {
	var eventTypes []event.EventType
	for _, t := range req.EventTypes {
		t = strings.TrimSpace(t)
		if t != "" {
			eventTypes = append(eventTypes, event.EventType(t))
		}
	}

	ch, unsubscribe := s.bus.Subscribe(core.SubscribeOptions{EventTypes: eventTypes})
	defer unsubscribe()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(eventToProto(e)); err != nil {
				return err
			}
		}
	}
}

// grpcIngestBatch 是 gRPC 遥测客户端流单次 flush 的帧数阈值（T5.2/5.4）。
const grpcIngestBatch = 512

// IngestTelemetry 遥测批量上报（客户端流，T5.2）。
//
// 客户端可流式发送多帧；服务端按 grpcIngestBatch 阈值分批 flush 到总线，
// 降低逐请求写放大（T5.4）。全部接收后返回接受/总数，并计量 usage（T5.6）。
func (s *kgServer) IngestTelemetry(stream KGService_IngestTelemetryServer) error {
	if s.pipeline == nil {
		return status.Error(codes.Unimplemented, "遥测流水线未启用")
	}
	ctx := stream.Context()
	batch := make([]plugin.TelemetryFrame, 0, grpcIngestBatch)
	accepted, total := 0, 0
	var usageBytes int64

	flush := func() int {
		if len(batch) == 0 {
			return 0
		}
		n, _ := s.pipeline.IngestFrames(ctx, batch)
		batch = batch[:0]
		return n
	}

	for {
		f, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		total++
		pf := telemetryFrameFromProto(f)
		if b, err := json.Marshal(pf); err == nil {
			usageBytes += int64(len(b))
		}
		batch = append(batch, pf)
		if len(batch) >= grpcIngestBatch {
			accepted += flush()
		}
	}
	accepted += flush()

	// 用量计量（T5.6）
	usage.RecordIngest(ctx, s.usageCollector, s.usageMetrics, "/grpc/IngestTelemetry", "create", int64(total), usageBytes)
	s.logger.Info("gRPC 遥测批量上报完成", "total", total, "accepted", accepted)
	return stream.SendAndClose(&IngestTelemetryResponse{Accepted: int32(accepted), Total: int32(total)})
}

// telemetryFrameFromProto 将 gRPC TelemetryFrame 消息转换为插件遥测帧。
func telemetryFrameFromProto(f *TelemetryFrame) plugin.TelemetryFrame {
	ts := time.Now()
	if f.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, f.Timestamp); err == nil {
			ts = t
		}
	}
	params := make(map[string]any, len(f.Parameters))
	for k, v := range f.Parameters {
		params[k] = v
	}
	return plugin.TelemetryFrame{
		SatelliteID: f.SatelliteId,
		Timestamp:   ts,
		Parameters:  params,
		Quality:     f.Quality,
	}
}

// toStatusError 将领域服务错误转换为 gRPC 状态错误
func toStatusError(err error) error {
	if errors.Is(err, core.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// ===== 服务描述符与方法处理器 =====

// RegisterKGServiceServer 注册 KGServiceServer 到 gRPC 服务器
func RegisterKGServiceServer(s *grpc.Server, srv KGServiceServer) {
	s.RegisterService(&KGService_ServiceDesc, srv)
}

// KGService_ServiceDesc 是 KGService 的 gRPC 服务描述符
var KGService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "openspace_os.core.v1.OpenspaceOSCoreService",
	HandlerType: (*KGServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "CreateNode", Handler: _KGService_CreateNode_Handler},
		{MethodName: "GetNode", Handler: _KGService_GetNode_Handler},
		{MethodName: "UpdateNode", Handler: _KGService_UpdateNode_Handler},
		{MethodName: "DeleteNode", Handler: _KGService_DeleteNode_Handler},
		{MethodName: "ListNodes", Handler: _KGService_ListNodes_Handler},
		{MethodName: "CreateRelationship", Handler: _KGService_CreateRelationship_Handler},
		{MethodName: "DeleteRelationship", Handler: _KGService_DeleteRelationship_Handler},
		{MethodName: "QueryGraph", Handler: _KGService_QueryGraph_Handler},
		{MethodName: "ReplayEvents", Handler: _KGService_ReplayEvents_Handler},
		{MethodName: "ListSchemas", Handler: _KGService_ListSchemas_Handler},
	},
	Streams: []grpc.StreamDesc{
		{StreamName: "SubscribeEvents", Handler: _KGService_SubscribeEvents_Handler, ServerStreams: true},
		{StreamName: "IngestTelemetry", Handler: _KGService_IngestTelemetry_Handler, ClientStreams: true},
	},
	Metadata: "internal/api/grpc/proto/openspace_os_core.proto",
}

// 一元方法处理器
func _KGService_CreateNode_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(CreateNodeRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).CreateNode(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/CreateNode"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).CreateNode(ctx, req.(*CreateNodeRequest))
	})
}

func _KGService_GetNode_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(GetNodeRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).GetNode(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/GetNode"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).GetNode(ctx, req.(*GetNodeRequest))
	})
}

func _KGService_UpdateNode_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(UpdateNodeRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).UpdateNode(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/UpdateNode"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).UpdateNode(ctx, req.(*UpdateNodeRequest))
	})
}

func _KGService_DeleteNode_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(DeleteNodeRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).DeleteNode(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/DeleteNode"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).DeleteNode(ctx, req.(*DeleteNodeRequest))
	})
}

func _KGService_ListNodes_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(ListNodesRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).ListNodes(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/ListNodes"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).ListNodes(ctx, req.(*ListNodesRequest))
	})
}

func _KGService_CreateRelationship_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(CreateRelationshipRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).CreateRelationship(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/CreateRelationship"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).CreateRelationship(ctx, req.(*CreateRelationshipRequest))
	})
}

func _KGService_DeleteRelationship_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(DeleteRelationshipRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).DeleteRelationship(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/DeleteRelationship"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).DeleteRelationship(ctx, req.(*DeleteRelationshipRequest))
	})
}

func _KGService_QueryGraph_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(QueryGraphRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).QueryGraph(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/QueryGraph"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).QueryGraph(ctx, req.(*QueryGraphRequest))
	})
}

func _KGService_ReplayEvents_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(ReplayEventsRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).ReplayEvents(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/ReplayEvents"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).ReplayEvents(ctx, req.(*ReplayEventsRequest))
	})
}

func _KGService_ListSchemas_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(ListSchemasRequest)
	if err := dec(req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(KGServiceServer).ListSchemas(ctx, req)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/openspace_os.core.v1.OpenspaceOSCoreService/ListSchemas"}
	return interceptor(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(KGServiceServer).ListSchemas(ctx, req.(*ListSchemasRequest))
	})
}

// 流方法处理器
func _KGService_SubscribeEvents_Handler(srv interface{}, stream grpc.ServerStream) error {
	req := new(SubscribeEventsRequest)
	if err := stream.RecvMsg(req); err != nil {
		return err
	}
	return srv.(KGServiceServer).SubscribeEvents(req, &kgServiceSubscribeEventsServer{stream})
}

// kgServiceSubscribeEventsServer 包装 grpc.ServerStream，提供类型安全的 Send 方法
type kgServiceSubscribeEventsServer struct {
	grpc.ServerStream
}

func (x *kgServiceSubscribeEventsServer) Send(m *Event) error {
	return x.ServerStream.SendMsg(m)
}

// 客户端流方法处理器（T5.2）
func _KGService_IngestTelemetry_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(KGServiceServer).IngestTelemetry(&kgServiceIngestTelemetryServer{stream})
}

// kgServiceIngestTelemetryServer 包装 grpc.ServerStream，提供类型安全的 Recv 与 SendAndClose
type kgServiceIngestTelemetryServer struct {
	grpc.ServerStream
}

func (x *kgServiceIngestTelemetryServer) Recv() (*TelemetryFrame, error) {
	m := new(TelemetryFrame)
	if err := x.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (x *kgServiceIngestTelemetryServer) SendAndClose(m *IngestTelemetryResponse) error {
	return x.SendMsg(m)
}

// ===== gRPC 客户端 =====

// KGServiceClient 是 Openspace OS Core gRPC 服务的客户端接口
type KGServiceClient interface {
	CreateNode(ctx context.Context, req *CreateNodeRequest, opts ...grpc.CallOption) (*Node, error)
	GetNode(ctx context.Context, req *GetNodeRequest, opts ...grpc.CallOption) (*Node, error)
	UpdateNode(ctx context.Context, req *UpdateNodeRequest, opts ...grpc.CallOption) (*Node, error)
	DeleteNode(ctx context.Context, req *DeleteNodeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	ListNodes(ctx context.Context, req *ListNodesRequest, opts ...grpc.CallOption) (*ListNodesResponse, error)
	CreateRelationship(ctx context.Context, req *CreateRelationshipRequest, opts ...grpc.CallOption) (*Relationship, error)
	DeleteRelationship(ctx context.Context, req *DeleteRelationshipRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	QueryGraph(ctx context.Context, req *QueryGraphRequest, opts ...grpc.CallOption) (*QueryGraphResponse, error)
	ReplayEvents(ctx context.Context, req *ReplayEventsRequest, opts ...grpc.CallOption) (*ReplayEventsResponse, error)
	ListSchemas(ctx context.Context, req *ListSchemasRequest, opts ...grpc.CallOption) (*ListSchemasResponse, error)
	SubscribeEvents(ctx context.Context, req *SubscribeEventsRequest, opts ...grpc.CallOption) (KGService_SubscribeEventsClient, error)
	// IngestTelemetry 遥测批量上报（客户端流，T5.2）
	IngestTelemetry(ctx context.Context, opts ...grpc.CallOption) (KGService_IngestTelemetryClient, error)
}

// KGService_SubscribeEventsClient 是事件订阅客户端流接口
type KGService_SubscribeEventsClient interface {
	Recv() (*Event, error)
	grpc.ClientStream
}

// KGService_IngestTelemetryClient 是遥测上报客户端流接口
type KGService_IngestTelemetryClient interface {
	Send(*TelemetryFrame) error
	CloseAndRecv() (*IngestTelemetryResponse, error)
	grpc.ClientStream
}

type kgServiceClient struct {
	cc *grpc.ClientConn
}

// NewKGServiceClient 创建 gRPC 客户端
func NewKGServiceClient(cc *grpc.ClientConn) KGServiceClient {
	return &kgServiceClient{cc}
}

func (c *kgServiceClient) CreateNode(ctx context.Context, req *CreateNodeRequest, opts ...grpc.CallOption) (*Node, error) {
	resp := new(Node)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/CreateNode", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) GetNode(ctx context.Context, req *GetNodeRequest, opts ...grpc.CallOption) (*Node, error) {
	resp := new(Node)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/GetNode", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) UpdateNode(ctx context.Context, req *UpdateNodeRequest, opts ...grpc.CallOption) (*Node, error) {
	resp := new(Node)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/UpdateNode", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) DeleteNode(ctx context.Context, req *DeleteNodeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	resp := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/DeleteNode", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) ListNodes(ctx context.Context, req *ListNodesRequest, opts ...grpc.CallOption) (*ListNodesResponse, error) {
	resp := new(ListNodesResponse)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/ListNodes", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) CreateRelationship(ctx context.Context, req *CreateRelationshipRequest, opts ...grpc.CallOption) (*Relationship, error) {
	resp := new(Relationship)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/CreateRelationship", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) DeleteRelationship(ctx context.Context, req *DeleteRelationshipRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	resp := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/DeleteRelationship", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) QueryGraph(ctx context.Context, req *QueryGraphRequest, opts ...grpc.CallOption) (*QueryGraphResponse, error) {
	resp := new(QueryGraphResponse)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/QueryGraph", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) ReplayEvents(ctx context.Context, req *ReplayEventsRequest, opts ...grpc.CallOption) (*ReplayEventsResponse, error) {
	resp := new(ReplayEventsResponse)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/ReplayEvents", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) ListSchemas(ctx context.Context, req *ListSchemasRequest, opts ...grpc.CallOption) (*ListSchemasResponse, error) {
	resp := new(ListSchemasResponse)
	err := c.cc.Invoke(ctx, "/openspace_os.core.v1.OpenspaceOSCoreService/ListSchemas", req, resp, opts...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *kgServiceClient) SubscribeEvents(ctx context.Context, req *SubscribeEventsRequest, opts ...grpc.CallOption) (KGService_SubscribeEventsClient, error) {
	stream, err := c.cc.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "SubscribeEvents",
		ServerStreams: true,
	}, "/openspace_os.core.v1.OpenspaceOSCoreService/SubscribeEvents", opts...)
	if err != nil {
		return nil, err
	}
	x := &kgServiceSubscribeEventsClient{ClientStream: stream}
	if err := x.ClientStream.SendMsg(req); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type kgServiceSubscribeEventsClient struct {
	grpc.ClientStream
}

func (x *kgServiceSubscribeEventsClient) Recv() (*Event, error) {
	m := new(Event)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *kgServiceClient) IngestTelemetry(ctx context.Context, opts ...grpc.CallOption) (KGService_IngestTelemetryClient, error) {
	stream, err := c.cc.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "IngestTelemetry",
		ClientStreams: true,
	}, "/openspace_os.core.v1.OpenspaceOSCoreService/IngestTelemetry", opts...)
	if err != nil {
		return nil, err
	}
	return &kgServiceIngestTelemetryClient{ClientStream: stream}, nil
}

type kgServiceIngestTelemetryClient struct {
	grpc.ClientStream
}

func (x *kgServiceIngestTelemetryClient) Send(m *TelemetryFrame) error {
	return x.ClientStream.SendMsg(m)
}

func (x *kgServiceIngestTelemetryClient) CloseAndRecv() (*IngestTelemetryResponse, error) {
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	m := new(IngestTelemetryResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}
