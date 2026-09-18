echo "=== user units ==="
systemctl --user list-units --type=service --all --no-pager --plain | grep blockchain
echo "=== unit files ==="
ls -la ~/.config/systemd/user/ | grep -i blockchain
echo "--- api unit ---"; cat ~/.config/systemd/user/blockchain-api.service
echo "--- worker unit ---"; cat ~/.config/systemd/user/blockchain-worker.service
echo "=== linger ==="; loginctl show-user euripides | grep -i linger