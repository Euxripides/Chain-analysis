cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
export CONC=8 DUR=20s
bash scripts/perf/run-perf-suite.sh 2>&1 | tail -n 60
echo "=== PERF DONE ==="
