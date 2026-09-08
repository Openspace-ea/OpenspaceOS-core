#!/usr/bin/env bash
# Openspace OS Core 性能基线加载脚本（T6.6）
#
# 以多模块并发（默认 10 个模拟 client）通过 HTTP 遥测 ingest 入口打流，
# 逐请求采样延迟，统计：请求通过率（PASS%）、事件吞吐（frames/sec）、
# P50 / P95 / P99 延迟。
#
# 依赖：curl、jq、bc（可在容器内 apk add 后执行）。
# 需先部署完成并存在可用 API Key（见 e2e.sh / seed.sh）。
#
# 用法：
#   CORE_URL=http://localhost:8080 API_KEY=aos_xxx ./bench_load.sh
set -uo pipefail

CORE_URL="${CORE_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-}"                    # 必填：机器接入方 API Key（aos_ 前缀）
CLIENTS="${CLIENTS:-10}"                  # 模拟并发 client 数
FRAMES_PER_REQ="${FRAMES_PER_REQ:-5}"     # 每请求携带帧数
BATCHES="${BATCHES:-100}"                 # 每 client 发送的请求批次数
SATELLITE_ID="${SATELLITE_ID:-bench-sat}"

if [ -z "${API_KEY}" ]; then
  echo "!! 请通过 API_KEY 环境变量提供机器接入方 API Key（格式 aos_...）" >&2
  echo "   可先运行 seed.sh / e2e.sh 创建 Client 并获得 Key。" >&2
  exit 1
fi

# 当前 epoch 秒（整数）。busybox/GNU date 均支持 %s；不使用 %N（busybox 不支持）。
epoch_s() { date +%s; }

WORKFDIR="$(mktemp -d)"

# 并发打流：每个 client 单独子 shell，串行发送 BATCHES 个批量请求。
# 用 curl 的 %{time_total}（秒，浮点）精确采样每请求延迟，并加超时防止挂起。
for ((c=0; c<CLIENTS; c++)); do
  (
    pass=0; done=0
    SAMPLE="${WORKFDIR}/samples.${c}"
    : > "${SAMPLE}"
    for ((b=0; b<BATCHES; b++)); do
      ts="$((2000000000 + b))"
      payload='{"frames":[]}'
      for ((f=0; f<FRAMES_PER_REQ; f++)); do
        payload="$(printf '%s' "${payload}" | jq --arg sid "${SATELLITE_ID}-${c}" --argjson v $((b+f)) \
          '.frames += [{"satelliteId":$sid,"parameters":{"voltage":$v,"temp":45.2},"quality":"good"}]')"
      done
      # curl 输出两列：HTTP 状态码、耗时秒浮点；超时关闭的生效但不计入 latency
      read -r code tt <<< "$(curl -s --connect-timeout 5 --max-time 20 -o /dev/null \
        -w '%{http_code} %{time_total}' -X POST "${CORE_URL}/api/v1/telemetry/ingest" \
        -H "Authorization: Bearer ${API_KEY}" -H "Content-Type: application/json" -d "${payload}")"
      ms="$(echo "scale=0; ${tt:-0}*1000/1" | bc 2>/dev/null || echo 0)"
      printf '%s\n' "${ms:-0}" >> "${SAMPLE}"
      done=$((done+1))
      [ "$code" = "202" ] && pass=$((pass+1))
    done
    printf '%s %s\n' "$pass" "$done" > "${WORKFDIR}/c_${c}.out"
  ) &
done
START="$(epoch_s)"
wait
END="$(epoch_s)"

ELAPSED_S=$((END-START))
if [ "${ELAPSED_S}" -lt 1 ]; then ELAPSED_S=1; fi

# 聚合请求数与通过数
TOT_PASS=0; TOT_DONE=0
for f in "${WORKFDIR}"/c_*.out; do
  read -r p d < "${f}"
  TOT_PASS=$((TOT_PASS+p)); TOT_DONE=$((TOT_DONE+d))
done

# 合并全部延迟样本，计算 P50 / P95 / P99
ALL_SAMPLES="${WORKFDIR}/all.samples"
: > "${ALL_SAMPLES}"
for f in "${WORKFDIR}"/samples.*; do
  cat "${f}" >> "${ALL_SAMPLES}"
done
N="$(wc -l < "${ALL_SAMPLES}" | tr -d ' ')"
if [ "${N:-0}" -gt 0 ]; then
  SORTED="$(sort -n "${ALL_SAMPLES}")"
  pct() { # <percentile>
    echo "${SORTED}" | awk -v n="$N" -v p="$1" 'BEGIN{idx=int(((p/100)*n)+0.999); if(idx<1)idx=1} NR==idx{print; exit}'
  }
  P50="$(pct 50)"; P95="$(pct 95)"; P99="$(pct 99)"
else
  P50=0; P95=0; P99=0
fi
rm -rf "${WORKFDIR}"

TOT_FRAMES="$((TOT_DONE*FRAMES_PER_REQ))"
PCT="$(echo "scale=2; 100*${TOT_PASS}/${TOT_DONE}" | bc 2>/dev/null || echo 100)"
THROUGHPUT="$(echo "scale=1; ${TOT_FRAMES}/${ELAPSED_S}" | bc 2>/dev/null || echo 0)"

cat <<EOF
==================== 性能基线（T6.6） ====================
  并发 client 数        : ${CLIENTS}
  请求总数              : ${TOT_DONE}
  请求通过(PASS)数      : ${TOT_PASS}     （HTTP 202）
  通过率(PASS%)         : ${PCT}%
  总帧数                : ${TOT_FRAMES}
  耗时(s)               : ${ELAPSED_S}
  事件吞吐(frames/s)    : ${THROUGHPUT}
  延迟 P50(ms)          : ${P50}
  延迟 P95(ms)          : ${P95}
  延迟 P99(ms)          : ${P99}
=========================================================
EOF