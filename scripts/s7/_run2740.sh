cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== sprint5 API FAIL ==="; grep -n 'FAIL' sprint5/console.log | head -5
echo "=== UI failures ==="; grep -E '^  [0-9]+\) ' ui/playwright.log | head -6
echo "=== UI error snippet ==="; sed -n '/1) /,/2) /p' ui/playwright.log | grep -E 'Error|expect|Received|Expected' | head -10
echo SCRIPT_END
