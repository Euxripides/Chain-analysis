cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== ui progress ==="; grep -c '✓\|✘' ui/playwright.log 2>/dev/null; tail -n 4 ui/playwright.log 2>/dev/null
echo "=== failures so far ==="; grep -c '✘' ui/playwright.log 2>/dev/null
echo SCRIPT_END
