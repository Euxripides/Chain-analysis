#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P" || exit 90
echo "=== runner run-r7-jobs-scope-ui.sh ==="
cat scripts/s7/run-r7-jobs-scope-ui.sh
echo "=== cred env KEYS only ==="
sed -E 's/=.*/=<redacted>/' /home/euripides/.blockchain-fixture-creds.env
echo "=== r7 234331 tree ==="
find acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r7-ui-20260912T234331Z -maxdepth 2 | sort
echo "=== r7 desktop evidence (api_calls + console) ==="
python3 - <<'PY'
import json,glob
for f in sorted(glob.glob('acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r7-ui-20260912T234331Z/*/r7-ui-evidence.json')):
    d=json.load(open(f))
    print('FILE',f)
    print(' keys',sorted(d.keys()))
    print(' project',d.get('project'),'viewport',d.get('viewport'),'dom_rows',d.get('dom_rows'),'session',d.get('session_status'))
    print(' console_errors',json.dumps(d.get('console_errors'),ensure_ascii=False)[:1200])
    for c in (d.get('api_calls') or []):
        print('  CALL',c.get('status'),c.get('url'),'count',c.get('count'),'rid',c.get('requestId'),'tid',c.get('traceId'))
PY
echo "=== any json with 404 near r7 dirs ==="
grep -rl '404' acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r7-ui-20260912T234331Z 2>/dev/null | head
echo "=== done ==="
