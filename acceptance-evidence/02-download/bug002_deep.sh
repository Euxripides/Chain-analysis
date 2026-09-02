#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== CH row for 0x4b61b535 ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, log_index, from_address, to_address, token_address, source_provider, source_ingested_at FROM $DB.token_transfers FINAL WHERE tx_hash='0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a' AND from_address='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' FORMAT Vertical"
echo "=== PEER3 coverage ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count, committed_at FROM dataset_coverage WHERE subject='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' ORDER BY dataset, from_block;"
echo "=== DL-20260831-0019 shard detail ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, status, downloaded_rows, page_no, raw_object FROM download_shards WHERE job_id=(SELECT id FROM download_jobs WHERE job_no='DL-20260831-0019');"
