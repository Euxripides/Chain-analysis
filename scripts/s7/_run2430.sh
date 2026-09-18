cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
bash scripts/backup/kill9-drill.sh 2>&1 | tail -n 60
echo "=== KILL9 DONE ==="
