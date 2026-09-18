cd /home/euripides/projects/blockchain-platform
mkdir -p acceptance-evidence/dual-system-v1/tests/sprint7
export XDG_RUNTIME_DIR=/run/user/$(id -u)
setsid nohup bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh > /tmp/s7qa.out 2>&1 < /dev/null &
echo "launched pid=$!"
sleep 8
echo "=== early output ==="; tail -5 /tmp/s7qa.out