cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui
grep -E '^  [0-9]+\) ' playwright.log | head -10
echo "=== 错误摘要 ==="
sed -n '/1) /,/^  3) /p' playwright.log | grep -E 'Error|expect|at /home' | head -14
echo SCRIPT_END
