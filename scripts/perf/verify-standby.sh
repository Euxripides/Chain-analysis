#!/usr/bin/env bash
# S7-PERF-02：在备用端口验证 bin/api.new（P1-B 安全 standby 启动：不跑后台循环、不做
# 导出对账，路由/中间件/健康检查与生产实例完全一致），确认无误后再覆盖 bin/api。
#
# 用法：bash scripts/perf/verify-standby.sh <label> [n] [warmup]
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
LABEL="${1:-standby}"
N="${2:-3}"
WARM="${3:-1}"
PORT="${STANDBY_PORT:-18099}"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$P/deploy/.env"; set +a
export API_HTTP_PORT="$PORT"
export API_STANDBY_STARTUP=1

BIN="${BIN:-$P/backend/bin/api.new}"
[ -x "$BIN" ] || { echo "缺少 $BIN"; exit 2; }
echo "== binary =="; sha256sum "$BIN"

"$BIN" > /tmp/"$LABEL"-api.log 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null' EXIT

ok=""
for i in $(seq 1 60); do
  if curl -sf -o /dev/null --max-time 2 "http://127.0.0.1:$PORT/health/live"; then ok="${i}"; break; fi
  if ! kill -0 $PID 2>/dev/null; then echo "FATAL: 进程已退出"; tail -20 /tmp/"$LABEL"-api.log; exit 1; fi
  sleep 0.5
done
[ -n "$ok" ] || { echo "FATAL: /health/live 未就绪"; tail -20 /tmp/"$LABEL"-api.log; exit 1; }
echo "health/live=200 (after ${ok} polls)"
printf 'health/ready=%s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "http://127.0.0.1:$PORT/health/ready")"

BASE="http://127.0.0.1:$PORT" bash "$P/scripts/perf/lan-explorer-probe.sh" "$LABEL" "$N" "$WARM" "$P/.work/perf-$LABEL"
echo "== log tail =="; tail -5 /tmp/"$LABEL"-api.log
