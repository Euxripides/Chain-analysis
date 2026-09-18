cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
bash scripts/release/observe-release.sh 2>&1 | tail -n 55
echo "=== audit coverage log ==="; head -20 acceptance-evidence/dual-system-v1/tests/sprint7/release/audit-coverage.log 2>/dev/null
echo SCRIPT_END
