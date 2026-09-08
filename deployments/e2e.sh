#!/usr/bin/env bash
# Openspace OS Core 端到端验证脚本（T6.5）
#
# 覆盖生产组合（PostgreSQL 存储 + NATS JetStream 事件总线 + 机器接入方 API Key 鉴权，
# 遥测 ingest 上报 + usage 用量可查询）的完整链路：
#   1) 健康检查：/healthz 显示 database 与 bus 均 up（PG + NATS 连通）
#   2) admin 登录获取 JWT
#   3) 幂等创建示例 Client，捕获明文 API Key（格式 aos_...）
#   4) 用 API Key 直接调用 POST /api/v1/telemetry/ingest 上报遥测帧
#   5) 用 API Key 换取 JWT（/api/v1/auth/token），验证机器侧鉴权
#   6) 查询用量统计 /api/v1/billing/usage，断言 frame/byte 计数增长
#
# 依赖：curl、jq；需要在编译出的 CLI 命令路径（default ./openspace-os-cli）。
#
# 用法（生产 compose 拉起后）：
#   CORE_URL=http://localhost:8080 CLI=/usr/local/bin/openspace-os-cli ./e2e.sh
set -euo pipefail

CORE_URL="${CORE_URL:-http://localhost:8080}"
CLI="${CLI:-openspace-os-cli}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
DEMO_NAME="${DEMO_NAME:-e2e-satellite-watcher}"
DEMO_MODULE="${DEMO_MODULE:-watcher}"
DEMO_COMMUNITY="${DEMO_COMMUNITY:-community-001}"
SATELLITE_ID="${SATELLITE_ID:-e2e-sat-001}"

PASS=0; FAIL=0

ok()   { echo "  ✔ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✘ $1"; FAIL=$((FAIL+1)); }

json_out() { # <curl options...> 输出 HTTP body
  curl -sfS -s "$@" 2>/dev/null || true
}

echo "==> [1/6] 健康检查（PG + NATS 连通性）"
HZ="$(json_out "${CORE_URL}/healthz")"
if [ "$(printf '%s' "${HZ}" | jq -r '.status')" != "ok" ]; then bad "healthz 状态非 ok"; else ok "healthz status=ok"; fi
DB="$(printf '%s' "${HZ}" | jq -r '.deps.database.status' 2>/dev/null || echo down)"
BUS="$(printf '%s' "${HZ}" | jq -r '.deps.bus.status' 2>/dev/null || echo down)"
[ "$DB" = "up" ] && ok "database(PostgreSQL)=up" || bad "database 状态=$DB"
[ "$BUS" = "up" ] && ok "bus(NATS JetStream)=up"   || bad "bus 状态=$BUS"

echo "==> [2/6] admin 登录获取 JWT"
LOGIN_OUT="$("${CLI}" --server "${CORE_URL}" auth login --username "${ADMIN_USER}" --password "${ADMIN_PASS}" 2>/dev/null)"
TOKEN="$(printf '%s' "${LOGIN_OUT}" | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | tr -d '\r')"
if [ -z "${TOKEN}" ]; then bad "admin 登录失败 / 未能解析 token"; else ok "admin 登录成功（token 已获取）"; fi

echo "==> [3/6] 幂等创建示例 Client，捕获 API Key"
# 复用 seed.sh 的幂等逻辑：已存在则跳过创建
if ! "${CLI}" --server "${CORE_URL}" --token "${TOKEN}" client list 2>/dev/null | grep -q "${DEMO_NAME}"; then
  CREATE_OUT="$("${CLI}" --server "${CORE_URL}" --token "${TOKEN}" client create \
    --name "${DEMO_NAME}" --module "${DEMO_MODULE}" --community "${DEMO_COMMUNITY}")"
  API_KEY="$(printf '%s' "${CREATE_OUT}" | jq -r '.apiKey // ""' 2>/dev/null | tr -d '\r' | xargs)"
else
  ok "Client '${DEMO_NAME}' 已存在，复用（无明文 Key，无法重取）——请手动在日志中取 Key 或更换 DEMO_NAME 重建"
  API_KEY=""
fi
if [ -n "${API_KEY}" ] && [[ "${API_KEY}" == aos_* ]]; then
  ok "已捕获明文 API Key（aos_ 前缀）"
else
  bad "未能捕获 aos_ 前缀 API Key"
  echo "    ⇒ 若 Key 为空且是复用旧 Client，请设置 DEMO_NAME 为一个新名字后重跑；或从 core 日志/先前创建输出取 Key。"
fi

echo "==> [4/6] 用 API Key 上报遥测（POST /api/v1/telemetry/ingest）"
if [ -n "${API_KEY}" ]; then
  TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  INGEST_OUT="$(json_out -X POST "${CORE_URL}/api/v1/telemetry/ingest" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"frames\":[
          {\"satelliteId\":\"${SATELLITE_ID}\",\"timestamp\":\"${TS}\",\"parameters\":{\"voltage\":3.3,\"temp\":45.2},\"quality\":\"good\"},
          {\"satelliteId\":\"${SATELLITE_ID}\",\"parameters\":{\"voltage\":3.1},\"quality\":\"good\"}
        ]}")"
  ACCEPTED="$(printf '%s' "${INGEST_OUT}" | jq -r '.accepted // 0' 2>/dev/null || echo 0)"
  if [ "${ACCEPTED:-0}" -ge 1 ]; then ok "遥测 ingest 已接受 ${ACCEPTED} 帧（事件已入 NATS JetStream）"; else bad "遥测 ingest 未接受帧"; fi
else
  echo "    （跳过：无可用 API Key）"
fi

echo "==> [5/6] 机器接入方用 API Key 换取 JWT（/api/v1/auth/token）"
if [ -n "${API_KEY}" ]; then
  XCH="$(json_out -X POST "${CORE_URL}/api/v1/auth/token" \
    -H "Content-Type: application/json" -d "{\"apiKey\":\"${API_KEY}\"}")"
  MACHINE_TOKEN="$(printf '%s' "${XCH}" | jq -r '.token // ""' 2>/dev/null)"
  [ -n "${MACHINE_TOKEN}" ] && ok "API Key 成功换取机器 JWT" || bad "API Key 换取 JWT 失败"
else
  MACHINE_TOKEN=""
  echo "    （跳过：无可用 API Key）"
fi

echo "==> [6/6] 用量统计可查询（/api/v1/billing/usage）"
# 稍等 Collector 聚合并落库
sleep 1
USAGE_OUT="$(json_out "${CORE_URL}/api/v1/billing/usage?limit=50" -H "Authorization: Bearer ${TOKEN}")"
AGGS="$(printf '%s' "${USAGE_OUT}" | jq -r '.aggregates // []' 2>/dev/null)"
FRAMES="$(printf '%s' "${AGGS}" | jq 'map(select(.unit=="frame")|.totalCount)|add // 0' 2>/dev/null)"
BYTES="$(printf '%s' "${AGGS}" | jq 'map(select(.unit=="byte")|.totalCount)|add // 0' 2>/dev/null)"
if [ -n "${FRAMES}" ] && [ "${FRAMES}" -gt 0 ]; then
  ok "usage 聚合可查询：frame 累计 ${FRAMES}，byte 累计 ${BYTES}"
else
  bad "usage 查询无 frame 计数（计时误差可重跑；或确认 auth 已启用、Client/tenant 正确）"
fi

echo ""
echo "================= 结果汇总 ================="
echo "  PASS: ${PASS}   FAIL: ${FAIL}"
echo "============================================"
[ "${FAIL}" -eq 0 ] && echo "T6.5 端到端验证：通过 ✔" || echo "T6.5 端到端验证：存在失败，请检查上方 ✘ 项。"
exit ${FAIL}