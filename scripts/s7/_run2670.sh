D=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests
ls -t $D/*.log | head -3
echo "=== 最新一轮的 FAIL ==="
latest=$(ls -t $D/run.*.log 2>/dev/null | head -1)
echo "latest=$latest"
grep -n 'FAIL' "$latest" | head -6
echo "=== result.json ==="
cat $D/result.json
echo SCRIPT_END
