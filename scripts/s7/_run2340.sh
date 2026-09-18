cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
bash scripts/backup/restore-drill.sh 2>&1 | tail -n 80
echo "=== RESTORE DRILL DONE ==="
