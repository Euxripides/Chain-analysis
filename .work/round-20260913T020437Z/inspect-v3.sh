#!/usr/bin/env bash
set -uo pipefail
EV3=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z/e2e-v3-20260913T023140Z
cd "$EV3" || exit 1
echo "=== result lines ==="
grep -nE '✓|✘|passed|failed|interrupted|did not run' playwright.log | head -30
echo "=== first errors ==="
grep -n "Error:" playwright.log | head -12
echo "=== context around first failure ==="
awk '/^  [0-9]+\) /{c++} c==1' playwright.log | head -45
echo "=== report shape ==="
python3 - <<'PY'
import json
d=json.load(open('playwright-report.json'))
print('top keys', list(d.keys()))
print('suites', len(d.get('suites',[])))
def walk(s, depth=0, acc=None):
    if acc is None: acc=[]
    for sp in s.get('specs',[]):
        for t in sp.get('tests',[]):
            acc.append((sp.get('title','')[:12], t.get('projectName'), t.get('status')))
    for c in s.get('suites',[]):
        walk(c, depth+1, acc)
    return acc
rows = []
for s in d.get('suites',[]):
    rows = walk(s, 0, rows)
print('rows', len(rows))
for r in rows[:16]: print(r)
PY
