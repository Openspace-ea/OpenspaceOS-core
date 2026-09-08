# Openspace OS Core

> 太空操作系统核心平台（Openspace OS Core）—— 基于 Go 实现的平台基座，独立部署并向上层应用（如 Openspace-Satellite-Watcher、SSA-Watcher）提供统一的节点/关系、事件、遥测、认证与计费预留能力。

✅ **上层应用接入必读**：[docs/upstream-apps-integration.md](docs/upstream-apps-integration.md)（接入三步、认证、端点、事件模型、Sample 流程）。运行时可访问 `GET /api/v1/docs`（Swagger UI）查看实时契约。

[![GitHub](https://img.shields.io/badge/GitHub-jeffreytanhao--eng%2Fopenspace--os--core-181717?logo=github&logoColor=white)](https://github.com/jeffreytanhao-eng/openspace-os-core)

仓库：https://github.com/jeffreytanhao-eng/openspace-os-core

## 定位

Openspace OS Core 定位为**平台基座**，独立部署，多个上层应用（如 Openspace-Satellite-Watcher）通过 Core 的接口接入：

- **节点/关系图谱**：Node、Relationship 管理，支持社区（community）维度隔离与 Graph 遍历。
- **事件总线**：基于 NATS JetStream 的权威事件流，发布/订阅/回放，持久化 + TTL。
- **遥测通道**：带鉴权的 HTTP/gRPC 批量上报，批量写入 + 背压保护；保留 TCP 兼容老协议。
- **机器接入方认证**：识别自然人（User/JWT+RBAC）与机器调用方（Client/API Key）两种主体。
- **用量统计**：计量口径 + usage 表 + 聚合查询，为未来计费预留（不实现费率/扣费）。
- **存储**：SQLite（单机/开发默认）+ PostgreSQL（生产多后端），双驱动同一测试集。

## 前置条件

- **Go** 1.26+（[下载地址](https://go.dev/dl/)）
- **Docker**（部署生产依赖 PostgreSQL / NATS 时需要）
- （可选）`golangci-lint` 用于代码检查
- （可选）`protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` 用于生成 gRPC 代码
- 部署脚本依赖 `curl`、`jq`、`bc`

## 快速开始

### 单机模式（默认 SQLite + LocalBus，无外部依赖）

```bash
make build
make run            # 启动 Core，默认监听 :8080，读取 config.yaml
```

验证：

```bash
curl http://localhost:8080/healthz
# 预期输出：{"status":"ok"}
```

### 使用 CLI

```bash
go run ./cmd/openspace-os-cli status --server http://localhost:8080
```

### 测试

```bash
make test            # 或 go test ./...
```

## 架构与能力

| 能力 | 说明 |
| --- | --- |
| **存储双后端** | SQLite（`data.type=sqlite`，默认/开发）+ PostgreSQL（`data.type=postgres`，生产） |
| **内置迁移器** | `internal/storage` 轻量迁移器，`schema_migrations` 版本表，SQLite/PG 双兼容、幂等重放 |
| **事件总线** | `bus.type=local`（单机）或 `bus.type=nats`（NATS JetStream，权威事件流 + 回放） |
| **认证** | 自然人 JWT（User，RBAC）+ 机器调用方 API Key（Client，格式 `aos_...`，bcrypt 哈希） |
| **遥测** | HTTP `POST /api/v1/telemetry/ingest`（单帧/批量）+ gRPC 客户端流 + TCP（老协议，限流） |
| **用量统计** | HTTP 计量中间件 + 异步 Collector 落 `usage` 表 + OTel 指标 + `GET /api/v1/billing/usage` |
| **API** | REST（chi）+ gRPC，均接入统一认证（JWT / API Key），OpenAPI 3.0 契约 |
| **可观测性** | OpenTelemetry metrics / traces 导出 |

## 部署

### 生产模式（PostgreSQL + NATS）

```bash
cd deployments
docker compose up -d --build      # 启动 postgres / nats / openspace-os-core
# 幂等初始化：admin 登录 + 示例 Client（打印明文 aos_... API Key）
bash seed.sh
# 端到端验证
bash e2e.sh
# 性能基线（默认 10 并发 × 100 批 × 5 帧）
CORE_URL=http://localhost:8080 API_KEY=aos_xxx bash bench_load.sh
```

生产 `docker-compose.yml` 通过 `OPENSPACE_*` 环境变量切换为 `data.type=postgres`、`bus.type=nats`、`auth.enabled=true`。

### 单机回退模式（SQLite + LocalBus）

```bash
cd deployments
docker compose -f docker-compose.dev.yml up -d --build
```

## 数据迁移（SQLite → PostgreSQL）

将存量 SQLite 数据导入 PostgreSQL（幂等，目标库已存在的主键自动跳过）：

```bash
go run ./cmd/openspace-os-cli migrate import \
  --sqlite ./data/openspace-os.db \
  --dsn 'postgres://aos:aos_secret@localhost:5432/openspace?sslmode=disable'
```

## 目录结构

```
.
├── cmd/                      # 服务入口
│   ├── openspace-os-core/             # Core 服务入口（HTTP + gRPC，运行时 schema 迁移）
│   └── openspace-os-cli/              # CLI 工具（node/rel/event/telemetry/auth/client/migrate）
├── internal/
│   ├── core/                 # 核心领域：Node / KG / Event / 事件总线（Local + NATS）
│   ├── storage/              # 内置轻量迁移器（SQLite/PG 双兼容）
│   ├── usage/                # 用量计量、usage 表、异步 Collector
│   ├── auth/                 # 身份认证 / RBAC / Client（API Key）
│   ├── pipeline/
│   │   ├── telemetry/        # 遥测流水线（HTTP/gRPC/TCP 批量 + 背压）
│   │   └── command/          # 指令流水线
│   ├── plugin/               # 插件框架（HashiCorp go-plugin，热加载）
│   ├── api/
│   │   ├── rest/             # REST API（chi + OpenAPI）
│   │   └── grpc/             # gRPC API（含 AuthInterceptor）
│   ├── config/               # 配置管理（viper）
│   └── observability/        # OpenTelemetry 可观测性
├── pkg/                      # 对外可复用包
│   ├── model/                # Node 数据模型
│   ├── event/                # 事件结构定义
│   └── errors/               # 统一错误类型
├── sdk/
│   └── python/               # Python SDK（占位）
├── deployments/              # Dockerfile / docker-compose（prod + dev）/ seed / e2e / bench
├── plugins/
│   └── examples/             # 示例插件
├── proto/                    # gRPC protobuf 定义
├── docs/                     # 上层应用对接接口文档（upstream-apps-integration.md）
├── config.yaml               # 配置文件示例
└── Makefile                  # 构建/测试/运行脚本
```

## 配置

配置通过 `config.yaml` 管理，支持环境变量覆盖（前缀 `OPENSPACE_`，层级用下划线分隔），例如：

```bash
OPENSPACE_DATA_TYPE=postgres \
OPENSPACE_DATA_POSTGRES_DSN='postgres://aos:aos_secret@localhost:5432/openspace?sslmode=disable' \
OPENSPACE_BUS_TYPE=nats \
OPENSPACE_BUS_URL='nats://localhost:4222' \
./bin/openspace-os-core run
```

配置段与键（`config.yaml`，均可环境变量覆盖）：

| 段 | 键 | 说明 |
| --- | --- | --- |
| `server` | `http_port` / `grpc_port` / `telemetry_port` | 服务端口 |
| `data` | `type`（`sqlite`\|`postgres`）/ `sqlite_path` / `postgres_dsn` | 存储后端 |
| `bus` | `type`（`local`\|`nats`）/ `url` / `stream_name` / `max_stream_age` | 事件总线，`max_stream_age` 即事件 TTL（默认 168h） |
| `plugin` | `directory` | 插件目录 |
| `log` | `level` / `format` | 日志级别与格式 |
| `otel` | `enabled` / `endpoint` / `service_name` | OpenTelemetry 导出 |
| `auth` | `enabled` / `secret_key` / `token_duration` | 认证开关、JWT 密钥（生产务必覆盖）、令牌有效期（小时） |

> 说明：机器调用方 API Key 前缀固定为 `aos_`；`secret_key` 为 JWT 签名密钥，生产环境务必通过环境变量覆盖。

详见 [`config.yaml`](./config.yaml)。

## 技术栈

| 能力 | 选型 |
| --- | --- |
| HTTP 框架 | [chi](https://github.com/go-chi/chi) |
| gRPC | [grpc-go](https://google.golang.org/grpc) |
| 日志 | 标准库 `log/slog` |
| 配置 | [viper](https://github.com/spf13/viper) |
| CLI | [cobra](https://github.com/spf13/cobra) |
| 存储 | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)（纯 Go，无 CGO）+ [pgx](https://github.com/jackc/pgx)（PostgreSQL，无 CGO） |
| 迁移 | 内置轻量迁移器（`internal/storage`，SQLite/PG 双兼容） |
| 事件总线 | [NATS JetStream](https://nats.io) + 内置 `LocalBus` 回退 |
| 认证 | [golang-jwt/jwt/v5](https://github.com/golang-jwt/jwt/v5) + bcrypt |
| 插件 | [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin) |
| 遥测 | OpenTelemetry SDK |
| 测试 | [testify](https://github.com/stretchr/testify) |