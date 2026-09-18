set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== A) TO File('s7-probe') ==="
$CH --query "BACKUP TABLE $CLICKHOUSE_DB.transaction_traces TO File('s7-probe')" 2>&1 | tail -2
echo "=== B) TO Disk('default','s7-probe2') ==="
$CH --query "BACKUP TABLE $CLICKHOUSE_DB.transaction_traces TO Disk('default', 's7-probe2')" 2>&1 | tail -2
echo "=== files ==="
sudo -n docker exec deploy-clickhouse-1 sh -c 'ls -la /var/lib/clickhouse/backups/ 2>/dev/null | head -6; ls -la /var/lib/clickhouse/ | grep -i probe'
echo SCRIPT_END
