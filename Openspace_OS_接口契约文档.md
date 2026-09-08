# 《Openspace OS 接口契约文档》 v1.0

**文档名称**：Openspace OS 接口契约文档  
**版本**：v1.0  
**编制日期**：2026年7月  
**适用范围**：Openspace OS Core + Commercial Modules + 第三方插件

---

## 1. 文档目的与范围

本文档定义 Openspace OS 各层之间的核心接口契约，包括：

- 核心对象模型（Node）
- 核心事件 Schema
- Plugin Framework 扩展点
- Commercial Modules 与 Core 的集成规则

本契约是 Open Core 与上层模块（包括 Commercial Modules 和第三方插件）之间的**唯一交互规范**。任何模块必须通过本契约定义的方式与 Core 交互。

---

## 2. 核心对象模型（Node）

### 2.1 Node 基础定义

所有核心实体统一建模为 **Node**，具备以下通用属性：

| 属性 | 类型 | 说明 | 是否必填 |
|------|------|------|----------|
| `nodeId` | string | 全局唯一标识 | 是 |
| `nodeType` | string | Node 类型（Satellite / GroundStation / Task / Community 等） | 是 |
| `name` | string | 名称 | 是 |
| `status` | string | 当前状态 | 是 |
| `ownerCommunityId` | string | 所属 Community ID | 是 |
| `createdAt` | timestamp | 创建时间 | 是 |
| `updatedAt` | timestamp | 更新时间 | 是 |
| `shardKey` | string | 分片键（预留） | 否 |
| `federationId` | string | 联邦标识（预留） | 否 |

### 2.2 核心 Node 类型定义（Phase 1）

#### Satellite
- `nodeType`: `Satellite`
- 关键属性：`noradId`, `orbit`, `owner`, `capabilities`
- 主要关系：`belongsTo` (Community), `controlledBy` (GroundStation)

#### GroundStation
- `nodeType`: `GroundStation`
- 关键属性：`location`, `capabilities`, `status`
- 主要关系：`owns` (Antenna), `providesServiceTo` (Satellite)

#### Task
- `nodeType`: `Task`
- 关键属性：`taskType`, `priority`, `timeWindow`, `status`
- 主要关系：`executedBy` (Satellite), `usesResource` (GroundStation)

#### Community
- `nodeType`: `Community`
- 关键属性：`communityType`, `name`
- 主要关系：`owns` (Node), `memberOf`

---

## 3. 核心事件 Schema（Phase 1 重点）

### 3.1 事件通用结构

所有事件均包含以下字段：

```json
{
  "eventId": "string",
  "eventType": "string",
  "timestamp": "timestamp",
  "sourceNodeId": "string",
  "traceId": "string",
  "payload": "object"
}
```

### 3.2 Phase 1 核心事件列表

| 事件类型 | 触发场景 | 主要消费者 | 说明 |
|----------|----------|------------|------|
| `TelemetryReceived` | 收到新的遥测数据 | Health模块、Knowledge Graph、预警模块 | 遥测数据标准化后的事件 |
| `StateUpdated` | Node 状态发生变化 | 所有订阅者 | 通用状态变更事件 |
| `NodeRegistered` | 新 Node 注册 | Knowledge Graph、资源对接平台 | 新实体接入系统 |
| `TaskStatusChanged` | 任务状态变更 | 资源对接平台、预警模块 | 任务生命周期事件 |
| `ConjunctionAlert` | 检测到碰撞预警 | 预警模块、任务规划、资源对接 | 商业模块产生的重要事件 |
| `ResourceMatchCompleted` | 资源匹配完成 | 任务执行方、Core | 资源对接平台产生的事件 |

### 3.3 事件 Schema 示例（TelemetryReceived）

```json
{
  "eventType": "TelemetryReceived",
  "payload": {
    "satelliteId": "string",
    "timestamp": "timestamp",
    "parameters": {
      "key": "value"
    },
    "quality": "string"
  }
}
```

### 3.4 事件 Schema 示例（ConjunctionAlert）

```json
{
  "eventType": "ConjunctionAlert",
  "payload": {
    "primarySatelliteId": "string",
    "secondaryObjectId": "string",
    "probability": "number",
    "timeOfClosestApproach": "timestamp",
    "impactAssessment": {
      "affectedTasks": ["taskId"],
      "riskLevel": "string"
    }
  }
}
```

---

## 4. Plugin Framework 扩展点（Phase 1）

### 4.1 基础扩展点

| 扩展点名称 | 说明 | 典型使用场景 |
|------------|------|--------------|
| `TelemetryParser` | 遥测解析扩展 | 不同协议的遥测解析 |
| `CommandAdapter` | 遥控指令适配扩展 | 不同卫星的指令格式转换 |
| `EventSubscriber` | 事件订阅扩展 | 插件订阅核心事件 |
| `NodeLifecycleHook` | Node 生命周期钩子 | 新卫星注册时的自定义逻辑 |

### 4.2 扩展点调用规则
- 插件通过事件或明确定义的扩展点与 Core 交互。
- 插件不得直接访问 Core 内部存储。
- 插件发布的事件必须符合核心事件 schema。

---

## 5. Commercial Modules 集成规则

### 5.1 通用集成规则
- Commercial Modules **只能**通过以下方式与 Core 交互：
  - 订阅 Core 发布的事件
  - 调用 Core 提供的公开 API（gRPC / REST）
  - 通过 Plugin Framework 注册扩展点
- 禁止直接访问 Core 数据库或内部服务。

### 5.2 碰撞预警增强服务集成规则
- 必须订阅 `TelemetryReceived`、`StateUpdated`、`NodeRegistered` 事件。
- 必须将生成的 `ConjunctionAlert` 事件发布到 Core Event Bus。
- 可通过 Knowledge Graph API 查询卫星关系和历史数据。

### 5.3 空间任务资源对接平台集成规则
- 必须订阅 `TaskStatusChanged`、`NodeRegistered`、`ConjunctionAlert` 等事件。
- 匹配结果需通过事件（`ResourceMatchCompleted`）反馈给 Core。
- 强烈依赖 Knowledge Graph 进行关系查询和匹配决策。

---

## 6. 版本与变更管理

- 本契约采用语义化版本管理（Semantic Versioning）。
- 核心事件 schema 和对象模型的**破坏性变更**必须通过 RFC 流程。
- 非破坏性变更（新增字段、事件）可直接升级 minor 版本。
- 所有变更需同步更新本文档和相关 SDK。

---

## 7. 后续演进方向（Phase 2+）

- 分层 Event Mesh 相关事件规范
- Knowledge Graph 联邦查询接口
- 更丰富的 Plugin 扩展点（任务规划、仿真、AI 推理等）
- Commercial Modules 之间的标准事件交互规范

---

**文档结束**

*本接口契约文档是 Openspace OS 各模块协作的最高优先级规范，任何实现必须严格遵循。*