cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
BK=20260912T171120Z
DB=$CLICKHOUSE_DB
echo "=== live baseline ==="
$CH --query "select count() as rows_live, max(timestamp) as max_ts_live from $DB.address_activity"
$CH --query "select count() as rows_live_final from $DB.address_activity FINAL" 2>&1 | head -2
$CH --query "select count() as tt_live, max(timestamp) as tt_max from $DB.token_transfers"
$CH --query "select count() as tt_live_final from $DB.token_transfers FINAL" 2>&1 | head -2
echo "=== restore single table from backup ==="
T0=$(date +%s)
$CH --query "RESTORE TABLE $DB.address_activity AS ${DB}_drchk.address_activity FROM File('$BK')" 2>&1 | tail -2
T1=$(date +%s); echo "restore_table_seconds=$((T1-T0))"
$CH --query "select count() as rows_restored, max(timestamp) as max_ts_restored from ${DB}_drchk.address_activity"
$CH --query "select count() as rows_restored_final from ${DB}_drchk.address_activity FINAL" 2>&1 | head -2
echo "=== cleanup ==="
$CH --query "DROP DATABASE IF EXISTS ${DB}_drchk"
echo SCRIPT_END