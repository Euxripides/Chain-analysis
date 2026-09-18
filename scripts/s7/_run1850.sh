cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== playwright failure detail ==="
sed -n '/1) /,/5) /p' playwright.log | head -60
echo SCRIPT_END
