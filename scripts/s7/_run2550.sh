set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
echo "=== 复现 counterparties 查询（含错误详情） ==="
$CH --query "
SELECT counterparty, sum(tx_count) AS tx_count
FROM (
  SELECT if(from_address = '$A', to_address, from_address) AS counterparty, count() AS tx_count
  FROM $CLICKHOUSE_DB.transactions FINAL
  WHERE chain = 'tron' AND (from_address = '$A' OR to_address = '$A')
  GROUP BY counterparty
  UNION ALL
  SELECT if(from_address = '$A', to_address, from_address) AS counterparty, count() AS tx_count
  FROM $CLICKHOUSE_DB.token_transfers FINAL
  WHERE chain = 'tron' AND (from_address = '$A' OR to_address = '$A')
  GROUP BY counterparty
) GROUP BY counterparty ORDER BY tx_count DESC LIMIT 50
FORMAT TSV" 2>&1 | head -12
echo "=== 同义查询（不带 FINAL） ==="
timeout 60 $CH --query "
SELECT counterparty, sum(tx_count) AS tx_count FROM (
  SELECT if(from_address = '$A', to_address, from_address) AS counterparty, count() AS tx_count
  FROM $CLICKHOUSE_DB.transactions
  WHERE chain = 'tron' AND (from_address = '$A' OR to_address = '$A') GROUP BY counterparty
) GROUP BY counterparty ORDER BY tx_count DESC LIMIT 5 FORMAT TSV" 2>&1 | head -8
echo "=== 该地址在 transactions 中的规模 ==="
$CH --query "select count() from $CLICKHOUSE_DB.transactions where chain='tron' and (from_address='$A' or to_address='$A')"
echo "=== CH 错误日志 ==="
sudo -n docker logs deploy-clickhouse-1 2>&1 | grep -iE 'timeout|memory limit|TOO_SLOW|exception' | tail -5 | cut -c1-250
echo SCRIPT_END
