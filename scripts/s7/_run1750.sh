cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh 2>&1 | tail -n 60
