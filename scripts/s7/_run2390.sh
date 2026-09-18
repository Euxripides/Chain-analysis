echo "=== ch restore progress ==="
sudo -n docker exec deploy-clickhouse-1 clickhouse-client -q "select query, elapsed, formatReadableSize(memory_usage) from system.processes where query like '%RESTORE%' limit 3" 2>/dev/null
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD -q "select name from system.databases where name like '%dr_%'" 2>/dev/null
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD -q "select count() from system.tables where database like '%dr_%'" 2>/dev/null
echo "=== pg restore db ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d postgres -Atc "select datname from pg_database where datname like '%dr_%'"
echo SCRIPT_END
