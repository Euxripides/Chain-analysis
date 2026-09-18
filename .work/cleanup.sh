#!/usr/bin/env bash
set -uo pipefail
echo "== listeners =="
ss -ltnp 2>/dev/null | grep -E ':(8080|8081|18099)' || echo "(none)"
echo "--- app processes ---"
pgrep -a -f 'backend/bin/(api|worker)' || echo "(none)"
echo "--- bin dir api/worker artifacts ---"
ls -la /home/euripides/projects/blockchain-platform/backend/bin/ | grep -E ' (api|worker)(\.|$)' || echo "(none)"
echo "--- 18099 ---"
curl -s -o /dev/null -w '18099=%{http_code}\n' --max-time 3 http://127.0.0.1:18099/health/live || echo "18099 closed (good)"
echo "--- load ---"; uptime
