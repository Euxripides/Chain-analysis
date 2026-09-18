cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
export SPECS="tests/dual-system-quality.spec.ts"
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-ui.sh 2>&1 | tail -n 20
echo SCRIPT_END
