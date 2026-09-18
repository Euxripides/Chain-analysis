cat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/summary.txt 2>/dev/null
echo "--- procs ---"; ps aux | grep -E '[r]un-sprint7|[p]laywright' | head -3 | cut -c1-110
echo "--- qa dir ---"; ls /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ 2>/dev/null
echo SCRIPT_END
