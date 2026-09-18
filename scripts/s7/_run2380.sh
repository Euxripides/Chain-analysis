cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
rm -f acceptance-evidence/dual-system-v1/sprint7/dr/restore-drill-20260912T173634Z.md
bash scripts/backup/restore-drill.sh 2>&1 | tail -n 90
echo "=== RESTORE DRILL DONE ==="
