# Openspace OS Core 阿里云部署指南

> 适用：Openspace OS Core（平台基座，支撑 Satellite-Watcher / SSA-Watcher 等上层应用接入）
> 最后更新：2026年8月
> 生产形态：Core + PostgreSQL + NATS JetStream 三容器（`deployments/docker-compose.yml`）；单机回退为 SQLite + LocalBus（`docker-compose.dev.yml`）

---

## 〇、选型结论：轻量服务器 还是 ECS？

| 场景 | 推荐 | 理由 |
|------|------|------|
| 低成本起步 / 内部试用 / 并发不高 | **轻量应用服务器 2C4G** | `docker compose up -d` 一键起 Core+PG+NATS，固定低价、自带公网 IP |
| 正式对外服务多个上层应用（推荐） | **ECS + RDS PostgreSQL** | 弹性、VPC 网络、SLB 负载均衡、随时升配与高可用，符合平台基座定位 |

> Core 本身很省（Go 单体，几十 MB 级内存），资源大头在 PostgreSQL（建议 ≥1–2 GiB）。本文以**生产模式（PG + NATS）**为主线展开，单机回退模式只需用 `docker-compose.dev.yml` 并跳过 PostgreSQL/NATS 相关步骤。

---

## 一、ECS 规格选型

### 生产模式（Core + PostgreSQL + NATS）

| 配置项 | 推荐规格 | 说明 |
|--------|----------|------|
| **实例规格** | **ecs.c7.large**（2 vCPU / 4 GiB）起步；遥测量大可 **ecs.c7.xlarge**（4 vCPU / 8 GiB） | Core+PG+NATS 三容器 2C4G 即可跑顺；商用模块联调建议 2C8G |
| **操作系统** | **Alibaba Cloud Linux 3** 或 **Ubuntu 22.04 LTS** | 均 Docker 友好，Alibaba Cloud Linux 对阿里云生态更优 |
| **系统盘** | **ESSD PL0 100GiB** | 镜像 + 容器数据 + PG 数据卷足够 |
| **公网带宽** | **5~10 Mbps 固定带宽**起；大流量遥测按量付费 | REST/遥测/SSH；大量接入建议按量 |
| **地域** | 靠近用户/地面站（华东2-上海、华北2-北京等） | 靠近地面站网络 |
| **数据库演进** | 起步容器内 PG；正式对外建议迁 **RDS for PostgreSQL**（自动备份/高可用） | 本指南第 §3 的 PostgreSQL 服务可由 RDS 链接串（`postgres_dsn`）替换 |

**费用估算（包月，华东2）**：
- ecs.c7.large + 100G ESSD + 5Mbps：约 ¥200~280/月

### 起步档：阿里云轻量应用服务器（推荐低成本试用）

按当前实际选购的轻量配置，可直接下单：

| 配置项 | 取值 | 说明 |
|--------|------|------|
| vCPU / 内存 | **2 vCPU / 4 GiB** | 三容器（Core+PG+NATS）即可跑顺；商用模块联调建议升 4 vCPU/8 GiB |
| 系统盘 | **50 GiB** | 刻镜像+PG 数据卷足够；遥测/事件在 PG 累积，建议配定期清理+OSS 备份 |
| 带宽 | **200 Mbps 峰值（无固定流量）** | 峰值打流性能够；**超量按流量计费**，大流量接入建议改固定带宽 |
| 公网 IP | **固定 1 IPv4** | 即 `OPENSPACE_CORE_URL` 使用地址（或后续绑域名） |
| 系统镜像 | **Alibaba Cloud Linux 3.21** | Docker 友好 |
| 地域 | 北京 | 就近上层应用/地面站即可 |

> 轻量服务器适合**低成本起步/试用/并发不高的对外服务**；多上层应用长期正式对外建议按上方 ECS + RDS PostgreSQL 演进（弹性、VPC、SLB、可升配高可用）。

### Phase 2~3 扩容路径（参考）

| 阶段 | 架构变化 | 推荐阿里云资源 |
|------|----------|---------------|
| Phase 2 | Core + 插件沙箱 + 基础联邦 | 2~3 台 ECS + SLB + 云数据库 RDS PostgreSQL + 图数据库 GDB(Neo4j) |
| Phase 3 | 分布式高可用 + Event Mesh | ACK（容器服务）+ RDS + GDB + 消息队列 Kafka/NATS + 日志服务 SLS + ARMS 监控 |

---

## 二、网络与安全组配置

### 安全组规则（入方向）

| 协议 | 端口 | 源地址 | 用途 |
|------|------|--------|------|
| TCP | 22 | 管理 IP/办公网段 | SSH 远程管理 |
| TCP | 8080 | 需要访问 REST API 的网段 | Core REST API |
| TCP | 9090 | 需要访问 gRPC 的网段 | Core gRPC API |
| TCP | 4317 | 本机/同 VPC | OpenTelemetry gRPC（如有外部 Collector） |
| TCP/UDP | 7000~7100 | 地面站/卫星模拟器 IP 段 | Telemetry 接收端口（按协议需要开放，可后续按需添加） |
| ICMP | — | 管理 IP | ping 探测（可选） |

> **安全建议**：8080/9090 不建议直接对公网 0.0.0.0/0 开放，生产环境应通过 **SLB + 白名单** 或 **VPN/专线** 访问。MVP 测试阶段可用但建议限制源 IP。

### 其他网络建议
- **VPC**：创建专用 VPC（如 10.0.0.0/16），Core 放一个独立 vSwitch
- **弹性公网 IP (EIP)**：绑定 ECS 提供公网访问
- **安全组最小权限原则**：只开放必要端口，管理端口限制源 IP

---

## 三、部署步骤

### 步骤 1：购买并初始化 ECS

1. 登录阿里云控制台 → 云服务器 ECS → 创建实例
2. 按上方规格选择配置
3. 密钥对登录（推荐）或设置 root 密码
4. 绑定 EIP，配置安全组
5. 启动后 SSH 登录：
   ```bash
   ssh -i your-key.pem root@<EIP>
   ```

### 步骤 2：系统初始化

```bash
# 更新系统（Alibaba Cloud Linux 3 / CentOS 系）
yum update -y

# Ubuntu 系使用：
# apt update && apt upgrade -y

# 安装基础工具
yum install -y git curl wget vim       # Alibaba Cloud Linux
# apt install -y git curl wget vim     # Ubuntu

# 设置时区
timedatectl set-timezone Asia/Shanghai

# 关闭 selinux（Alibaba Cloud Linux，如不影响安全策略）
setenforce 0
sed -i 's/SELINUX=enforcing/SELINUX=disabled/g' /etc/selinux/config
```

### 步骤 3：安装 Docker 和 Docker Compose

```bash
# 安装 Docker（Alibaba Cloud Linux 3 / CentOS）
yum install -y yum-utils
yum-config-manager --add-repo https://mirrors.aliyun.com/docker-ce/linux/centos/docker-ce.repo
yum install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Ubuntu：
# apt install -y docker.io docker-compose-plugin

# 配置镜像加速（登录阿里云容器服务控制台获取专属加速地址）
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": ["https://<你的加速地址>.mirror.aliyuncs.com"],
  "log-driver": "json-file",
  "log-opts": {"max-size": "100m", "max-file": "3"}
}
EOF

# 启动 Docker
systemctl enable docker
systemctl start docker
docker --version
docker compose version
```

### 步骤 4：创建部署目录

```bash
mkdir -p /opt/openspace-os/{data,plugins,config,logs,backup}
cd /opt/openspace-os
```

准备配置文件 `/opt/openspace-os/config/config.yaml`（键与当前 `config.yaml` 一致；`secret_key` 生产务必修改）：

```yaml
server:
  http_port: 8080
  grpc_port: 9090
  telemetry_port: 7000

data:
  type: postgres            # 生产用 postgres；单机回退为 sqlite
  postgres_dsn: "postgres://aos:<password>@<pg-host>:5432/openspace?sslmode=disable"

bus:
  type: nats                # 生产用 nats；单机回退为 local
  url: "nats://<nats-host>:4222"
  stream_name: "openspace"
  max_stream_age: "168h"    # 事件 TTL（默认 7 天）

plugin:
  directory: /plugins

log:
  level: info
  format: json

otel:
  enabled: false
  endpoint: ""
  service_name: openspace-os-core

auth:
  enabled: true
  secret_key: "change-me-in-production"   # JWT 签名密钥，生产务必覆盖
  token_duration: 24                      # 令牌有效期（小时）
```

> 生产环境建议用环境变量注入（`OPENSPACE_*`，优先级高于配置）：`OPENSPACE_DATA_POSTGRES_DSN`、`OPENSPACE_BUS_URL`、`OPENSPACE_AUTH_SECRET_KEY` 等。

### 步骤 5：镜像与 docker-compose（生产模式）

> **构建说明**：Core 使用纯 Go SQLite（`modernc.org/sqlite`）与 `pgx`，**无需 CGO**。Dockerfile 已提交到仓库顶层，多阶段构建如下。

**Dockerfile**（项目根目录，即 `deployments/Dockerfile`）：

```dockerfile
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/openspace-os-core ./cmd/openspace-os-core
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/openspace-os-cli ./cmd/openspace-os-cli

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/openspace-os-core /app/openspace-os-core
COPY --from=builder /out/openspace-os-cli /usr/local/bin/openspace-os-cli
COPY config.yaml /app/config.yaml
EXPOSE 8080 9090 7000
ENTRYPOINT ["/app/openspace-os-core"]
CMD ["run", "--config", "/app/config.yaml"]
```

**docker-compose.yml**（直接使用仓库 `deployments/docker-compose.yml`，含 PostgreSQL + NATS + Core 三服务）：

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: aos
      POSTGRES_PASSWORD: aos_secret
      POSTGRES_DB: openspace
    ports: ["5432:5432"]          # 供本机 CLI/迁移/测试连接
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U aos -d openspace"]

  nats:
    image: nats:2.10-alpine
    command: ["-js", "-m", "8222"] # 启用 JetStream
    volumes: [natsdata:/data]
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://localhost:8222/healthz || exit 1"]

  openspace-os-core:
    build: ..
    restart: unless-stopped
    ports: ["8080:8080", "9090:9090", "7000:7000"]
    environment:
      - OPENSPACE_DATA_TYPE=postgres
      - OPENSPACE_DATA_POSTGRES_DSN=postgres://aos:aos_secret@postgres:5432/openspace?sslmode=disable
      - OPENSPACE_BUS_TYPE=nats
      - OPENSPACE_BUS_URL=nats://nats:4222
      - OPENSPACE_AUTH_ENABLED=true
      - OPENSPACE_AUTH_SECRET_KEY=change-me-in-production
    depends_on:
      postgres: { condition: service_healthy }
      nats: { condition: service_healthy }
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/healthz"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  pgdata:
  natsdata:
```

**单机回退模式**：将 `OPENSPACE_DATA_TYPE=sqlite`、`OPENSPACE_BUS_TYPE=local`，并去掉 `postgres`/`nats` 两个服务，即对应仓库的 `docker-compose.dev.yml`。

### 步骤 6：部署 Core 服务

```bash
cd /opt/openspace-os

# 方式 A：代码已上传至服务器，克隆后一键起（生产 = PG + NATS + Core）
git clone <仓库地址> app && cd app
docker compose -f deployments/docker-compose.yml up -d --build
# 单机回退（SQLite + LocalBus）：
# docker compose -f deployments/docker-compose.dev.yml up -d --build

# 幂等初始化：admin 登录 + 创建示例机器接入方（打印明文 aos_... API Key）
bash deployments/seed.sh
# 端到端自检
bash deployments/e2e.sh

# 方式 B：通过阿里云 ACR 拉取预构建镜像（推荐生产方式）
# docker login --username=<用户名> registry.cn-shanghai.aliyuncs.com
# docker tag 本地镜像 registry.cn-shanghai.aliyuncs.com/<命名空间>/openspace-os-core:latest
# docker push ... 后，修改 compose 的 image 字段为 ACR 地址再 up -d
```

### 步骤 7：验证部署（含认证与上层应用对接参数）

```bash
docker compose -f deployments/docker-compose.yml ps
docker compose -f deployments/docker-compose.yml logs -f openspace-os-core

# 健康检查（含数据库/事件总线依赖状态）
curl http://localhost:8080/healthz
# → {"status":"ok","version":"…","deps":{"database":{"status":"up"},"bus":{"status":"up"}}}

# —— 认证后注册节点 ——
# 1) 创建机器接入方（返回明文 API Key，仅此一次）
#    也可用 CLI：aos client create --name ssa-watcher --module SSA-Watcher --community comm-ssa-1
curl -s http://localhost:8080/api/v1/clients -X POST \
  -H "Authorization: Bearer <admin-token>" -H "Content-Type: application/json" \
  -d '{"name":"ssa-watcher","moduleName":"SSA-Watcher","communityId":"comm-ssa-1"}'
# → {"client":{...},"apiKey":"aos_xxx"}

# 2) 用 API Key 换 JWT（也是上层应用对接的连通性自检）
curl -s http://localhost:8080/api/v1/auth/token \
  -H "Content-Type: application/json" -d '{"apiKey":"aos_xxx"}'
# → {"token":"<jwt>","expiresIn":<秒>}  → 导出 TOKEN=<jwt>

# 3) 注册节点（ownerCommunityId 需与 client.communityId 一致）
curl -X POST http://localhost:8080/api/v1/nodes \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"nodeId":"comm-001","nodeType":"Community","name":"TestCommunity","status":"active","ownerCommunityId":"comm-ssa-1"}'

# 4) 订阅事件（SSE，需鉴权）
curl -N http://localhost:8080/api/v1/events/subscribe?types=NodeRegistered \
  -H "Authorization: Bearer $TOKEN"
```

> **上层应用对接只需三个变量**：`OPENSPACE_CORE_URL`(=Core 地址，如 `http://<core-host>:8080`)、`OPENSPACE_API_KEY`(=创建 Client 返回的 `aos_...`)、`OPENSPACE_COMMUNITY_ID`(=创建 Client 时传入的 `communityId`)。完整对接说明见 `docs/upstream-apps-integration.md`。

### 步骤 8：安装 CLI 工具（可选）

```bash
# CLI 已内置在 Docker 镜像中，可通过 docker exec 使用
docker exec openspace-os-core openspace-os-cli status
docker exec openspace-os-core openspace-os-cli node list

# 或将 CLI 拷贝到宿主机
docker cp openspace-os-core:/usr/local/bin/openspace-os-cli /usr/local/bin/
openspace-os-cli status
openspace-os-cli node list
```

---

## 四、Windows/Linux 本地原生构建

> Core 使用纯 Go SQLite 与 pgx，**无需 CGO**，本地直接 `go build` 即可。

### Linux

```bash
# 安装 Go 1.26+
# 构建（无需 gcc）
go build -o bin/openspace-os-core ./cmd/openspace-os-core
go build -o bin/openspace-os-cli ./cmd/openspace-os-cli

# 运行
./bin/openspace-os-core --config config.yaml
```

### Windows

```bash
# 安装 Go 1.26+

# 构建（无需 MinGW / CGO）
go build -o bin\openspace-os-core.exe .\cmd\openspace-os-core
go build -o bin\openspace-os-cli.exe .\cmd\openspace-os-cli

# 运行
.\bin\openspace-os-core.exe --config config.yaml
```

---

## 五、日常运维

### 常用命令

```bash
# 生产 compose 路径
COMPOSE="docker compose -f deployments/docker-compose.yml"

$COMPOSE ps
$COMPOSE logs -f --tail=200 openspace-os-core
$COMPOSE restart openspace-os-core          # 重启
$COMPOSE down                                # 停止（卷保留，数据不丢）

# 升级（拉取新代码/新镜像后）
git pull && $COMPOSE up -d --build

# 数据迁移（SQLite → PostgreSQL，见下）
./bin/openspace-os-cli migrate import --sqlite ./data/openspace-os.db \
  --dsn "$OPENSPACE_DATA_POSTGRES_DSN"
```

### 数据备份

生产使用 PostgreSQL，备份在容器内执行：

```bash
# PostgreSQL：逻辑备份
docker exec <postgres容器> pg_dump -U aos -d openspace \
  > /opt/openspace-os/backup/openspace-$(date +%Y%m%d%H%M).sql

# PostgreSQL：物理一致性快照（推荐 + RDS 或定期）
# 或生产直接选用 RDS for PostgreSQL 自带自动备份
```

> 单机回退（SQLite）：直接拷贝 `data/openspace-os.db`。若从旧 SQLite 升级到 PG 生产库，用上面的 `aos migrate import`（幂等，可重复执行）。

建议同时挂载**阿里云 OSS** 做异地备份（通过 ossfs 或定时 rclone 上传）。

### 可观测性

- **日志**：docker logs + 阿里云日志服务 SLS（配置容器日志采集）
- **指标**：开启 OpenTelemetry 后可对接阿里云 ARMS Prometheus；MVP 阶段可先用 docker logs
- **告警**：阿里云云监控配置 ECS 级别告警（CPU > 80%、内存 > 85%、磁盘 > 80%、进程异常退出）

### 开启 OpenTelemetry（可选）

修改 `config.yaml`：

```yaml
otel:
  enabled: true
  endpoint: "otlp-collector:4317"   # OTLP gRPC 端点
  service_name: openspace-os-core
```

或在 docker-compose.yml 中增加 OTel Collector 容器：

```yaml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    container_name: otel-collector
    ports:
      - "4317:4317"    # OTLP gRPC
      - "4318:4318"    # OTLP HTTP
    volumes:
      - ./otel-config.yaml:/etc/otelcol/config.yaml
    command: ["--config=/etc/otelcol/config.yaml"]
```

---

## 六、安全加固

1. **SSH**：禁用密码登录，仅用密钥对；可修改默认 SSH 端口
2. **防火墙**：除安全组外，操作系统内 firewalld/iptables 双重限制
3. **API 认证**：开启 `auth.enabled=true`（`OPENSPACE_AUTH_ENABLED=true`）；生产**必须修改** `secret_key`。API Key 仅对需要的上层应用创建，泄露时用 `aos client revoke` 撤销
4. **密钥**：`OPENSPACE_AUTH_SECRET_KEY`、PG 密码不硬编码在 compose 里，生产用 secret 注入；`jwt_secret` 旧名不再使用
5. **HTTPS**：公网 API 建议通过阿里云 SLB 配置 SSL 证书，或在 Core 前加 Nginx 反向代理做 TLS 终结
6. **网络**：8080/9090 不直接对公网 0.0.0.0/0 开放，生产经 SLB/白名单或 VPN/专线
7. **云盘加密**：可启用 ECS 云盘加密功能

---

## 七、部署验收清单

- [ ] `docker compose -f deployments/docker-compose.yml up -d` 后 PG/NATS/Core 三容器 healthy
- [ ] `curl /healthz` 返回 `"status":"ok"`，`deps.database`/`deps.bus` 均为 `up`
- [ ] seed.sh 成功执行，创建到用户（拿到明文 `aos_...` API Key）
- [ ] 用 API Key 调 `POST /api/v1/auth/token` 换到 JWT
- [ ] 通过 API/CLI 成功注册 Satellite、GroundStation、Community、Mission 节点
- [ ] 向 Telemetry 端口（7000）发送模拟遥测帧，验证 TelemetryReceived 事件发布
- [ ] 通过 API 下发遥控指令，收到 CommandAcked 事件
- [ ] `docker compose down` → `up` 后 PG 数据不丢失
- [ ] （可选）运行 `deployments/e2e.sh` 全 PASS；跑 `deployments/bench_load.sh` 记录基准
- [ ] 云监控告警规则配置完毕
- [ ] 生产已修改 `secret_key` 与 PG 密码，认证已启用
