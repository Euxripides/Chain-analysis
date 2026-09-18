set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
echo "=== 手工执行 counterparties 查询耗时 ==="
/usr/bin/time -f 'elapsed=%e s maxrss=%M KB' $CH --query "
SELECT counterparty, sum(tx_count) AS tx_count FROM (
  SELECT if(from_address = '$A', to_address, from_address) AS counterparty, count() AS tx_count
  FROM $CLICKHOUSE_DB.transactions FINAL WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY counterparty
  UNION ALL
  SELECT if(from_address = '$A', to_address, from_address) AS counterparty, count() AS tx_count
  FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE chain='tron' AND (from_address='$A' OR to_address='$A') GROUP BY counterparty
) GROUP BY counterparty ORDER BY tx_count DESC LIMIT 50 FORMAT TSV" > /dev/null 2>/tmp/t.txt; cat /tmp/t.txt
echo "=== API 侧超时配置 ==="
grep -nE 'WriteTimeout|ReadTimeout|IdleTimeout|HandlerTimeout|timeout' /home/euripides/projects/blockchain-platform/backend/internal/api/server.go | head -8
grep -rnE 'http.Server\{' -A6 /home/euripides/projects/blockchain-platform/backend/cmd/api/main.go | head -14
echo "=== CH 单查询默认限制 ==="
$CH --query "select name, value from system.settings where name in ('max_execution_time','max_memory_usage','max_rows_to_read','max_bytes_to_read')"
echo SCRIPT_END
