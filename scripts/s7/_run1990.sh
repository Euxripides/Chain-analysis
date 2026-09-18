set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== system.disks ==="
$CH --query "select name, path, type from system.disks"
echo "=== try BACKUP to Disk('backups', ...) on a tiny table ==="
$CH --query "BACKUP TABLE $CLICKHOUSE_DB.transaction_traces TO Disk('backups', 's7-probe')" 2>&1 | tail -3
echo "=== backups dir ==="
sudo -n docker exec deploy-clickhouse-1 sh -c 'ls -la /var/lib/clickhouse/backups/ 2>/dev/null | head'
echo "=== drop probe backup ==="
$CH --query "DROP BACKUP Disk('backups', 's7-probe')" 2>&1 | tail -2
echo SCRIPT_END
