#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== cancelled job shard state ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, downloaded_rows, page_no, page_token, error FROM download_shards WHERE job_id='f1375560-222b-4825-93a7-a5c47ab6f6b8';"
echo "=== coverage for range 60065001-60100000 (should NOT exist) ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT from_block, to_block, row_count FROM dataset_coverage WHERE subject='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND dataset='logs' AND from_block >= 60065000 ORDER BY from_block;"
echo "=== ingested rows kept? (completed data preserved) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM blockchain.logs FINAL WHERE chain='bsc' AND lower(address)='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND block_number BETWEEN 60065001 AND 60100000"
