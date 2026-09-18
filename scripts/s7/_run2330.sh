date -u +%H:%M:%S
D=/home/euripides/backups/blockchain-platform/20260912T171120Z
stat -c '%s %y' $D/clickhouse.tar.gz
sleep 15
stat -c '%s %y' $D/clickhouse.tar.gz
echo "--- tar state ---"; cat /proc/2852631/status 2>/dev/null | grep -E 'State|VmRSS'
echo "--- tar fd ---"; sudo -n ls -l /proc/2852631/fd 2>/dev/null | head -5
echo "--- iowait ---"; cat /proc/loadavg
echo SCRIPT_END
