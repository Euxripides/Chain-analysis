set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== address_activity 列 ==="
$CH --query "select name, type from system.columns where database='$CLICKHOUSE_DB' and table='address_activity' order by position FORMAT TSV"
echo "=== 用 address_activity 直接聚合 counterparties（单表、无 UNION） ==="
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
/usr/bin/time -f 'elapsed=%e s' $CH --query "
select counterparty, count() as tx_count, toString(sumIf(amount, direction='in')) as in_raw, toString(sumIf(amount, direction='out')) as out_raw
from $CLICKHOUSE_DB.address_activity
where chain='tron' and address='$A'
group by counterparty order by tx_count desc limit 5 FORMAT TSV" 2>&1 | head -10
echo SCRIPT_END
