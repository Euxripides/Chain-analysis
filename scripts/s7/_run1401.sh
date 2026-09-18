cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
ls -la
echo "=== sprint4 console tail ==="; tail -25 sprint4/console.log 2>/dev/null
echo "=== sprint5 console tail ==="; tail -15 sprint5/console.log 2>/dev/null
echo "=== s7qa.out full ==="; cat /tmp/s7qa.out
echo "=== any runner procs ==="; ps aux | grep -E 'run-sprint|playwright' | grep -v grep | head