cat ~/.config/systemd/user/blockchain-vite.service | grep -E 'WorkingDirectory|ExecStart'
echo "--- what serves 3000 ---"
ls /home/euripides/projects/blockchain-platform/frontend/ | head -20
echo "--- legacy frontend dir? ---"
find /home/euripides/projects/blockchain-platform/frontend -maxdepth 2 -name 'index.html' 2>/dev/null | head
echo SCRIPT_END
