echo "=== api.log tail ==="; tail -n 20 /tmp/api.log
echo "=== worker.log tail ==="; tail -n 12 /tmp/worker.log
echo "=== systemctl status ==="
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user status blockchain-api --no-pager -n 6 2>&1 | head -20
echo SCRIPT_END
