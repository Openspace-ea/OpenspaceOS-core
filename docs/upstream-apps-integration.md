# 上层应用对接 Openspace OS Core 接口文档

> 本仓库中已有一个 OpenAPI 3.0 契约文件：[`internal/api/rest/openapi.yaml`](../internal/api/rest/openapi.yaml)，部署后可访问 `GET /api/v1/docs`（Swagger UI）或 `GET /api/v1/openapi.yaml` 实时查看。本文档面向**新的上层应用接入方**，提供接入路径、认证方式、端点与数据结构的权威说明，配套代码示例均取自本仓库真实实现。

- 服务端默认端口：HTTP **8080**、gRPC **9090**、遥测 TCP **7000**
- 数据格式：默认 `application/json`（`POST/PUT` 需携带），响应统一 JSON

---

## 1. 接入三步走

```
① 让 Core 为你的应用注册一个"机器接入方(Client)"，拿到明文 API Key
        │
② 用 API Key 换取短期 JWT（或直接把 API Key 放 Authorization 头）
        │
③ 携带令牌调用业务端点：图谱 / 事件 / 遥测 / 指令 / 用量
```

### 1.0 部署侧注入的环境变量（上层应用/前端托管，如 Vercel）

上层应用运行时只需配好下面三个变量，即可完成对 Core 的对接（其余统一由核心处理）：

| 变量 | 含义 | 来源 |
| --- | --- | --- |
| `OPENSPACE_CORE_URL` | Core 的 HTTP 入口地址 | Core 部署后的公网地址（`http://<core-host>:8080`） |
| `OPENSPACE_API_KEY` | 机器接入方 Client 的凭证（`aos_...`） | **由管理员在 Core 上为你的应用创建 Client 时返回**，仅此一次，需保存 |
| `OPENSPACE_COMMUNITY_ID` | 业务社区隔离标识 | **自行约定的稳定字符串**（须与创建 Client 的 `communityId`、注册节点的 `ownerCommunityId` 一致） |

**`OPENSPACE_API_KEY` / `OPENSPACE_COMMUNITY_ID` 产生方式见下述 §1.1**：前者对着 Client 创建接口要，后者由你定义并在创建 Client 时传入 `communityId`。

**校验三变量是否就绪**（能换到 JWT 即连接正确）：

```bash
curl -s "$OPENSPACE_CORE_URL/api/v1/auth/token" \
  -H "Content-Type: application/json" -d "{\"apiKey\":\"$OPENSPACE_API_KEY\"}"
# → {"token":"<jwt>","expiresIn":<秒>}   成功即地址/认证/DB 均正常
```

### 1.1 申请机器接入凭证

两种方式等价，二选一：

**方式 A：管理员用 CLI 创建（推荐，`aos client` 子命令）**

```bash
./bin/openspace-os-cli --server http://<core-host>:8080 client create \
  --name "ssa-watcher" \
  --module "SSA-Watcher" \
  --community "comm-saa-1"
# 输出中包含明文 apiKey（格式 aos_...），仅此一次，请妥善保存
```

**方式 B：管理员用 REST 创建（需 admin 权限）**

```bash
curl -s http://<core-host>:8080/api/v1/clients \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"satellite-watcher","moduleName":"Satellite-Watcher","communityId":"comm-a"}'
```

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 应用/接入方名称，唯一 |
| `moduleName` | string | 否 | 所属上层应用模块名 |
| `communityId` | string | 否 | 社区隔离维度（后续图谱/事件的数据隔离粒度） |
| `roles` | string[] | 否 | 授予的角色（如 `admin`） |

响应 `{ "client": {...}, "apiKey": "aos_..." }`。**明文 API Key 仅创建时返回，此后不可再获取**，请立即保存。

---

## 2. 认证

Core 区分两类主体，统一经 `Authorization` 头鉴权：

- **人（User）**：用户名/密码登录换取 JWT，适合 Web/管理端。
- **机器（Client）**：API Key（`aos_` 前缀），适合上层应用间的服务调用。

### 2.1 机器接入方：API Key → JWT 交换（推荐）

```bash
curl -s http://<core-host>:8080/api/v1/auth/token \
  -H "Content-Type: application/json" \
  -d '{"apiKey":"aos_xxxxxxxxxxxxxxxx"}'
# → {"token":"<jwt>","expiresIn":<秒>}
```

此后以 `Authorization: Bearer <jwt>` 调用所有业务端点。JWT 有有效期（`expiresIn`），到期后用 `POST /api/v1/auth/refresh` 刷新。

### 2.2 直接携带 API Key（等价但建议仅用于压测/调试）

`Authorization: Bearer aos_xxxxxxxx` 亦可直接通过鉴权中间件（路由已同时识别 JWT 与 `aos_` API Key）。

### 2.3 人（可选）：登录换取 JWT

```bash
curl -s http://<core-host>:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"..."}'
# → {"token":"<jwt>","expiresIn":<秒>}
```

### 免认证端点（无需 token）

`GET /healthz`、`/metrics`、`/api/v1/health`、`/api/v1/docs`、`/api/v1/openapi.yaml`、`POST /api/v1/auth/login`、`POST /api/v1/auth/token`。其余端点在启用认证时均需携带 token。

---

## 3. 端点速查表

> 路径前缀均为 `/api/v1`。鉴权抛 `401`、无权限抛 `403`。

| 功能 | 方法 & 路径 | 说明 |
| --- | --- | --- |
| 健康 | `GET /healthz` `GET /health` | 含 db/bus 依赖状态 |
| 指标 | `GET /metrics` | Prometheus 格式 |
| 登录 | `POST /auth/login` | user/password → JWT |
| Token 交换 | `POST /auth/token` | apiKey → JWT |
| 刷新 | `POST /auth/refresh` | 刷新 JWT |
| 我的信息 | `GET /auth/me` | 当前主体信息 |
| 客户端管理 | `GET|POST /clients` 、`GET|DELETE /clients/{id}` | 机器接入方 CRUD |
| 用户管理 | `GET|POST /users`、`GET|DELETE /users/{id}` | 自然人管理 |
| 节点 | `POST /nodes`、`GET /nodes`、`GET|PUT|DELETE /nodes/{id}` | 图谱节点 |
| 建立关系 | `POST /nodes/{id}/relationships` | 加边 |
| 图谱遍历 | `GET /nodes/{id}/graph` | BFS 邻接 |
| 删除关系 | `DELETE /relationships/{relId}` | |
| 订阅事件 | `GET /events/subscribe` | **SSE 实时流**（见 §6） |
| 回放事件 | `POST /events/replay` | 按类型/时间回查 |
| 事件 Schema | `GET /schemas` | 注册的事件类型与校验器 |
| 遥测批量上报 | `POST /telemetry/ingest` | **推荐机器接入**（见 §5） |
| 遥测解析器 | `GET /telemetry/parsers`、`POST /telemetry/parser` | 解析器选择 |
| 遥测原始发送 | `POST /telemetry/send` | 单条原始帧（老协议） |
| 指令下发 | `POST /commands`、`GET /commands/{id}`、`DELETE /commands/{id}`、`GET /commands` | 遥控指令 |
| 用量统计 | `GET /billing/usage` | 计量预留（见 §7） |

> gRPC 在 `:9090` 暴露同类能力（含遥测客户端流与事件流，支持 JWT/API Key）。OpenAPI 之外，gRPC 反射（server reflection）可用以获得 `.proto` 服务描述。

---

## 4. 图谱：注册节点与关系

### 4.1 注册节点

```bash
curl -s http://<core-host>:8080/api/v1/nodes \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{
        "nodeId":"sat-1",
        "nodeType":"satellite",
        "name":"Satellite One",
        "status":"online",
        "ownerCommunityId":"comm-a",
        "shardKey":"shard-1",
        "federationId":"fed-2"
      }'
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `nodeId` | 是 | 全局唯一节点 ID |
| `nodeType` | 是 | 节点类型（`satellite`/`ground`/`community`/…上层自定义） |
| `name` | 是 | 名称 |
| `status` | 是 | `online`/`offline`/`degraded` 等 |
| `ownerCommunityId` | 是 | 所属社区 |
| `shardKey` | 否 | 逻辑分片键 |
| `federationId` | 否 | 联邦标识 |

注册成功会广播 `NodeRegistered`、`StateUpdated` 事件。

### 4.2 建立关系（加边）

```bash
curl -s http://<core-host>:8080/api/v1/nodes/sat-1/relationships \
  -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" \
  -d '{"toNodeId":"gs-1","relType":"linked_to"}'
```

### 4.3 图谱遍历

```bash
curl -s "http://<core-host>:8080/api/v1/nodes/comm-a/graph?direction=out&depth=2&relType=" \
  -H "Authorization: Bearer <jwt>"
```

- `direction`: `out`(默认)/`in`/`both`；`depth`: 遍历深度；`relType`: 按关系类型过滤。

---

## 5. 遥测上报（机器接入方主入口）

### 5.1 批量上报 `POST /telemetry/ingest`

推荐上层应用以**批量帧**方式上报，减少往返、触发背压保护。

```bash
curl -s http://<core-host>:8080/api/v1/telemetry/ingest \
  -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" \
  -d '{
        "frames": [
          {"satelliteId":"sat-1","timestamp":"2026-08-28T10:00:00Z","parameters":{"pos":{"x":1,"y":2,"z":3},"temp":35.1},"quality":"good"},
          {"satelliteId":"sat-1","parameters":{"temp":35.2}}
        ]
      }'
# → 202 {"status":"accepted","accepted":2,"total":2}
```

| 帧字段 | 说明 |
| --- | --- |
| `satelliteId` | 卫星/对象 ID |
| `timestamp` | 遥测时间（RFC3339）；缺省则自动补当前时间 |
| `parameters` | 遥测参数键值对（自由结构，Core 透传） |
| `quality` | 质量标记（`good`/`bad` 等） |

- 每帧标准化后发布为 **`TelemetryReceived`** 事件入总线。
- 响应 `202`：`accepted` 为成功入帧数、`total` 为请求帧数；部分失败仍返回 `202`，`accepted < total`。

### 5.2 其它遥测入口

- `POST /telemetry/send`：单条原始数据 `{"data":"raw telemetry"}`（老协议兼容）。
- `GET /telemetry/parsers`、`POST /telemetry/parser {"name":"tle"}`：查看/切换解析器（如 TLE）。
- TCP `:7000`：老协议遥测端口（含限流）。

---

## 6. 事件：实时订阅 + 回放

所有业务动作（节点增删改、关系、遥测、指令、告警）都会产生事件，通过统一事件总线广播。强烈建议上层应用**以事件驱动**方式感知系统变化，而非轮询。

### 6.1 事件结构

```json
{
  "eventId": "…",
  "eventType": "TelemetryReceived",
  "timestamp": "2026-08-28T10:00:00Z",
  "sourceNodeId": "sat-1",
  "traceId": "…",
  "payload": { "satelliteId": "sat-1", "parameters": {...} }
}
```

### 6.2 实时订阅（SSE）

```bash
curl -N http://<core-host>:8080/api/v1/events/subscribe?types=TelemetryReceived,NodeUpdated \
  -H "Authorization: Bearer <jwt>"
# 输出为 text/event-stream：data: {event...}
```

- `types` 逗号分隔，缺省订阅全部类型。客户端断开即停止，需自维持断线重连。

### 6.3 回放（补拉错过的历史事件）

```bash
curl -s http://<core-host>:8080/api/v1/events/replay \
  -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" \
  -d '{"eventTypes":["TelemetryReceived"],"sourceNodeId":"sat-1","startTime":"2026-08-28T09:00:00Z","endTime":"2026-08-28T10:00:00Z","limit":1000}'
# → [事件数组]
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `eventTypes` | string[] | 类型过滤 |
| `sourceNodeId` | string | 来源节点过滤 |
| `startTime`/`endTime` | string | RFC3339 时间范围 |
| `limit` | int | 条数上限 |

### 6.4 事件类型清单（来自 `pkg/event/types.go`）

| 事件 | 触发 |
| --- | --- |
| `NodeRegistered` / `NodeUpdated` / `NodeDeleted` | 节点生命周期 |
| `RelationshipCreated` / `RelationshipDeleted` | 关系变更 |
| `StateUpdated` | 节点状态变更 |
| `TelemetryReceived` | 收到遥测 |
| `CommandSent` / `CommandAcked` | 指令下发/确认 |
| `TaskScheduled` / `TaskStatusChanged` | 任务调度/状态 |
| `HealthAlarm` | 健康告警 |
| `ConjunctionAlert` | 碰撞预警（商业模块产生） |
| `ResourceMatchCompleted` | 资源匹配完成（资源对接平台产生） |

---

## 7. 指令下发

```bash
curl -s http://<core-host>:8080/api/v1/commands \
  -H "Authorization: Bearer <jwt>" -H "Content-Type: application/json" \
  -d '{"satelliteId":"sat-1","commandType":"attitude","parameters":{"mode":"sunpointing"},"priority":5}'
# → 包含 commandId，随后可 GET /api/v1/commands/{commandId} 查询状态
```

| 字段 | 说明 |
| --- | --- |
| `satelliteId` | 目标卫星 |
| `commandType` | 指令类型（`attitude` 等） |
| `parameters` | 指令参数 |
| `priority` | 优先级 |

下发触发 `CommandSent`，执行结果触发 `CommandAcked`。

---

## 8. 用量统计（为计费预留）

```bash
curl -s "http://<core-host>:8080/api/v1/billing/usage?tenantId=comm-a&clientId=c1&start=2026-08-28T00:00:00Z&end=2026-08-28T23:59:59Z" \
  -H "Authorization: Bearer <jwt>"
# → {"aggregates":[{"key":"…","count":N, …}]}
```

参数：`tenantId`/`clientId`/`operation`/`unit`/`start`/`end`（时间 RFC3339）。当前为计量与统计，**不涉及计费扣费**。

---

## 9. 上层应用接入示例流程

### 卫星监测（Satellite Watcher）

1. 批量：每 N 秒将一批遥测帧 `POST /telemetry/ingest`（带 `satelliteId`）。
2. `GET /events/subscribe?types=TelemetryReceived,StateUpdated` 实时感知卫星状态。
3. 需要图谱时 `GET /nodes/{satId}/graph` 看相邻节点（如地面站、资源）。
4. 下发动作 `POST /commands`。

### 空间态势感知（SSA Watcher）

1. 用 `POST /nodes` 登记空间对象的实体节点与 `relationships` 关系。
2. 订阅 `ConjunctionAlert`（碰撞预警事件）以驱动风险面板。
3. 查询图谱 `GET /nodes/comm-saa-1/graph?depth=2` 展示对象关联。
4. 回放 `POST /events/replay` 补历史告警时序。

建议将"订阅 + 回放"搭配使用：上线时先 `replay` 补历史，再切 `subscribe` 收增量。

---

## 10. 对接要点与最佳实践

- **尽量批量**：遥测用 `ingest` 一次多帧，而非逐条 `send`，以吻合背压设计并降延迟。
- **令牌生命周期**：JWT 有有效期；长期运行的机器接入方建议 `token → refresh`，或凭 `aos_` Key 直连请求头以降低成本。
- **事件驱动而非轮询**：感知系统变化优先用 `subscribe`+`replay`，避免高频 `GET`。
- **幂等**：`nodeId`/`relId` 由调用方提供可作为幂等键；写入重复节点以主键冲突处理。
- **错误语义**：`202`=已接受（遥测，即使部分成功）；`400`=参数/JSON 错误；`401`=未认证；`403`=无权限；`503`=依赖未就绪。
- **鉴权中间件**在启用认证（`auth.enabled=true`）时生效；生产部署务必配置强 `secret_key` 并走 HTTPS。
- **契约来源**：一切字段以 `GET /api/v1/openapi.yaml`（或 Swagger UI `GET /api/v1/docs`）为准。