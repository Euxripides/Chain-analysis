set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== 列类型 ==="
$CH --query "select table, name, type from system.columns where database='$CLICKHOUSE_DB' and name in ('amount','value_raw','raw_amount') order by table, name FORMAT TSV"
echo "=== token_transfers.amount 的 variant 分布（近 100 万行样本） ==="
$CH --query "select variantType(amount) vt, count() c from $CLICKHOUSE_DB.token_transfers where chain='tron' limit 1000000 FORMAT TSV" 2>&1 | head -5
$CH --query "select count() c, variantType(amount) vt from (select amount from $CLICKHOUSE_DB.token_transfers where chain='tron' limit 200000) group by vt FORMAT TSV" 2>&1 | head -5
echo "=== 样本值 ==="
$CH --query "select amount, toTypeName(amount), variantElement(amount,'Int256') as as_int, variantElement(amount,'Decimal(76, 38)') as as_dec from $CLICKHOUSE_DB.token_transfers where chain='tron' limit 3 FORMAT Vertical" 2>&1 | head -20
echo SCRIPT_END
