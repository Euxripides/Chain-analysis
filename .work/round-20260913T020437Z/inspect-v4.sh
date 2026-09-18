#!/usr/bin/env bash
set -uo pipefail
EV4=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v4-20260913T023455Z
cd "$EV4" || exit 1
echo "=== files ==="; ls -la | head -20
echo "report_json_exists=$( [ -f playwright-report.json ] && echo yes || echo no )"
echo "=== results ==="
grep -nE '✓|✘|passed|failed|did not run|interrupted' playwright.log | head -20
echo "=== errors ==="
grep -n "Error:" playwright.log | head -8
echo "=== first failure context ==="
awk '/^  1\) /{f=1} f' playwright.log | head -30
echo "=== p3/p5 evidence ==="
cat p4-db-crosscheck.txt p5-retry-evidence.txt 2>/dev/null
echo "=== r9-07 evidence ==="
python3 -c "
import json
d=json.load(open('uc-desktop/r9-07-evidence.json'))
for c in d['cases']: print(c['file'], '|', c['reason'][:110], '| validate', c['validateCalls'], 'create', c['createCalls'])
" 2>&1 | head -12
