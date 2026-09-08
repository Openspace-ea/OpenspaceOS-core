# Openspace OS Python SDK（占位）

本目录用于存放 Openspace OS 的 Python SDK，当前为占位说明，暂未实现。

## 规划

Python SDK 将提供与 Openspace OS Core REST API（`/api/v1`）交互的高层封装，包括：

- 节点管理（注册、查询、更新、删除、列表）
- 关系管理（创建、删除、图遍历）
- 事件订阅（SSE 实时订阅）与回放
- 遥控指令（发送、状态查询、取消、列表）
- 遥测（解析器管理、手动发送）
- 插件管理（列表、卸载）
- 认证（登录、获取当前用户）

## 使用示例（规划）

```python
from aos import Openspace OSClient

client = Openspace OSClient(base_url="http://localhost:8080", token="jwt-token")

# 注册节点
node = client.nodes.register(
    node_id="sat-1",
    node_type="Satellite",
    name="TestSat",
    status="active",
    owner_community_id="comm-1",
)

# 列出节点
nodes = client.nodes.list(node_type="Satellite")

# 发送指令
cmd = client.commands.send(
    satellite_id="sat-1",
    command_type="attitude",
    priority=5,
    parameters={"mode": "normal"},
)
```

## 状态

- [ ] 核心客户端封装（HTTP 请求、认证、错误处理）
- [ ] 节点管理模块
- [ ] 关系管理模块
- [ ] 事件模块（含 SSE 客户端）
- [ ] 指令模块
- [ ] 遥测模块
- [ ] 插件模块
- [ ] 认证模块

> 在 Python SDK 实现前，可通过 `openspace-os-cli` 命令行工具或直接调用 REST API 与 Openspace OS Core 交互。
