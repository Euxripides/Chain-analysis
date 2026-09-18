cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7 2>/dev/null || echo "no sprint7 dir"
ls -la qa 2>/dev/null | head -20
echo "=== summary.txt ==="; cat qa/summary.txt 2>/dev/null
echo "=== running procs ==="; ps aux | grep -E 'run-sprint|playwright' | grep -v grep | head -5
echo "=== db evidence ==="; ls -la /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/db/ 2>/dev/null