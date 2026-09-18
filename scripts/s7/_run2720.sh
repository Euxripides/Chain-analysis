cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
echo "=== FROZEN BUILD (final) ==="; sha256sum backend/bin/api backend/bin/worker
rm -rf acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo "=== 1) 安全负向套件 ==="
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh 2>&1 | tail -n 5
echo "=== 2) §27 全量（含 UI） ==="
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh 2>&1 | tail -n 55
echo "=== FINAL REGRESSION DONE ==="
