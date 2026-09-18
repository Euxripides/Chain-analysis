cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== rbac console tail ==="; tail -n 20 rbac/console.log 2>/dev/null
echo "=== iam console tail ==="; tail -n 30 iam/console.log 2>/dev/null
echo "=== iam FAIL lines ==="; grep -n "FAIL" iam/console.log | head -10
echo "=== playwright head ==="; head -n 40 playwright.log 2>/dev/null
echo "=== playwright errors ==="; grep -n -m 20 -E "Error|error:|failed" playwright.log | head -20
echo SCRIPT_END
