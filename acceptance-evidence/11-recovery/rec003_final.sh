#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
A=0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae
echo "=== job 61c6e511 final ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, error FROM download_jobs WHERE id='61c6e511-b9c2-4452-905d-2dcb5f85cea9';"
echo "=== shard final ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, downloaded_rows, page_no, retries, raw_object FROM download_shards WHERE job_id='61c6e511-b9c2-4452-905d-2dcb5f85cea9';"
echo "=== CH physical vs FINAL for downloaded range ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS phys FROM $DB.logs WHERE chain='bsc' AND lower(address)='$A' AND block_number BETWEEN 60001201 AND 60060000"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS fin, uniqExact(tx_hash, log_index) AS grain FROM $DB.logs FINAL WHERE chain='bsc' AND lower(address)='$A' AND block_number BETWEEN 60001201 AND 60060000"
echo "=== coverage committed ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT from_block, to_block, row_count FROM dataset_coverage WHERE subject='$A' AND dataset='logs' ORDER BY from_block;"
