echo "=== nginx proxy headers for /api ==="
sudo -n sed -n '50,110p' /etc/nginx/sites-enabled/blockchain-platform.conf
echo "=== current cookie env ==="
grep -E '^AUTH_COOKIE' /home/euripides/projects/blockchain-platform/deploy/.env
echo SCRIPT_END
