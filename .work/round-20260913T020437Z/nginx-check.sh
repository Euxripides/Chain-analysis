#!/usr/bin/env bash
set -uo pipefail
echo "=== nginx configs ==="
ls -la /etc/nginx/sites-enabled/ 2>/dev/null; ls -la /etc/nginx/conf.d/ 2>/dev/null | head
echo "--- roots ---"
grep -rn "root\|location\|proxy_pass" /etc/nginx/sites-enabled/ 2>/dev/null | head -40
echo "=== dist dirs ==="
ls -la /home/euripides/projects/blockchain-platform/frontend/dist 2>/dev/null | head -12
echo "=== user-console dist ==="
find /home/euripides/projects/blockchain-platform/frontend -maxdepth 3 -name "dist" -type d 2>/dev/null | head
echo "=== served html (head) ==="
curl -sk https://127.0.0.1:8448/app-v2/ -o /tmp/r9-appv2.html -w "%{http_code}\n"
head -c 600 /tmp/r9-appv2.html; echo
echo "=== which bundle does app-v2 index reference ==="
grep -o 'src="[^"]*"' /tmp/r9-appv2.html | head
echo "=== done ==="
