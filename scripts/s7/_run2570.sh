set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== query_log 中 counterparty 查询的失败记录 ==="
$CH --query "
select event_time, type, exception_code, substring(exception,1,300) exc, query_duration_ms, substring(query,1,120) q
from system.query_log
where event_time > now() - interval 2 hour and query like '%counterparty%' and exception != ''
order by event_time desc limit 5 FORMAT Vertical" 2>&1 | head -40
echo "=== 最近失败的 CH 查询（任意） ==="
$CH --query "
select event_time, exception_code, substring(exception,1,200) exc, query_duration_ms
from system.query_log
where event_time > now() - interval 1 hour and exception != ''
order by event_time desc limit 6 FORMAT TSV" 2>&1 | head -10
echo SCRIPT_END
