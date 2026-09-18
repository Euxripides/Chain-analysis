#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
echo "== units =="
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
echo "== api =="
APIPID=$(pgrep -f 'backend/bin/api$' | head -1)
printf 'pid=%s ready=%s live=%s disk_sha=%s\n' "$APIPID" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/health/ready)" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8080/health/live)" \
  "$(sha256sum $P/backend/bin/api | awk '{print $1}')"
printf 'mem_sha=%s\n' "$(sha256sum /proc/$APIPID/exe | awk '{print $1}')"
echo "== worker =="
WPID=$(pgrep -f 'backend/bin/worker$' | head -1)
printf 'pid=%s unit=%s restarts=%s 8081_live=%s 8081_metrics=%s disk_sha=%s\n' "$WPID" \
  "$(systemctl --user is-active blockchain-worker)" "$(systemctl --user show blockchain-worker -p NRestarts --value)" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/health/live)" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/metrics)" \
  "$(sha256sum $P/backend/bin/worker | awk '{print $1}')"
printf 'mem_sha=%s\n' "$(sha256sum /proc/$WPID/exe | awk '{print $1}')"
echo "== entries =="
printf 'nginx 8088 ready=%s  tls=%s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8088/health/ready)" "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)"
echo "== explorer 端点未回归（快速抽查）=="
bash "$P/scripts/perf/lan-explorer-probe.sh" postwal 3 1 "$P/.work/perf-postwal" 2>&1 | tail -6
