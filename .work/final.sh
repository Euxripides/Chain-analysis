#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
EV="$P/acceptance-evidence/dual-system-v1/sprint7/perf/perf-optimization-20260917T1840Z.md"
echo "== evidence file =="
ls -la "$EV"; sha256sum "$EV"
echo
echo "== perf scripts =="
ls -la "$P/scripts/perf/"
echo
echo "== final unit state =="
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
echo
echo "== final health =="
printf 'api  8080 /health/ready     = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/health/ready)"
printf 'nginx 8088 /health/ready    = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8088/health/ready)"
printf 'tls https://aurore.online/health/ready = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)"
echo
echo "== final binaries =="
APIPID=$(pgrep -f 'backend/bin/api$' | head -1)
printf 'api    running pid=%s sha=%s\n' "$APIPID" "$(sha256sum /proc/$APIPID/exe | awk '{print $1}')"
printf 'api    on disk   sha=%s\n' "$(sha256sum "$P/backend/bin/api" | awk '{print $1}')"
printf 'worker on disk   sha=%s restarts=%s\n' "$(sha256sum "$P/backend/bin/worker" | awk '{print $1}')" "$(systemctl --user show blockchain-worker -p NRestarts --value)"
echo
echo "== backups =="
ls -la "$HOME/deploy-backups/pre-perf-20260916T174927Z/"
