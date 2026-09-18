echo "=== oom / kills ==="; sudo -n dmesg -T 2>/dev/null | tail -15
echo "=== services ==="; export XDG_RUNTIME_DIR=/run/user/$(id -u); systemctl --user is-active blockchain-api blockchain-worker
echo "=== sprint4 result.json location ==="
grep -n 'result.json\|EVIDENCE_DIR' /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/run-sprint4-e2e.sh | head -12
echo "=== sprint5 result.json location ==="
grep -n 'result.json\|EVIDENCE_DIR' /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/run-sprint5-e2e.sh | head -12
echo "=== memory ==="; free -m | head -2
echo "=== worker rss ==="; ps -o pid,rss,args -p $(pgrep -f 'bin/worker' | head -1) 2>/dev/null