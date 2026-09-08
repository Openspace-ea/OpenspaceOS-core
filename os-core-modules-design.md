# Openspace OS：Core 与上层模块调用关系 · 部署模式 · 接口设计

> 文档版本：v1.0
> 适用范围：`openspace-os-core`（平台基座）与上层 Watcher / 商业模块（如 SSA-Watcher）之间的集成设计
> 配套仓库：`jeffreytanhao-eng/openspace-os-core`、`jeffreytanhao-eng/SSA-Watcher`

---

## 目录

- [1. 概述与设计目标](#1-概述与设计目标)
- [2. 平台角色与分层](#2-平台角色与分层)
- [3. 部署模式](#3-部署模式)
- [4. 模块接入调用关系](#4-模块接入调用关系)
- [5. 接口设计](#5-接口设计)
- [6. 模块接入契约清单](#6-模块接入契约清单)
- [7. 身份与权限对接](#7-身份与权限对接)
- [8. 演进与扩展性](#8-演进与扩展性)
- [9. 附录：URL 速查](#9-附录url-速查)

---

## 1. 概述与设计目标

本项目以 **Openspace OS Core** 作为太空操作平台基座，上层挂接多个 Watcher / 商业模块（首个实装为本项目的 **SSA-Watcher**，即空间态势感知与卫星碰撞预警系统）。

本文档定义三者之间的关系与约束：

1. **调用关系**：上层模块如何调用 Core、如何与 Core 双向同步数据与事件；
2. **部署模式**：以 Docker Compose `include` 分模块、nginx 统一网关的整体部署方案；
3. **接口设计**：Core 已对外暴露的控制面（REST/gRPC）、数据面（Node/关系模型）、事件面（事件 schema），以及模块桥接所需新增的约定接口。

设计原则沿袭《Openspace OS 顶层设计白皮书》：

- **Event-Driven First** — 状态变更一律通过事件发布/订阅；
- **Knowledge Graph 优先** — Node 与 Relationship 作为单一事实源（Single Source of Truth）；
- **Plugin / Module-First** — Core 保持轻量，业务能力通过上层模块扩展；
- **接口契约先行** — 先冻结接口与事件 schema，再实现模块功能。

---

## 2. 平台角色与分层

```
┌────────────────────────── Browser ───────────────────────────┐
│                  统一入口 Gateway (nginx)                       │  80/443
└────────────┬──────────────────────┬───────────────────────────┘
     ┌───────▼───────┐      ┌───────▼───────┐
     │  watcher-1     │      │  watcher-2     │   … 多个上层模块
     │  SSA-Watcher   │      │  (未来)        │
     │ 前端 · 算法 ·  │      │               │
     └───────┬───────┘      └───────┬───────┘
             │  Node 同步 + 事件发布          │
             │  (REST / gRPC / EventBus)    │
     ┌───────▼─────────────────────────────▼───┐
     │        openspace-os-core（平台基座）       │   :8080 / :9090 / :7000
     │  Node/KG · EventBus · Plugin · Auth/RBAC │
     └────────────────┬─────────────────────────┘
                      │
          ┌───────────▼──────────┐   ┌──────────────────────────┐
          │  Core SQLite（元数据  │   │ watcher-N 私有数据库        │
          │  + 事件持久化）        │   │  PostGIS（每模块独立实例）   │
          └──────────────────────┘   └──────────────────────────┘
```

| 层级 | 定位 | 职责 | 典型实例 |
|---|---|---|---|
| **平台基座 Core** | 统一基础设施 | Node 生命周期、知识图谱（KG）、事件总线、插件框架、遥测/遥控流水线、身份与 RBAC | `openspace-os-core`（单一实例） |
| **上层模块** | 业务能力 | 各自面向特定业务：算法、可视化、报表、资源调度等 | SSA-Watcher（首个实装）、未来 watcher-N |
| **基础设施** | 公共支撑 | 数据库（PostGIS × N）、网关、可观测性、配置/密钥 | nginx、PostGIS、Prometheus、Vault |

---

## 3. 部署模式

### 3.1 总体方案（已确认决策）

| 决策项 | 取向 |
|---|---|
| 编排方式 | Docker Compose **`include` 分模块**（平台固定，模块可插拔） |
| 对外访问 | **nginx 统一网关**（80/443），模块端口不公网暴露 |
| 数据隔离 | **每模块独立 PostGIS 实例**，Core 单独 SQLite |
| 身份认证 | **第一时间对接 Core 认证/RBAC**（MVP 用共享签名密钥方案起步） |
| 内部通信 | 共享内网 `platform`，用服务名互访 |

### 3.2 目录结构

```
ops/
├── compose.yaml              # 根：平台层（gateway + include + 网络/卷）
├── .env                      # 统一环境变量
├── gateway/
│   ├── nginx.conf            # 统一入口路由
│   └── Dockerfile
├── core/
│   ├── compose.yaml          # openspace-os-core 子编排
│   └── config.yaml           # auth.enabled = true
└── watchers/
    ├── ssa-watcher/
    │   ├── compose.yaml      # db(独立 PostGIS) + backend + frontend
    │   └── bridge/           # Node 回填 + 事件桥 + Core 认证对接
    └── watcher-2/            # 未来模块，与 ssa-watcher 同构新增
```

### 3.3 根 compose 骨架

```yaml
name: openspace-os-platform

include:
  - core/compose.yaml
  - watchers/ssa-watcher/compose.yaml
  # - watchers/watcher-2/compose.yaml   # 按需启用/注释

services:
  gateway:
    image: nginx:alpine
    ports: ["80:80", "443:443"]
    networks: [platform]
    volumes: [./gateway/nginx.conf:/etc/nginx/conf.d/default.conf:ro]
    depends_on: [openspace-os, ssa-watcher-frontend]

networks:
  platform: { }

volumes:
  core-data: { }
  ssa-pgdata: { }
  # watcher2-pgdata: {}

secrets:
  jwt_secret: { external: true }
```

### 3.4 网关路由示例

```nginx
# 平台基座
location /core/        { proxy_pass http://openspace-os:8080/; }
# 模块 1：SSA-Watcher 前端 + 后端 API
location /ssa/         { proxy_pass http://ssa-watcher-frontend:3000/; }
location /ssa/api/     { proxy_pass http://ssa-watcher-backend:8000/api/; }
# 模块 N：按域名或前缀扩展
# location /watcher2/  { proxy_pass http://watcher2-frontend:3000/; }
```

### 3.5 端口 / 数据 / 网络规划

| 资源 | 规划 | 说明 |
|---|---|---|
| 共享网络 `platform` | 所有服务入内网 | 服务间用 Compose 服务名互访 |
| 对外暴露 | 仅 `gateway` 80/443 | 统一入口，生产可加域名 + HTTPS |
| Core 端口 | 8080 / 9090 / 7000 仅内网 | REST / gRPC / 遥测不直接公网 |
| Core 数据卷 | `core-data` | 存 Node / KG / 事件持久化（SQLite） |
| 模块数据卷 | `ssa-pgdata` 等各自命名 | 每模块独立 PostGIS，互不覆盖 |
| 密钥 | `jwt_secret` secret + 环境变量 | Core 与各模块共享同一签名密钥（方案 A） |

---

## 4. 模块接入调用关系

### 4.1 一次模块会话的完整调用流

```
用户浏览器
   │
   ▼
Gateway (nginx) ──► /ssa/  ──► watcher 前端
                              │
    1. 登录              认证   │  尝试 /ssa/api/auth/login
                              ▼
                    ┌── watcher backend ──┐
                    │  (Python / 或 Go)    │
                    │  验证 token(共享签名) │
                    └────────┬─────────────┘
                             │ 2. (可选)向 Core 请求用户/权限
                             ▼
                    ┌── openspace-os-core ──┐
                    │  POST /api/v1/auth/me │  (校验 RBAC)
                    └───────────────────────┘
```

### 4.2 数据同步启动时序（模块冷启动）

```
gateway 就绪
  └─ openspace-os 健康（healthz + start_period）
       └─ watcher 后端启动
            ├─ [回填] 本地卫星目录/星座 → POST /api/v1/nodes 注册为 Satellite Node
            ├─ [关系] 建立 Satellite‹‑belongsTo‑›Community、controlledBy 等关系
            └─ [订阅] 开启 /api/v1/events/subscribe (SSE) 消费 Core 事件
```

### 4.3 运行时：应用态 → 事件态

```
交会检测产生结果
     │
     ├─ 写入模块私有库（PostGIS：conjunction_events）
     └─ 事件桥发布到 Core
           │
           ▼
     ConjunctionAlert 事件（含 primarySatelliteId / secondaryObjectId / TCA / 风险等级）
           │
           └─ 订阅方：其他模块 / 任务规划 / 将来任务规划模块
```

---

## 5. 接口设计

### 5.1 控制面接口（REST，`/api/v1`，Core 已实现）

| 路径 | 方法 | 权限 | 说明 |
|---|---|---|---|
| `/healthz` `/metrics` | GET | — | 健康检查、Prometheus 指标 |
| `/api/v1/health` | GET | — | 业务健康 |
| `/api/v1/docs` `/openapi.yaml` | GET | — | Swagger UI / OpenAPI 规范 |
| `/api/v1/schemas` | GET | — | 事件 schema 列表 |
| `/api/v1/auth/login` | POST | — | 登录获取 token |
| `/api/v1/auth/refresh` | POST | AUTH | 刷新 token |
| `/api/v1/auth/me` | GET | AUTH | 当前用户信息 |
| `/api/v1/users` | GET/POST | user.manage | 用户管理 |
| `/api/v1/nodes` | POST/GET | node.create / node.read | 创建 / 列举 Node |
| `/api/v1/nodes/{nodeId}` | GET/PUT/DELETE | node.read / update / delete | 单 Node 生命周期 |
| `/api/v1/nodes/{nodeId}/relationships` | POST | relationship.create | 建立关系 |
| `/api/v1/relationships/{relId}` | DELETE | relationship.delete | 删除关系 |
| `/api/v1/nodes/{nodeId}/graph` | GET | graph.query | 关联图遍历（BFS） |
| `/api/v1/events/subscribe` | GET(SSE) | event.subscribe | 实时订阅事件流 |
| `/api/v1/events/replay` | POST | event.replay | 事件回放（调试/审计） |
| `/api/v1/plugins` | GET | plugin.manage | 插件列表 |
| `/api/v1/telemetry/parsers` `/telemetry/parser` `/telemetry/send` | GET/POST | plugin.manage | 遥测流水线管理 |
| `/api/v1/commands` `/commands/{commandId}` | POST/GET/DELETE | command.send | 指令流水线 |

> gRPC（`:9090`，定义见 `proto/openspace_os_core.proto`）镜像以上 REST 能力，供高频/强类型场景使用。

### 5.2 数据面接口（Node 与 Relationship 模型）

**Node 通用结构**（对齐《Openspace OS 接口契约文档 v1.0》）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `nodeId` | string | ✅ | 全局唯一标识 |
| `nodeType` | string | ✅ | Satellite / GroundStation / Task / Community / Mission / Antenna / Payload / Telemetry / Command / Alarm |
| `name` | string | ✅ | 名称 |
| `status` | string | ✅ | 当前状态 |
| `ownerCommunityId` | string | ✅ | 所属 Community |
| `createdAt` / `updatedAt` | timestamp | ✅ | 时间戳 |
| `shardKey` | string | 否 | 分片键（预留扩展性） |
| `federationId` | string | 否 | 联邦标识（预留） |
| `properties` | map | 否 | 类型特有属性 |

**Satellite 属性示例**：

```json
{
  "nodeType": "Satellite",
  "name": "ISS (ZARYA)",
  "status": "active",
  "ownerCommunityId": "comm-iss",
  "properties": {
    "noradId": "25544",
    "owner": "NASA-ISS",
    "capabilities": ["imaging"],
    "orbit": {
      "inclination": 51.6416,
      "raan": 247.4627,
      "eccentricity": 0.0006703,
      "argPerigee": 130.5360,
      "meanAnomaly": 325.0288,
      "meanMotion": 15.7213,
      "perigeeAltitude": 418.2,
      "apogeeAltitude": 432.1,
      "epoch": "2026-08-01T00:00:00Z"
    }
  }
}
```

**关系类型**：`belongsTo`、`controlledBy`、`providesServiceTo`、`ownedBy`、`memberOf` 等（关系有类型 + 方向）。

### 5.3 事件面接口（事件 schema）

**事件通用结构**：

```json
{
  "eventId": "uuid",
  "eventType": "string",
  "timestamp": "timestamp",
  "sourceNodeId": "string",
  "traceId": "string",
  "payload": {}
}
```

**核心事件清单**：

| 事件 | 触发场景 | 本平台内消费者 |
|---|---|---|
| `NodeRegistered` | 新 Node 注册 | 模块目录同步 |
| `NodeUpdated` / `StateUpdated` | Node 变更 / 状态变化 | 各订阅模块 |
| `NodeDeleted` | Node 删除 | 各订阅模块 |
| `RelationshipCreated` / `RelationshipDeleted` | 关系变更 | KG 查询方 |
| `TelemetryReceived` | 收到遥测/TLE | 健康、预警模块 |
| `ConjunctionAlert` | 检测到碰撞预警 | 任务规划、资源对接模块 |

**`ConjunctionAlert` 示例**（SSA-Watcher → Core 发布的桥接事件）：

```json
{
  "eventType": "ConjunctionAlert",
  "sourceNodeId": "sat-25544",
  "payload": {
    "primarySatelliteId": "25544",
    "secondaryObjectId": "48274",
    "probability": 0.12,
    "timeOfClosestApproach": "2026-08-27T09:15:00Z",
    "minDistanceKm": 3.2,
    "relativeVelocityKms": 8.4,
    "riskLevel": "warning",
    "tca": { "lat": 12.3, "lon": 45.6, "altKm": 420.0 }
  }
}
```

### 5.4 模块桥接约定接口（SSA-Watcher 需新增/对齐）

| 约定 | 方向 | 说明 |
|---|---|---|
| 节点回填 | Watcher → Core | 冷启动将本地卫星注册为 `Satellite` Node |
| 事件发布 | Watcher → Core | 交会结果发布为 `ConjunctionAlert` |
| 事件订阅 | Core → Watcher | 消费 `NodeRegistered` / `StateUpdated` 同步本地 |
| 身份校验 | Watcher ↔ Core | 共享 JWT 签名验证；权限查询走 `/api/v1/auth/me` |

---

## 6. 模块接入契约清单

| 集成点 | 现状（SSA-Watcher 侧） | 目标动作 | 变更成本 |
|---|---|---|---|
| 卫星目录 | `satellites` 表 + TLE | 冷启动回填为 `Satellite` Node | S |
| 交会事件 | `conjunction_events` 表 | 事件桥发布 `ConjunctionAlert` | S |
| 身份 | 自带 JWT 认证 | 对接 Core 认证（共享签名起步） | M |
| 数据存储 | 自身 PostGIS | 独立实例，命名卷 | S |
| 对外暴露 | 直接映射端口 | 收敛至 nginx 网关 | S |
| 前端 | Next.js + Cesium | 通过网关 `/ssa/` 访问 | S |

---

## 7. 身份与权限对接

### 7.1 演进路径

| 阶段 | 方案 | 说明 | 成本 |
|---|---|---|---|
| **P1（MVP）** | 共享签名密钥（方案 A） | Core 与模块用同一 `JWT_SECRET`，模块中间件校验 Core 签发 token | 小 |
| P2（可选） | 网关统一认证 | nginx 层统一校验 / JWKS，后端忽略 | 中 |
| P3（生产） | OpenID Connect / 授权码 | Core 作为 IdP，模块走 OAuth2 登录 + 回调 | 大 |

### 7.2 P1 关键点

- Core 开启 `auth.enabled = true`，`secret_key` 使用统一 secret；
- SSA-Watcher 登录接口改为调用 Core `/api/v1/auth/login` 交换 token；
- SSA-Watcher 后端 `deps.py` 中 JWT 校验逻辑使用与 Core 相同的 `JWT_SECRET` / 算法（`HS256`）；
- 模块内二次/细粒度权限（如 watchlist 归属）仍在模块内实现。

---

## 8. 演进与扩展性

- **模块插拔**：新增 watcher 只需在 `ops/watchers/` 新增同构子目录，并在根 `include` 中启用一行；
- **事件流升级**：Core 当前为进程内/SQLite 总线，规模增长后升级为分层 Event Mesh（Core Roadmap Phase 2/3）；
- **数据分片**：Node 模型已预留 `shardKey` / `federationId`，支撑后续水平拆分与联邦查询；
- **可观测性**：平台层预留 Prometheus + OpenTelemetry Collector，规模起来后启用。

---

## 9. 附录：URL 速查

| 项 | 地址 |
|---|---|
| Core REST | `http://openspace-os:8080` / `/api/v1/...` |
| Core Swagger | `/api/v1/docs` |
| Core gRPC | `openspace-os:9090` |
| Core 遥测 | `openspace-os:7000` |
| SSA-Watcher 前端 | `http://gateway/ssa/` |
| SSA-Watcher API | `http://gateway/ssa/api/` |

---

*文档基于当前 `openspace-os-core`（REST Router、KGService、TLE Parser、事件模型）与 `SSA-Watcher` 的既有代码与接口梳理。gRPC 具体方法以 `proto/openspace_os_core.proto` 为准。*