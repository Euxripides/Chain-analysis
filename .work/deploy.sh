#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BK="$HOME/deploy-backups/pre-perf-20260916T174927Z"
echo "== pre-deploy backup =="
cp -av "$P/backend/bin/api" "$BK/api.pre-deploy-$TS"
sha256sum "$BK/api.pre-deploy-$TS"
echo "== atomic swap (mv avoids ETXTBSY on a running binary) =="
ls -la "$P/backend/bin/api" "$P/backend/bin/api.new"
mv -v "$P/backend/bin/api.new" "$P/backend/bin/api"
echo "new on-disk sha: $(sha256sum "$P/backend/bin/api" | awk '{print $1}')"
echo "== restart =="
systemctl --user restart blockchain-api
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8080/health/live || true)
  [ "$code" = "200" ] && { echo "health/live=200 after ${i} polls (~${i}s)"; break; }
  sleep 1
done
printf 'health/ready=%s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 http://127.0.0.1:8080/health/ready)"
PID=$(pgrep -f 'backend/bin/api$' | head -1)
echo "running pid=$PID"
sha256sum /proc/$PID/exe
echo "== units =="
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
