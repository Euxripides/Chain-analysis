#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
A=0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae
echo "=== REC-004 range dedup ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS phys FROM $DB.logs WHERE chain='bsc' AND lower(address)='$A' AND block_number BETWEEN 60100001 AND 60120000"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(tx_hash,log_index) AS grain FROM $DB.logs FINAL WHERE chain='bsc' AND lower(address)='$A' AND block_number BETWEEN 60100001 AND 60120000"
echo "=== coverage after REC-004 ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT from_block, to_block, row_count FROM dataset_coverage WHERE subject='$A' AND dataset='logs' ORDER BY from_block;"
