#!/bin/bash
# 冒烟测试脚本：启动服务后验证基本功能
# 用法：先启动服务（make docker-run 或 ./openspace-os-core run），再执行本脚本
# 依赖：curl、jq（Git Bash 环境下需自行安装 jq）
set -e

CORE_URL=${CORE_URL:-http://localhost:8080}

echo "1. 健康检查..."
curl -sf ${CORE_URL}/healthz | jq .status

echo "2. 列出 Schema..."
SCHEMA_COUNT=$(curl -sf ${CORE_URL}/api/v1/schemas | jq length)
echo "  Schema 数量: ${SCHEMA_COUNT}"

echo "3. 注册 Community 节点..."
curl -sf -X POST ${CORE_URL}/api/v1/nodes \
  -H "Content-Type: application/json" \
  -d '{"nodeId":"comm-test","nodeType":"Community","name":"TestCommunity","status":"active","ownerCommunityId":"comm-test"}' | jq .

echo "4. 注册 Satellite 节点..."
curl -sf -X POST ${CORE_URL}/api/v1/nodes \
  -H "Content-Type: application/json" \
  -d '{"nodeId":"sat-test","nodeType":"Satellite","name":"TestSat","status":"active","ownerCommunityId":"comm-test"}' | jq .

echo "5. 建立关系..."
curl -sf -X POST ${CORE_URL}/api/v1/nodes/sat-test/relationships \
  -H "Content-Type: application/json" \
  -d '{"toNodeId":"comm-test","relType":"belongsTo"}' | jq .

echo "6. 图查询..."
curl -sf "${CORE_URL}/api/v1/nodes/sat-test/graph?direction=out&depth=1" | jq .

echo "7. 发送遥测..."
curl -sf -X POST ${CORE_URL}/api/v1/telemetry/send \
  -H "Content-Type: application/json" \
  -d '{"data":"{\"satelliteId\":\"sat-test\",\"timestamp\":\"2026-07-01T00:00:00Z\",\"parameters\":{\"temp\":45.2},\"quality\":\"good\"}"}' | jq .

echo "8. 发送指令..."
curl -sf -X POST ${CORE_URL}/api/v1/commands \
  -H "Content-Type: application/json" \
  -d '{"satelliteId":"sat-test","commandType":"attitude","priority":5,"parameters":{"mode":"normal"}}' | jq .

echo "9. 列出插件..."
curl -sf ${CORE_URL}/api/v1/plugins | jq .

echo "10. Metrics..."
curl -sf ${CORE_URL}/metrics | head -20

echo "冒烟测试全部通过!"
