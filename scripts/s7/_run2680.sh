D=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests
grep -n 'FAIL' $D/run.log | head -6
echo "--- tail ---"; tail -6 $D/run.log
echo SCRIPT_END
