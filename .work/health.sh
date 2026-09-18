#!/usr/bin/env bash
set -uo pipefail
echo "== units =="
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
echo "== health =="
printf 'api  8080 /health/live  = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8080/health/live)"
printf 'api  8080 /health/ready = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/health/ready)"
printf 'nginx 8088 /health/ready = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8088/health/ready)"
printf 'tls  https://aurore.online/health/ready = %s (time %ss)\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)" "$(curl -s -o /dev/null -w '%{time_total}' --max-time 30 https://aurore.online/health/ready)"
echo "== running api sha =="
sha256sum /proc/$(pgrep -f 'backend/bin/api$' | head -1)/exe 2>/dev/null || echo "(cannot read)"
