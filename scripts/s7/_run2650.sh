cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
echo "=== FROZEN BUILD ==="; sha256sum backend/bin/api backend/bin/worker backend/bin/migrate
echo "=== 1) 安全负向套件（修复后回归） ==="
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh 2>&1 | tail -n 12
echo "=== 2) §27 全量套件 ==="
EPOCH=$(date +%s); export S7_EPOCH=$EPOCH
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh 2>&1 | tail -n 45
echo "=== 3) UI 套件 ==="
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-ui.sh 2>&1 | tail -n 14
echo "=== FROZEN REGRESSION DONE ==="
