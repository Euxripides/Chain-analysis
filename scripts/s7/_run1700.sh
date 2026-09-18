cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
rm -rf acceptance-evidence/dual-system-v1/tests/sprint7/qa
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh 2>&1 | tail -60