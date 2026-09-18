cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests
echo "=== sprint7/qa/sprint4/ids.env ==="; cat sprint7/qa/sprint4/ids.env 2>/dev/null
echo "=== sprint7/qa/sprint5/ids.env ==="; cat sprint7/qa/sprint5/ids.env 2>/dev/null
echo "=== rbac required env ==="; grep -n '需要\|:-$\|FIXTURE_' run-rbac-e2e.sh | head -20
echo "=== iam SMOKE-09 ==="; grep -n -B3 -A6 'SMOKE-09' run-iam-e2e.sh | head -30
echo SCRIPT_END
