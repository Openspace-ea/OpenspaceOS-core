// Package rest 实现 Openspace OS Core 的 REST API 层，基于 chi 路由器对外暴露 HTTP 接口。
package rest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/command"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/internal/usage"
	"github.com/openspace-os/openspace-os-core/pkg/event"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// Handler 是 REST API 的请求处理器，持有 KGService、MessageBus 等依赖。
type Handler struct {
	kg       *core.KGService
	bus      core.MessageBus
	registry *event.SchemaRegistry
	plugins  *plugin.Manager
	log      *slog.Logger
	db       *sql.DB

	// 遥测流水线。为 nil 时表示未启用遥测相关端点。
	pipeline *telemetry.Pipeline

	// 指令流水线。为 nil 时表示未启用指令相关端点。
	cmdPipeline *command.Pipeline

	// 认证相关依赖。为 nil 时表示未启用认证。
	authSvc     *auth.Service
	clientSvc   *auth.ClientService
	tokenMgr    *auth.TokenManager
	authEnabled bool

	// 用量统计存储。为 nil 时表示未启用用量查询端点。
	usageStore usage.Store
	// 用量计量采集器与指标（用于遥测上报的帧数/字节数计量，T5.6）。
	usageCollector *usage.Collector
	usageMetrics   *usage.Metrics

	// 版本信息与启动时间，用于健康检查与 metrics 端点。
	version   string
	startTime time.Time
}

// NewHandler 创建 REST API Handler。
func NewHandler(kg *core.KGService, bus core.MessageBus, registry *event.SchemaRegistry, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	if registry == nil {
		registry = event.NewSchemaRegistry()
	}
	return &Handler{
		kg:        kg,
		bus:       bus,
		registry:  registry,
		log:       log,
		version:   "dev",
		startTime: time.Now(),
	}
}

// SetVersion 设置服务版本信息，用于健康检查端点。
func (h *Handler) SetVersion(v string) {
	if v != "" {
		h.version = v
	}
}

// SetDB 注入数据库连接，用于健康检查中的连通性探测（T6.3）。
func (h *Handler) SetDB(db *sql.DB) {
	h.db = db
}

// ErrorResponse 是统一的错误响应格式。
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// createNodeRequest 是创建 Node 的请求体（不含时间戳，由服务层设置）。
type createNodeRequest struct {
	NodeID           string         `json:"nodeId"`
	NodeType         string         `json:"nodeType"`
	Name             string         `json:"name"`
	Status           string         `json:"status"`
	OwnerCommunityID string         `json:"ownerCommunityId"`
	ShardKey         string         `json:"shardKey,omitempty"`
	FederationID     string         `json:"federationId,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
}

// createRelationshipRequest 是创建关系的请求体。
type createRelationshipRequest struct {
	ToNodeID   string         `json:"toNodeId"`
	RelType    string         `json:"relType"`
	Properties map[string]any `json:"properties,omitempty"`
}

// replayRequest 是事件回放的请求体。
type replayRequest struct {
	EventTypes   []string `json:"eventTypes"`
	SourceNodeID string   `json:"sourceNodeId"`
	StartTime    string   `json:"startTime"`
	EndTime      string   `json:"endTime"`
	Limit        int      `json:"limit"`
}

// writeJSON 写入 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// 编码失败时只能记录日志，响应头已写入
		slog.Default().Error("JSON 编码失败", "error", err)
	}
}

// writeError 写入错误响应。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{
		Error:   http.StatusText(status),
		Message: msg,
	})
}

// handleServiceError 根据服务层错误返回对应的 HTTP 状态码。
// 不存在返回 404，其余返回 500。
func handleServiceError(w http.ResponseWriter, err error, operation string) {
	if errors.Is(err, core.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	slog.Default().Error(operation+"失败", "error", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

// parseUsageQuery 解析用量查询的查询参数。
//
// start/end 支持 RFC3339 格式；未提供时默认最近 24 小时。
// unit 参数若提供则校验合法性。
func parseUsageQuery(r *http.Request) (usage.Query, error) {
	q := usage.Query{}
	now := time.Now()

	// 时间范围：默认最近 24 小时
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if start == "" {
		q.Start = now.Add(-24 * time.Hour)
	} else {
		t, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return q, fmt.Errorf("start 时间格式无效（需 RFC3339）: %w", err)
		}
		q.Start = t
	}
	if end == "" {
		q.End = now
	} else {
		t, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return q, fmt.Errorf("end 时间格式无效（需 RFC3339）: %w", err)
		}
		q.End = t
	}
	if !q.End.After(q.Start) {
		return q, fmt.Errorf("end 必须晚于 start")
	}

	q.TenantID = r.URL.Query().Get("tenantId")
	q.ClientID = r.URL.Query().Get("clientId")
	q.Operation = r.URL.Query().Get("operation")

	if unitStr := r.URL.Query().Get("unit"); unitStr != "" {
		u := usage.Unit(unitStr)
		if !usage.ValidUnit(u) {
			return q, fmt.Errorf("unit 非法: %s", unitStr)
		}
		q.Unit = u
		q.HasUnit = true
	}
	return q, nil
}

// Health 健康检查端点。
// GET /api/v1/health 及 GET /healthz
//
// 返回服务状态、版本信息、运行时间，以及后端依赖（数据库、事件总线）的连通性。
// 任一已配置的依赖不可达时返回 503 并将 status 置为 "degraded"（T6.3）。
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	deps := map[string]any{}

	// 数据库连通性（已通过 SetDB 注入时检查）
	if h.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.db.PingContext(ctx); err != nil {
			deps["database"] = map[string]any{"status": "down", "error": err.Error()}
			status = "degraded"
			code = http.StatusServiceUnavailable
		} else {
			deps["database"] = map[string]any{"status": "up"}
		}
	}

	// 事件总线连通性（进程内总线恒 up，NATS 检查连接状态）
	if h.bus != nil {
		if err := h.bus.Ping(r.Context()); err != nil {
			deps["bus"] = map[string]any{"status": "down", "error": err.Error()}
			status = "degraded"
			code = http.StatusServiceUnavailable
		} else {
			deps["bus"] = map[string]any{"status": "up"}
		}
	}

	writeJSON(w, code, map[string]any{
		"status":   status,
		"version":  h.version,
		"uptime":   time.Since(h.startTime).String(),
		"uptimeMs": time.Since(h.startTime).Milliseconds(),
		"deps":     deps,
	})
}

// Metrics 以 Prometheus 文本格式暴露核心运行指标。
// GET /metrics
//
// 当 OTel 未启用时，仍返回基础 metrics（来自遥测流水线与指令流水线的原子计数器）。
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder

	// 遥测流水线指标
	if h.pipeline != nil {
		m := h.pipeline.Metrics().Snapshot()
		writePromMetric(&sb, "aos_telemetry_ingress_total", "counter", "遥测入口消息总数", float64(m.IngressQPS))
		writePromMetric(&sb, "aos_telemetry_parse_latency_us", "gauge", "最近一次遥测解析耗时（微秒）", float64(m.ParseLatency))
		writePromMetric(&sb, "aos_telemetry_drop_total", "counter", "遥测丢弃消息总数", float64(m.DropCount))
	}

	// 指令流水线指标
	if h.cmdPipeline != nil {
		m := h.cmdPipeline.Metrics().Snapshot()
		writePromMetric(&sb, "aos_command_sent_total", "counter", "已发送指令总数", float64(m.TotalSent))
		writePromMetric(&sb, "aos_command_acked_total", "counter", "已确认指令总数", float64(m.TotalAcked))
		writePromMetric(&sb, "aos_command_failed_total", "counter", "发送失败指令总数", float64(m.TotalFailed))
		writePromMetric(&sb, "aos_command_timeout_total", "counter", "发送超时指令总数", float64(m.TotalTimeout))
		writePromMetric(&sb, "aos_command_send_latency_us", "gauge", "最近一次指令发送耗时（微秒）", float64(m.SendLatency))
		writePromMetric(&sb, "aos_command_success_rate", "gauge", "指令成功率（百分比*100）", float64(m.SuccessRate))
	}

	// 运行时间指标
	uptimeSec := time.Since(h.startTime).Seconds()
	writePromMetric(&sb, "aos_uptime_seconds", "gauge", "服务运行时间（秒）", uptimeSec)

	_, _ = w.Write([]byte(sb.String()))
}

// writePromMetric 向 StringBuilder 写入一条 Prometheus 格式的指标。
func writePromMetric(sb *strings.Builder, name, metricType, help string, value float64) {
	sb.WriteString("# HELP ")
	sb.WriteString(name)
	sb.WriteString(" ")
	sb.WriteString(help)
	sb.WriteString("\n")

	sb.WriteString("# TYPE ")
	sb.WriteString(name)
	sb.WriteString(" ")
	sb.WriteString(metricType)
	sb.WriteString("\n")

	sb.WriteString(name)
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	sb.WriteString("\n")
}

// CreateNode 创建 Node。
// POST /api/v1/nodes
func (h *Handler) CreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}

	// 基本校验：必填字段非空
	if req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}
	if req.NodeType == "" {
		writeError(w, http.StatusBadRequest, "nodeType 不能为空")
		return
	}
	if !model.NodeType(req.NodeType).Valid() {
		writeError(w, http.StatusBadRequest, "未知的 nodeType: "+req.NodeType)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status 不能为空")
		return
	}
	if req.OwnerCommunityID == "" {
		writeError(w, http.StatusBadRequest, "ownerCommunityId 不能为空")
		return
	}

	node := &model.Node{
		NodeID:           req.NodeID,
		NodeType:         model.NodeType(req.NodeType),
		Name:             req.Name,
		Status:           req.Status,
		OwnerCommunityID: req.OwnerCommunityID,
		ShardKey:         req.ShardKey,
		FederationID:     req.FederationID,
		Properties:       req.Properties,
	}

	if err := h.kg.RegisterNode(r.Context(), node); err != nil {
		handleServiceError(w, err, "创建节点")
		return
	}

	writeJSON(w, http.StatusCreated, node)
}

// GetNode 查询单个 Node。
// GET /api/v1/nodes/{nodeId}
func (h *Handler) GetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}

	node, err := h.kg.GetNode(r.Context(), nodeID)
	if err != nil {
		handleServiceError(w, err, "查询节点")
		return
	}

	writeJSON(w, http.StatusOK, node)
}

// UpdateNode 更新 Node。
// PUT /api/v1/nodes/{nodeId}
func (h *Handler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}

	var changes map[string]any
	if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if len(changes) == 0 {
		writeError(w, http.StatusBadRequest, "changes 不能为空")
		return
	}

	node, err := h.kg.UpdateNode(r.Context(), nodeID, changes)
	if err != nil {
		handleServiceError(w, err, "更新节点")
		return
	}

	writeJSON(w, http.StatusOK, node)
}

// DeleteNode 删除 Node。
// DELETE /api/v1/nodes/{nodeId}
func (h *Handler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}

	// 先检查节点是否存在，不存在则返回 404
	if _, err := h.kg.GetNode(r.Context(), nodeID); err != nil {
		handleServiceError(w, err, "查询节点")
		return
	}

	if err := h.kg.DeleteNode(r.Context(), nodeID); err != nil {
		handleServiceError(w, err, "删除节点")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListNodes 列表查询 Node。
// GET /api/v1/nodes?nodeType=&communityId=&status=&limit=&offset=
func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	opts := core.ListOptions{
		NodeType:         model.NodeType(q.Get("nodeType")),
		OwnerCommunityID: q.Get("communityId"),
		Status:           q.Get("status"),
	}

	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			opts.Offset = n
		}
	}

	nodes, err := h.kg.ListNodes(r.Context(), opts)
	if err != nil {
		handleServiceError(w, err, "列表查询节点")
		return
	}

	writeJSON(w, http.StatusOK, nodes)
}

// CreateRelationship 建立关系。
// POST /api/v1/nodes/{nodeId}/relationships
func (h *Handler) CreateRelationship(w http.ResponseWriter, r *http.Request) {
	fromNodeID := chi.URLParam(r, "nodeId")
	if fromNodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}

	var req createRelationshipRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}

	if req.ToNodeID == "" {
		writeError(w, http.StatusBadRequest, "toNodeId 不能为空")
		return
	}
	if req.RelType == "" {
		writeError(w, http.StatusBadRequest, "relType 不能为空")
		return
	}
	if !model.RelationshipType(req.RelType).Valid() {
		writeError(w, http.StatusBadRequest, "未知的 relType: "+req.RelType)
		return
	}

	rel := &model.Relationship{
		RelID:      uuid.NewString(),
		FromNodeID: fromNodeID,
		ToNodeID:   req.ToNodeID,
		RelType:    model.RelationshipType(req.RelType),
		Properties: req.Properties,
	}

	if err := h.kg.CreateRelationship(r.Context(), rel); err != nil {
		handleServiceError(w, err, "创建关系")
		return
	}

	writeJSON(w, http.StatusCreated, rel)
}

// DeleteRelationship 删除关系。
// DELETE /api/v1/relationships/{relId}
func (h *Handler) DeleteRelationship(w http.ResponseWriter, r *http.Request) {
	relID := chi.URLParam(r, "relId")
	if relID == "" {
		writeError(w, http.StatusBadRequest, "relId 不能为空")
		return
	}

	if err := h.kg.DeleteRelationship(r.Context(), relID); err != nil {
		handleServiceError(w, err, "删除关系")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TraverseGraph 关系图查询。
// GET /api/v1/nodes/{nodeId}/graph?direction=&relType=&depth=
func (h *Handler) TraverseGraph(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}

	q := r.URL.Query()
	direction := q.Get("direction")
	if direction == "" {
		direction = "out"
	}

	opts := core.TraverseOptions{
		RelType:   model.RelationshipType(q.Get("relType")),
		Direction: direction,
	}
	if s := q.Get("depth"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.MaxDepth = n
		}
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 1
	}

	rels, err := h.kg.TraverseGraph(r.Context(), nodeID, opts)
	if err != nil {
		handleServiceError(w, err, "图遍历查询")
		return
	}

	writeJSON(w, http.StatusOK, rels)
}

// SubscribeEvents SSE 事件订阅。
// GET /api/v1/events/subscribe?types=TypeA,TypeB
func (h *Handler) SubscribeEvents(w http.ResponseWriter, r *http.Request) {
	// 解析事件类型过滤
	var eventTypes []event.EventType
	if typesParam := r.URL.Query().Get("types"); typesParam != "" {
		for _, t := range strings.Split(typesParam, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				eventTypes = append(eventTypes, event.EventType(t))
			}
		}
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "服务器不支持 SSE")
		return
	}

	// 订阅事件总线
	ch, unsubscribe := h.bus.Subscribe(core.SubscribeOptions{
		EventTypes: eventTypes,
	})
	defer unsubscribe()

	// 写入初始注释行，建立连接
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// 客户端断开连接
			return
		case e, ok := <-ch:
			if !ok {
				// channel 已关闭
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				h.log.Error("序列化事件失败", "error", err, "eventId", e.EventID)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ReplayEvents 事件回放。
// POST /api/v1/events/replay
func (h *Handler) ReplayEvents(w http.ResponseWriter, r *http.Request) {
	var req replayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}

	opts := core.ReplayOptions{
		SourceNodeID: req.SourceNodeID,
		Limit:        req.Limit,
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
			writeError(w, http.StatusBadRequest, "startTime 格式无效，需 RFC3339: "+err.Error())
			return
		}
		opts.StartTime = &t
	}
	if req.EndTime != "" {
		t, err := time.Parse(time.RFC3339, req.EndTime)
		if err != nil {
			writeError(w, http.StatusBadRequest, "endTime 格式无效，需 RFC3339: "+err.Error())
			return
		}
		opts.EndTime = &t
	}

	events, err := h.bus.Replay(r.Context(), opts)
	if err != nil {
		handleServiceError(w, err, "事件回放")
		return
	}

	writeJSON(w, http.StatusOK, events)
}

// ListSchemas 列出所有事件 Schema。
// GET /api/v1/schemas
func (h *Handler) ListSchemas(w http.ResponseWriter, r *http.Request) {
	schemas := h.registry.ListSchemas()
	writeJSON(w, http.StatusOK, schemas)
}

// SetPluginManager 注入插件管理器实例。
//
// 在 main.go 中创建 PluginManager 后调用此方法注入，
// 以启用插件管理相关的 REST 端点。
func (h *Handler) SetPluginManager(pm *plugin.Manager) {
	h.plugins = pm
}

// ListPlugins 列出所有已注册的插件。
// GET /api/v1/plugins
func (h *Handler) ListPlugins(w http.ResponseWriter, r *http.Request) {
	if h.plugins == nil {
		writeJSON(w, http.StatusOK, []*plugin.PluginInstance{})
		return
	}
	writeJSON(w, http.StatusOK, h.plugins.ListPlugins())
}

// UnloadPlugin 卸载指定名称的插件。
// POST /api/v1/plugins/{name}/unload
func (h *Handler) UnloadPlugin(w http.ResponseWriter, r *http.Request) {
	if h.plugins == nil {
		writeError(w, http.StatusServiceUnavailable, "插件管理未启用")
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	if err := h.plugins.Unload(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetAuth 注入认证服务依赖，并设置是否启用认证。
//
// 在 main.go 中创建 AuthService 后调用此方法注入，
// 以启用认证相关的 REST 端点与路由保护。
func (h *Handler) SetAuth(svc *auth.Service, tm *auth.TokenManager, enabled bool) {
	h.authSvc = svc
	h.tokenMgr = tm
	h.authEnabled = enabled
}

// SetClientService 注入机器接入方（Client）服务。
//
// 注入后启用客户端管理 API，并让路由鉴权同时支持 API Key。
func (h *Handler) SetClientService(cs *auth.ClientService) {
	h.clientSvc = cs
}

// SetUsageStore 注入用量统计存储，启用用量查询端点。
func (h *Handler) SetUsageStore(s usage.Store) {
	h.usageStore = s
}

// SetUsageMeter 注入用量计量采集器与指标（用于遥测上报的帧数/字节数计量，T5.6）。
//
// 在 main.go 中创建 usage Collector 与 Metrics 后调用，以在遥测 ingest 时记录流量。
func (h *Handler) SetUsageMeter(c *usage.Collector, m *usage.Metrics) {
	h.usageCollector = c
	h.usageMetrics = m
}

// ListUsage 用量聚合查询端点。
// GET /api/v1/billing/usage?tenantId=&clientId=&operation=&unit=&start=&end=
//
// 返回按租户/客户端/端点/操作聚合的用量记录。受限时间段为必填条件；
// 未提供 start/end 时默认查询最近 24 小时。
func (h *Handler) ListUsage(w http.ResponseWriter, r *http.Request) {
	if h.usageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "用量统计未启用")
		return
	}
	q, err := parseUsageQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agg, err := h.usageStore.AggregateByTenant(r.Context(), q)
	if err != nil {
		h.log.Error("查询用量聚合失败", "error", err)
		writeError(w, http.StatusInternalServerError, "查询用量失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"aggregates": agg,
		"count":      len(agg),
	})
}

// SetTelemetryPipeline 注入遥测流水线实例。
//
// 在 main.go 中创建 Pipeline 后调用此方法注入，
// 以启用遥测解析器管理与手动发送等 REST 端点。
func (h *Handler) SetTelemetryPipeline(p *telemetry.Pipeline) {
	h.pipeline = p
}

// setParserRequest 是设置遥测解析器的请求体。
type setParserRequest struct {
	Name string `json:"name"`
}

// sendTelemetryRequest 是手动发送遥测数据的请求体。
type sendTelemetryRequest struct {
	Data string `json:"data"`
}

// ingestTelemetryRequest 是遥测批量上报的请求体。
//
// 支持单帧与批量数组两种结构：{"frames":[{...},{...}]}。
type ingestTelemetryRequest struct {
	Frames []plugin.TelemetryFrame `json:"frames"`
}

// IngestTelemetry 遥测上报端点（带鉴权）。
// POST /api/v1/telemetry/ingest  body: {"frames":[{"satelliteId":"sat-1",...}]}
//
// 批量接收遥测帧，逐帧标准化并发布为 TelemetryReceived 事件入总线（T5.1/5.4）。
// 注册到认证路由（任何已认证人/机主体均可上报）。
func (h *Handler) IngestTelemetry(w http.ResponseWriter, r *http.Request) {
	if h.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "遥测流水线未启用")
		return
	}
	var req ingestTelemetryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if len(req.Frames) == 0 {
		writeError(w, http.StatusBadRequest, "frames 不能为空")
		return
	}
	// 缺失时间戳时使用当前时间，保证 Schema 校验通过
	now := time.Now()
	for i := range req.Frames {
		if req.Frames[i].Timestamp.IsZero() {
			req.Frames[i].Timestamp = now
		}
	}

	accepted, err := h.pipeline.IngestFrames(r.Context(), req.Frames)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 用量计量（T5.6）：上报帧数与字节数
	endpoint := r.URL.Path
	usage.RecordIngest(r.Context(), h.usageCollector, h.usageMetrics, endpoint, "create", int64(len(req.Frames)), int64(r.ContentLength))

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":   "accepted",
		"accepted": accepted,
		"total":    len(req.Frames),
	})
}

// ListTelemetryParsers 列出可用的遥测解析器。
// GET /api/v1/telemetry/parsers
func (h *Handler) ListTelemetryParsers(w http.ResponseWriter, r *http.Request) {
	if h.plugins == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"parsers":   []string{},
			"active":    "",
			"available": false,
		})
		return
	}
	active := ""
	if h.pipeline != nil {
		active = h.pipeline.GetParserName()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"parsers":   h.plugins.ListParsers(),
		"active":    active,
		"available": true,
	})
}

// SetTelemetryParser 设置当前遥测解析器。
// POST /api/v1/telemetry/parser  body: {"name":"json"}
func (h *Handler) SetTelemetryParser(w http.ResponseWriter, r *http.Request) {
	if h.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "遥测流水线未启用")
		return
	}
	var req setParserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	if err := h.pipeline.SetParser(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"active": req.Name})
}

// SendTelemetry 手动发送遥测数据（用于测试）。
// POST /api/v1/telemetry/send  body: {"data":"raw telemetry data"}
func (h *Handler) SendTelemetry(w http.ResponseWriter, r *http.Request) {
	if h.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "遥测流水线未启用")
		return
	}
	var req sendTelemetryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if req.Data == "" {
		writeError(w, http.StatusBadRequest, "data 不能为空")
		return
	}
	if err := h.pipeline.Process(r.Context(), []byte(req.Data)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	metrics := h.pipeline.Metrics().Snapshot()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":  "accepted",
		"parser":  h.pipeline.GetParserName(),
		"metrics": metrics,
	})
}

// SetCommandPipeline 注入指令流水线实例。
//
// 在 main.go 中创建 Command Pipeline 后调用此方法注入，
// 以启用指令发送、状态查询、取消等 REST 端点。
func (h *Handler) SetCommandPipeline(p *command.Pipeline) {
	h.cmdPipeline = p
}

// sendCommandRequest 是发送指令的请求体。
type sendCommandRequest struct {
	SatelliteID string         `json:"satelliteId"`
	CommandType string         `json:"commandType"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Priority    int            `json:"priority,omitempty"`
}

// SendCommand 发送遥控指令。
// POST /api/v1/commands  body: {"satelliteId":"sat-1","commandType":"attitude","parameters":{},"priority":5}
func (h *Handler) SendCommand(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	var req sendCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if req.SatelliteID == "" {
		writeError(w, http.StatusBadRequest, "satelliteId 不能为空")
		return
	}
	if req.CommandType == "" {
		writeError(w, http.StatusBadRequest, "commandType 不能为空")
		return
	}

	cmd := plugin.Command{
		CommandID:   uuid.NewString(),
		SatelliteID: req.SatelliteID,
		CommandType: req.CommandType,
		Parameters:  req.Parameters,
	}
	if err := h.cmdPipeline.Send(r.Context(), cmd, req.Priority); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"commandId": cmd.CommandID,
		"status":    "queued",
	})
}

// GetCommandStatus 查询指令状态。
// GET /api/v1/commands/{commandId}
func (h *Handler) GetCommandStatus(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	commandID := chi.URLParam(r, "commandId")
	if commandID == "" {
		writeError(w, http.StatusBadRequest, "commandId 不能为空")
		return
	}

	status, err := h.cmdPipeline.GetStatus(commandID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// CancelCommand 取消指令。
// DELETE /api/v1/commands/{commandId}
func (h *Handler) CancelCommand(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	commandID := chi.URLParam(r, "commandId")
	if commandID == "" {
		writeError(w, http.StatusBadRequest, "commandId 不能为空")
		return
	}

	if err := h.cmdPipeline.Cancel(commandID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"commandId": commandID,
		"status":    "cancelled",
	})
}

// ListCommands 列出所有指令（支持 status 过滤）。
// GET /api/v1/commands?status=queued
func (h *Handler) ListCommands(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	status := r.URL.Query().Get("status")
	commands := h.cmdPipeline.ListCommands(status)
	writeJSON(w, http.StatusOK, commands)
}

// ListCommandAdapters 列出可用的指令适配器。
// GET /api/v1/command-adapters
func (h *Handler) ListCommandAdapters(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	active := h.cmdPipeline.GetAdapterName()
	adapters := h.cmdPipeline.ListAdapters()
	writeJSON(w, http.StatusOK, map[string]any{
		"adapters": adapters,
		"active":   active,
	})
}

// SetCommandAdapter 设置当前指令适配器。
// POST /api/v1/command-adapter  body: {"name":"mock"}
func (h *Handler) SetCommandAdapter(w http.ResponseWriter, r *http.Request) {
	if h.cmdPipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "指令流水线未启用")
		return
	}
	var req setParserRequest // 复用 setParserRequest 结构体（同为 {"name":"xxx"} 格式）
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体 JSON 解析失败: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	if err := h.cmdPipeline.SetAdapter(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"active": req.Name})
}
