set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
echo "=== ch dr db ==="
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD -q "select name from system.databases where name like '%dr_%'"
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD -q "select database, count() from system.tables where database like '%dr_%' group by 1"
echo "=== restore process ==="
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD -q "select query_kind, elapsed from system.processes where query like '%RESTORE%'" 2>/dev/null | head -3
echo "=== drill script alive? ==="; pgrep -af 'restore-drill' | head -2
echo SCRIPT_END
