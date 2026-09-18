#!/usr/bin/env bash
# S7-WAL-01 一键部署：备份 → mv 原子替换 → 分单元重启 → 健康检查 → AFTER 采样。
set -uo pipefail
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
P=/home/euripides/projects/blockchain-platform
BK="$HOME/deploy-backups/pre-wal-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BK"

echo "===== 1) 备份 ====="
cp -av "$P/backend/bin/api"    "$BK/api.frozen-before"    >/dev/null
cp -av "$P/backend/bin/worker" "$BK/worker.frozen-before" >/dev/null
sha256sum "$BK"/api.frozen-before "$BK"/worker.frozen-before
echo "new api    = $(sha256sum "$P/backend/bin/api.new" | awk '{print $1}')"
echo "new worker = $(sha256sum "$P/backend/bin/worker.new" | awk '{print $1}')"
echo "backup dir = $BK"

echo
echo "===== 2) 原子替换（mv 避免 Text file busy）====="
mv -v "$P/backend/bin/api.new"    "$P/backend/bin/api"
mv -v "$P/backend/bin/worker.new" "$P/backend/bin/worker"

echo
echo "===== 3) 分别只重启对应单元 ====="
systemctl --user restart blockchain-api
for i in $(seq 1 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8080/health/live)" = "200" ] && { echo "api live after ${i}s"; break; }
  sleep 1
done
echo "api  /health/ready = $(curl -s -o /dev/null -w '%{http_code}' --max-time 30 http://127.0.0.1:8080/health/ready)"
systemctl --user stop blockchain-worker
sleep 2
systemctl --user start blockchain-worker
for i in $(seq 1 30); do
  [ "$(systemctl --user is-active blockchain-worker)" = "active" ] && { echo "worker active after ${i}s"; break; }
  sleep 1
done
sleep 3
echo "worker unit = $(systemctl --user is-active blockchain-worker) restarts=$(systemctl --user show blockchain-worker -p NRestarts --value)"
echo "worker metrics 8081 = $(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/health/live)"
APIPID=$(pgrep -f 'backend/bin/api$' | head -1)
echo "running api pid=$APIPID sha=$(sha256sum /proc/$APIPID/exe | awk '{print $1}')"
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
echo "tls = $(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)"

echo
echo "===== 4) AFTER 采样 ====="
bash /tmp/measure.sh AFTER 90
