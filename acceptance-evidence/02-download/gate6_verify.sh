#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== rows ingested for PEER3 at block 114147137 ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, log_index, from_address, to_address, token_address, amount_raw, source_ingested_at FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE chain='bsc' AND from_address='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' AND block_number=114147137 FORMAT Vertical"
echo "=== coverage committed ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count FROM dataset_coverage WHERE subject='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' ORDER BY from_block;"
echo "=== raw parts ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT raw_object, downloaded_rows, page_no FROM download_shards WHERE job_id='f0b43c1d-d922-4af5-bbab-7b52a6728d88';"
