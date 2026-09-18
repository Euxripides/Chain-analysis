cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
echo "=== 系统静默后重跑：security + sprint5 + UI ==="
echo "--- security ---"
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh 2>&1 | tail -n 3
echo "--- sprint5 ---"
env EVIDENCE_DIR="$PWD/acceptance-evidence/dual-system-v1/tests/sprint7/qa/sprint5" PROJECT="$PWD" bash acceptance-evidence/dual-system-v1/tests/run-sprint5-e2e.sh > /tmp/s5.log 2>&1; echo "rc=$?"; tail -n 2 /tmp/s5.log
cp "$PWD/acceptance-evidence/dual-system-v1/tests/sprint7/qa/sprint5/result.json" "$PWD/acceptance-evidence/dual-system-v1/tests/sprint7/qa/sprint5.result.json" 2>/dev/null
echo "--- UI ---"
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-ui.sh 2>&1 | tail -n 12
echo "=== RERUN DONE ==="
