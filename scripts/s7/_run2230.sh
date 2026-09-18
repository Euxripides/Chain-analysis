set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD"
echo "=== 最大的地址（按 activity 行数） ==="
$CH --query "select chain, address, count() c from address_activity group by chain, address order by c desc limit 5 FORMAT TSV"
echo "=== transactions 最大的 from ==="
$CH --query "select chain, from_address, count() c from transactions group by chain, from_address order by c desc limit 3 FORMAT TSV"
echo "=== dataset_coverage（已有覆盖） ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select chain, subject_type, subject, dataset, status, from_block, to_block, rows_written from dataset_coverage order by rows_written desc nulls last limit 8"
echo SCRIPT_END
