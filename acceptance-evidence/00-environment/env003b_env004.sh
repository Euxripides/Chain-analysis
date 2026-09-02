#!/usr/bin/env bash
set +e
api_pid=$(pgrep -f 'bin/api' | head -1)
echo "=== ANKR_MODE value (non-secret) ==="
tr '\0' '\n' < /proc/$api_pid/environ | grep -E '^ANKR_MODE='
echo "=== RPC-related env names ==="
tr '\0' '\n' < /proc/$api_pid/environ | grep -E '^RPC|^ANKR' | sed -E 's/=.*/=[SET]/'
echo "=== .env files on project (names only, redact values) ==="
ls -la /home/euripides/projects/blockchain-platform/ | head -30
find /home/euripides/projects/blockchain-platform -maxdepth 2 -name '.env*' -o -maxdepth 2 -name '*.env' 2>/dev/null | head
echo "=== deploy dir ==="
ls /home/euripides/projects/blockchain-platform/deploy/ 2>/dev/null | head -30
echo "=== ENV-004 health live ==="
curl -sS -w '\nHTTP_CODE=%{http_code}\n' http://127.0.0.1:8080/health/live
echo "=== ENV-004 health ready ==="
curl -sS -w '\nHTTP_CODE=%{http_code}\n' http://127.0.0.1:8080/health/ready
