#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== shard metadata ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT page_no, page_size, page_token, downloaded_rows, page_state FROM download_shards WHERE job_id='3e031e80-1622-4024-b50e-39769e2adc0c';"
echo "=== ingested rows block distribution ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT min(block_number), max(block_number), count() FROM $DB.logs FINAL WHERE chain='bsc' AND address='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND block_number >= 60001001 AND block_number <= 60001200"
echo "=== coverage committed ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT from_block, to_block, row_count FROM dataset_coverage WHERE subject='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND dataset='logs' ORDER BY from_block;"
