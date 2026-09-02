#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== all PEER3 rows at 114147137 (either side) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, log_index, from_address, to_address, amount_raw, source_ingested_at FROM $CLICKHOUSE_DB.token_transfers FINAL WHERE chain='bsc' AND block_number=114147137 AND (from_address='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' OR to_address='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28') FORMAT Vertical"
