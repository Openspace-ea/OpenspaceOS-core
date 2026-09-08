# Openspace OS Core 阿里云部署指南

> 适用版本：Openspace OS Core Phase 1 (MVP)
> 最后更新：2026年7月

---

## 一、ECS 规格选型

### Phase 1（MVP 单机版）

| 配置项 | 推荐规格 | 说明 |
|--------|----------|------|
| **实例规格** | **ecs.g7.large**（2 vCPU / 8 GiB）或 **ecs.c7.large**（2 vCPU / 4 GiB） | MVP 单机 Core + SQLite，2C4G 起步即可；如果同时跑 Commercial Modules 原型建议 2C8G |
| **操作系统** | **Alibaba Cloud Linux 3** 或 **Ubuntu 22.04 LTS** | 均为 Docker 友好型系统，Alibaba Cloud Linux 对阿里云生态优化更好 |
| **系统盘** | **ESSD PL0 100GiB** | SQLite 数据 + 事件日志 + 插件 + 镜像，100G 足够 MVP 阶段 |
| **公网带宽** | **5~10 Mbps 按固定带宽** | 遥测数据接收 + API 访问 + SSH 管理；如有大规模遥测接入建议按量付费或升级 |
| **地域** | 根据用户/地面站位置选择（如华东2-上海、华北2-北京） | 优先靠近地面站网络 |
| **可用区** | 单可用区即可（MVP 不要求跨 AZ 高可用） | — |

**费用估算（包月，华东2）**：
- ecs.g7.large + 100G ESSD + 5Mbps：约 ¥300~400/月
- ecs.c7.large + 100G ESSD + 5Mbps：约 ¥200~280/月

> 如果要把 Commercial Modules（碰撞预警 + 资源对接）也一起部署在同一台机器上做联调，建议升一档：**ecs.g7.xlarge（4 vCPU / 16 GiB）**，约 ¥600~700/月。

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

准备配置文件 `/opt/openspace-os/config/config.yaml`：

```yaml
server:
  http_port: 8080
  grpc_port: 9090

data:
  sqlite_path: /data/openspace-os.db
  event_wal_path: /data/events.wal

plugin:
  directory: /plugins

telemetry:
  tcp_listen: ":7000"

log:
  level: info
  format: json

otel:
  enabled: false
  endpoint: ""
```

### 步骤 5：Dockerfile 与 docker-compose.yml

**Dockerfile**（项目根目录）：

```dockerfile
# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/openspace-os-core ./cmd/openspace-os-core
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/openspace-os-cli ./cmd/openspace-os-cli

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata sqlite-libs
WORKDIR /app
COPY --from=builder /out/openspace-os-core /app/openspace-os-core
COPY --from=builder /out/openspace-os-cli /usr/local/bin/openspace-os-cli
EXPOSE 8080 9090 7000
ENTRYPOINT ["/app/openspace-os-core"]
CMD ["--config", "/config/config.yaml"]
```

**docker-compose.yml**（部署目录）：

```yaml
version: "3.8"

services:
  openspace-os-core:
    build: .
    image: openspace-os-core:latest
    container_name: openspace-os-core
    restart: unless-stopped
    ports:
      - "8080:8080"    # REST API
      - "9090:9090"    # gRPC
      - "7000:7000"    # Telemetry TCP
    volumes:
      - ./data:/data
      - ./plugins:/plugins
      - ./config:/config
      - ./logs:/app/logs
    environment:
      - TZ=Asia/Shanghai
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/healthz"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 10s
    logging:
      driver: "json-file"
      options:
        max-size: "50m"
        max-file: "5"
```

### 步骤 6：部署 Core 服务

```bash
cd /opt/openspace-os

# 方式 A：代码已上传至服务器，直接构建
git clone <仓库地址> src
cd src
docker compose build
docker compose up -d

# 方式 B：通过阿里云 ACR 拉取预构建镜像（推荐生产方式）
# docker login --username=<用户名> registry.cn-shanghai.aliyuncs.com
# docker pull registry.cn-shanghai.aliyuncs.com/<命名空间>/openspace-os-core:latest
# 修改 docker-compose.yml 中 image 字段为 ACR 地址后 up -d
```

### 步骤 7：验证部署

```bash
# 检查容器状态
docker compose ps
docker compose logs -f openspace-os-core

# 健康检查
curl http://localhost:8080/healthz

# 测试注册一个 Community 节点
curl -X POST http://localhost:8080/api/v1/nodes \
  -H "Content-Type: application/json" \
  -d '{
    "nodeId": "comm-001",
    "nodeType": "Community",
    "name": "TestCommunity",
    "status": "active",
    "ownerCommunityId": "comm-001"
  }'

# 查询节点
curl http://localhost:8080/api/v1/nodes/comm-001
```

### 步骤 8：安装 CLI 工具（可选）

```bash
cp /path/to/openspace-os-cli /usr/local/bin/
openspace-os-cli status
openspace-os-cli node list
```

---

## 四、日常运维

### 常用命令

```bash
cd /opt/openspace-os/src

# 查看服务状态
docker compose ps

# 查看日志
docker compose logs -f --tail=200 openspace-os-core

# 重启服务
docker compose restart openspace-os-core

# 停止服务
docker compose down

# 升级（拉取新代码/新镜像后）
git pull && docker compose build && docker compose up -d
```

### 数据备份

```bash
# 手动备份 SQLite 数据库
cp /opt/openspace-os/data/openspace-os.db /opt/openspace-os/backup/openspace-os-$(date +%Y%m%d%H%M).db

# 配置 crontab 每日自动备份
0 2 * * * cp /opt/openspace-os/data/openspace-os.db /opt/openspace-os/backup/openspace-os-$(date +\%Y\%m\%d).db
```

建议同时挂载**阿里云 OSS** 做异地备份（通过 ossfs 或定时 rclone 上传）。

### 可观测性

- **日志**：docker logs + 阿里云日志服务 SLS（配置容器日志采集）
- **指标**：开启 OpenTelemetry 后可对接阿里云 ARMS Prometheus；MVP 阶段可先用 docker logs
- **告警**：阿里云云监控配置 ECS 级别告警（CPU > 80%、内存 > 85%、磁盘 > 80%、进程异常退出）

---

## 五、安全加固

1. **SSH**：禁用密码登录，仅用密钥对；可修改默认 SSH 端口
2. **防火墙**：除安全组外，操作系统内 firewalld/iptables 双重限制
3. **API 认证**：JWT 认证务必启用，生产环境不要使用默认密钥
4. **HTTPS**：公网 API 建议通过阿里云 SLB 配置 SSL 证书，或在 Core 前加 Nginx 反向代理做 TLS 终结
5. **云盘加密**：可启用 ECS 云盘加密功能

---

## 六、部署验收清单

- [ ] `docker compose up -d` 启动成功，容器状态 healthy
- [ ] `curl /healthz` 返回正常
- [ ] 通过 API/CLI 成功注册 Satellite、GroundStation、Community 节点
- [ ] 向 Telemetry 端口（7000）发送模拟遥测帧，验证 TelemetryReceived 事件发布
- [ ] 通过 API 下发遥控指令，收到 CommandAcked 事件
- [ ] 加载示例插件成功
- [ ] `docker compose down` → `up` 后数据不丢失
- [ ] 云监控告警规则配置完毕
