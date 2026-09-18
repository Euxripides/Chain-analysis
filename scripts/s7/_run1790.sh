cat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/summary.txt 2>/dev/null
echo "--- dirs ---"
ls /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ 2>/dev/null
echo "--- running ---"
ps aux | grep -E 'run-sprint|playwright' | grep -v grep | awk '{print $NF}' | head -3
echo SCRIPT_END
