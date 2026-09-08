package grpc

import (
	"fmt"
	"time"

	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// 本文件手动定义 protobuf 消息的 Go 结构体，对应 internal/api/grpc/proto/openspace_os_core.proto。
// 由于 Windows 环境可能未安装 protoc 编译器，这里不依赖 protoc 生成的代码，
// 而是手写与 protobuf 兼容的 Go 结构体，并通过自定义 JSON 编解码器（见 codec.go）
// 实现 gRPC 传输。时间字段统一使用 RFC3339 字符串，Properties 统一使用 map[string]string。

// Node 消息，对应 model.Node
type Node struct {
	NodeId           string            `json:"nodeId"`
	NodeType         string            `json:"nodeType"`
	Name             string            `json:"name"`
	Status           string            `json:"status"`
	OwnerCommunityId string            `json:"ownerCommunityId"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
	ShardKey         string            `json:"shardKey,omitempty"`
	FederationId     string            `json:"federationId,omitempty"`
	Properties       map[string]string `json:"properties,omitempty"`
}

// Relationship 消息，对应 model.Relationship
type Relationship struct {
	RelId      string            `json:"relId"`
	FromNodeId string            `json:"fromNodeId"`
	ToNodeId   string            `json:"toNodeId"`
	RelType    string            `json:"relType"`
	Properties map[string]string `json:"properties,omitempty"`
	CreatedAt  string            `json:"createdAt"`
}

// Event 消息，对应 event.Event
type Event struct {
	EventId     string            `json:"eventId"`
	EventType   string            `json:"eventType"`
	Timestamp   string            `json:"timestamp"`
	SourceNodeId string           `json:"sourceNodeId"`
	TraceId     string            `json:"traceId"`
	Payload     map[string]string `json:"payload,omitempty"`
}

// Schema 消息，对应 event.Schema
type Schema struct {
	EventType string     `json:"eventType"`
	Version   string     `json:"version"`
	Fields    []*FieldDef `json:"fields"`
}

// FieldDef 描述 Schema 中的一个字段
type FieldDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// CreateNodeRequest 创建 Node 请求
type CreateNodeRequest struct {
	Node *Node `json:"node"`
}

// GetNodeRequest 查询 Node 请求
type GetNodeRequest struct {
	NodeId string `json:"nodeId"`
}

// UpdateNodeRequest 更新 Node 请求
type UpdateNodeRequest struct {
	NodeId  string            `json:"nodeId"`
	Changes map[string]string `json:"changes"`
}

// DeleteNodeRequest 删除 Node 请求
type DeleteNodeRequest struct {
	NodeId string `json:"nodeId"`
}

// ListNodesRequest 列表查询 Node 请求
type ListNodesRequest struct {
	NodeType         string `json:"nodeType"`
	OwnerCommunityId string `json:"ownerCommunityId"`
	Status           string `json:"status"`
	Limit            int32  `json:"limit"`
	Offset           int32  `json:"offset"`
}

// ListNodesResponse 列表查询 Node 响应
type ListNodesResponse struct {
	Nodes []*Node `json:"nodes"`
}

// CreateRelationshipRequest 建立关系请求
type CreateRelationshipRequest struct {
	FromNodeId string            `json:"fromNodeId"`
	ToNodeId   string            `json:"toNodeId"`
	RelType    string            `json:"relType"`
	Properties map[string]string `json:"properties,omitempty"`
}

// DeleteRelationshipRequest 删除关系请求
type DeleteRelationshipRequest struct {
	RelId string `json:"relId"`
}

// QueryGraphRequest 图遍历请求
type QueryGraphRequest struct {
	NodeId   string `json:"nodeId"`
	Direction string `json:"direction"`
	RelType  string `json:"relType"`
	MaxDepth int32  `json:"maxDepth"`
}

// QueryGraphResponse 图遍历响应
type QueryGraphResponse struct {
	Relationships []*Relationship `json:"relationships"`
}

// ReplayEventsRequest 事件回放请求
type ReplayEventsRequest struct {
	EventTypes   []string `json:"eventTypes"`
	SourceNodeId string   `json:"sourceNodeId"`
	StartTime    string   `json:"startTime"`
	EndTime      string   `json:"endTime"`
	Limit        int32    `json:"limit"`
}

// ReplayEventsResponse 事件回放响应
type ReplayEventsResponse struct {
	Events []*Event `json:"events"`
}

// ListSchemasRequest 列出 Schema 请求
type ListSchemasRequest struct{}

// ListSchemasResponse 列出 Schema 响应
type ListSchemasResponse struct {
	Schemas []*Schema `json:"schemas"`
}

// SubscribeEventsRequest 事件订阅请求
type SubscribeEventsRequest struct {
	EventTypes []string `json:"eventTypes"`
}

// TelemetryFrame 单帧遥测消息，对应遥测上报（T5.2）。
type TelemetryFrame struct {
	SatelliteId string            `json:"satelliteId"`
	Timestamp   string            `json:"timestamp"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	Quality     string            `json:"quality"`
}

// IngestTelemetryResponse 遥测批量上报响应（T5.2）。
type IngestTelemetryResponse struct {
	Accepted int32 `json:"accepted"`
	Total    int32 `json:"total"`
}

// ===== 模型转换函数 =====

// nodeToProto 将 model.Node 转换为 gRPC Node 消息
func nodeToProto(n *model.Node) *Node {
	if n == nil {
		return nil
	}
	return &Node{
		NodeId:           n.NodeID,
		NodeType:         string(n.NodeType),
		Name:             n.Name,
		Status:           n.Status,
		OwnerCommunityId: n.OwnerCommunityID,
		CreatedAt:        n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        n.UpdatedAt.UTC().Format(time.RFC3339),
		ShardKey:         n.ShardKey,
		FederationId:     n.FederationID,
		Properties:       anyMapToStringMap(n.Properties),
	}
}

// nodeFromProto 将 gRPC Node 消息转换为 model.Node
func nodeFromProto(n *Node) *model.Node {
	if n == nil {
		return nil
	}
	return &model.Node{
		NodeID:           n.NodeId,
		NodeType:         model.NodeType(n.NodeType),
		Name:             n.Name,
		Status:           n.Status,
		OwnerCommunityID: n.OwnerCommunityId,
		CreatedAt:        parseTime(n.CreatedAt),
		UpdatedAt:        parseTime(n.UpdatedAt),
		ShardKey:         n.ShardKey,
		FederationID:     n.FederationId,
		Properties:       stringMapToAnyMap(n.Properties),
	}
}

// relToProto 将 model.Relationship 转换为 gRPC Relationship 消息
func relToProto(r *model.Relationship) *Relationship {
	if r == nil {
		return nil
	}
	return &Relationship{
		RelId:      r.RelID,
		FromNodeId: r.FromNodeID,
		ToNodeId:   r.ToNodeID,
		RelType:    string(r.RelType),
		Properties: anyMapToStringMap(r.Properties),
		CreatedAt:  r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// relFromProto 将 gRPC Relationship 消息转换为 model.Relationship
func relFromProto(r *Relationship) *model.Relationship {
	if r == nil {
		return nil
	}
	return &model.Relationship{
		RelID:      r.RelId,
		FromNodeID: r.FromNodeId,
		ToNodeID:   r.ToNodeId,
		RelType:    model.RelationshipType(r.RelType),
		Properties: stringMapToAnyMap(r.Properties),
		CreatedAt:  parseTime(r.CreatedAt),
	}
}

// eventToProto 将 event.Event 转换为 gRPC Event 消息
func eventToProto(e *event.Event) *Event {
	if e == nil {
		return nil
	}
	return &Event{
		EventId:      e.EventID,
		EventType:    string(e.EventType),
		Timestamp:    e.Timestamp.UTC().Format(time.RFC3339),
		SourceNodeId: e.SourceNodeID,
		TraceId:      e.TraceID,
		Payload:      anyMapToStringMap(e.Payload),
	}
}

// schemaToProto 将 event.Schema 转换为 gRPC Schema 消息
func schemaToProto(s event.Schema) *Schema {
	fields := make([]*FieldDef, 0, len(s.Fields))
	for _, f := range s.Fields {
		fields = append(fields, &FieldDef{
			Name:     f.Name,
			Type:     f.Type,
			Required: f.Required,
		})
	}
	return &Schema{
		EventType: string(s.EventType),
		Version:   s.Version,
		Fields:    fields,
	}
}

// anyMapToStringMap 将 map[string]any 转换为 map[string]string
// 值通过 fmt.Sprintf 转为字符串，简化 protobuf 兼容性
func anyMapToStringMap(m map[string]any) map[string]string {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

// stringMapToAnyMap 将 map[string]string 转换为 map[string]any
func stringMapToAnyMap(m map[string]string) map[string]any {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// parseTime 解析 RFC3339 时间字符串，空字符串返回零值
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
