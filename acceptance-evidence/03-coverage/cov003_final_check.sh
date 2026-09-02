#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
P=0x680849ef321ea46f3038665134c866bf55b5a6ae
echo "=== CH physical rows ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $CLICKHOUSE_DB.token_transfers WHERE chain='bsc' AND block_number=115394241 AND (from_address='$P' OR to_address='$P')"
echo "=== CH FINAL rows ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE chain='bsc' AND block_number=115394241 AND (from_address='$P' OR to_address='$P')"
echo "=== CH FINAL rows detail ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, log_index, token_address, from_address, to_address, amount_raw, source_ingested_at FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE chain='bsc' AND block_number=115394241 AND (from_address='$P' OR to_address='$P') FORMAT Vertical"
echo "=== coverage rows for peer2 ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count, committed_at FROM dataset_coverage WHERE subject='$P' ORDER BY from_block;"
