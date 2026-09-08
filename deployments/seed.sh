#!/usr/bin/env bash
# Openspace OS Core 初始化 seed 脚本（幂等）
#
# 用途：
#   1) 以默认 admin 身份登录获取 JWT（admin / admin123，由 EnsureDefaultAdmin 首次启动创建）
#   2) 幂等创建示例机器接入方（Client），并打印其明文 API Key（仅此一次暴露）
#
# 用法：
#   CORE_URL=http://localhost:8080 CLI=/usr/local/bin/openspace-os-cli ./seed.sh
#
# 可配合 docker compose exec 在 core 容器内执行，例如：
#   docker compose exec openspace-os-core /app/seed.sh
#   （需将本脚本放入镜像 /app/ 或挂载进容器）

set -euo pipefail

CORE_URL="${CORE_URL:-http://localhost:8080}"
CLI="${CLI:-openspace-os-cli}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
DEMO_NAME="${DEMO_NAME:-demo-satellite-watcher}"
DEMO_MODULE="${DEMO_MODULE:-watcher}"
DEMO_COMMUNITY="${DEMO_COMMUNITY:-community-001}"

echo "==> 等待服务就绪: ${CORE_URL}/healthz"
for i in $(seq 1 30); do
  if "${CLI}" --server "${CORE_URL}" health >/dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "!! 服务未在 30s 内就绪" >&2
    exit 1
  fi
  sleep 2
done

echo "==> 登录默认 admin 获取 JWT"
LOGIN_OUT="$("${CLI}" --server "${CORE_URL}" auth login --username "${ADMIN_USER}" --password "${ADMIN_PASS}" 2>/dev/null)"
# 登录输出为 JSON：{"token":"...","expiresIn":...}
TOKEN="$(printf '%s' "${LOGIN_OUT}" | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | tr -d '\r')"
if [ -z "${TOKEN}" ]; then
  echo "!! 未能从登录输出中解析出 JWT，原始输出如下：" >&2
  printf '%s\n' "${LOGIN_OUT}" >&2
  exit 1
fi
export OPENSPACE_TOKEN="${TOKEN}"

echo "==> 检查示例 Client 是否已存在（幂等）"
EXISTS="$("${CLI}" --server "${CORE_URL}" --token "${TOKEN}" client list 2>/dev/null | grep -c "${DEMO_NAME}" || true)"
if [ "${EXISTS}" != "0" ]; then
  echo "==> Client '${DEMO_NAME}' 已存在，跳过创建"
  exit 0
fi

echo "==> 创建示例 Client '${DEMO_NAME}'"
"${CLI}" --server "${CORE_URL}" --token "${TOKEN}" client create \
  --name "${DEMO_NAME}" \
  --module "${DEMO_MODULE}" \
  --community "${DEMO_COMMUNITY}"

echo "==> 完成。请保存上方打印的 API Key（格式 aos_...，用于机器接入方鉴权）。"