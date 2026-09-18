set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== query_log 中最近的 address_activity 聚合查询 ==="
$CH --query "
select event_time, type, read_rows, result_rows, query_duration_ms, exception_code, substring(query,1,200) q, substring(exception,1,150) e
from system.query_log
where event_time > now() - interval 10 minute and query like '%address_activity%'
order by event_time desc limit 4 FORMAT Vertical" 2>&1 | head -40
echo SCRIPT_END
