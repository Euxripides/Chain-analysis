#!/usr/bin/env bash
set -uo pipefail
echo "== worker health now =="
echo "unit=$(systemctl --user is-active blockchain-worker) restarts=$(systemctl --user show blockchain-worker -p NRestarts --value) pid=$(pgrep -f 'backend/bin/worker$' | head -1)"
printf '8081 /health/live  = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/health/live)"
printf '8081 /metrics      = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/metrics)"
echo "== worker log =="
journalctl --user -u blockchain-worker --since "-8 min" --no-pager | grep -vE 'systemd\[' | tail -8
echo "== worker binary sha on disk & in memory =="
sha256sum /home/euripides/projects/blockchain-platform/backend/bin/worker
sha256sum /proc/$(pgrep -f 'backend/bin/worker$' | head -1)/exe
echo "== api =="
echo "api ready=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/health/ready) tls=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)"
