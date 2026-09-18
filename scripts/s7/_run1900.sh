cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== rbac tail ==="; tail -n 5 rbac/console.log 2>/dev/null
echo "=== iam tail ==="; tail -n 5 iam/console.log 2>/dev/null
echo "=== ui dir ==="; ls -la ui 2>/dev/null
echo "=== ui tail ==="; tail -n 8 ui/playwright.log 2>/dev/null
echo "=== procs ==="; ps aux | grep -E 'playwright|run-sprint7' | grep -v grep | head -3 | cut -c1-140
echo SCRIPT_END
