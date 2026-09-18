cat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/summary.txt 2>/dev/null
echo "--- procs ---"; ps aux | grep -E 'run-sprint' | grep -v grep | head -3
echo SCRIPT_END
