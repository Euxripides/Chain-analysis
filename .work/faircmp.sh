#!/usr/bin/env bash
# 公平对照：同一时刻、同一方法（串行单请求），把**冻结版**跑在备用端口 18099 上与
# **合并版**跑在 8080 上背靠背测量。这样可以把"改动带来的收益"与"机器负载波动"分开：
# 首次改前基线是在别人做回滚演练/压测的窗口里测的，单独拿出来对比不公平。
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$P/deploy/.env"; set +a
OLD="$HOME/deploy-backups/pre-perf-20260916T174927Z/api.pre-perf"
echo "old sha: $(sha256sum "$OLD" | awk '{print $1}')"
echo "new sha: $(sha256sum "$P/backend/bin/api" | awk '{print $1}')"
echo "load: $(uptime)"

echo "=== 冻结版（P1-B standby, port 18099）==="
API_HTTP_PORT=18099 API_STANDBY_STARTUP=1 "$OLD" > /tmp/old-standby.log 2>&1 &
OLDPID=$!
for i in $(seq 1 90); do
  curl -sf -o /dev/null --max-time 2 http://127.0.0.1:18099/health/live && { echo "ready after ${i} polls"; break; }
  sleep 0.5
done
BASE=http://127.0.0.1:18099 bash "$P/scripts/perf/lan-explorer-probe.sh" before-quiet 25 3 "$P/.work/perf-cmp"
kill $OLDPID 2>/dev/null
sleep 3

echo
echo "=== 合并版（生产 8080）==="
BASE=http://127.0.0.1:8080 bash "$P/scripts/perf/lan-explorer-probe.sh" after-quiet 25 3 "$P/.work/perf-cmp"
echo
echo "load after: $(uptime)"
