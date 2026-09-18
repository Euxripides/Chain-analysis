cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
bash scripts/release/observe-release.sh 2>&1 | tail -n 60
echo SCRIPT_END
