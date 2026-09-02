#!/usr/bin/env bash
set +e
echo "=== dev-restart.log ==="
ls -la /tmp/dev-restart.log 2>&1
tail -n 20 /tmp/dev-restart.log 2>&1
echo "=== processes ==="
pgrep -af 'bin/api'
pgrep -af 'bin/worker'
echo "=== pid files ==="
ls -la /tmp/blockchain-platform-*.pid 2>&1
echo "=== health ==="
curl -sS -m 3 http://127.0.0.1:8080/health/ready
echo ""
curl -sS -m 3 -o /dev/null -w 'ui:%{http_code}' http://127.0.0.1:3000/
echo ""
