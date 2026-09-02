#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== sample transactions (10 valid rows) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, from_address, to_address, value_raw, status FROM $DB.transactions FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x') ORDER BY rand() LIMIT 10 FORMAT JSONEachRow"
echo "=== sample token_transfers (10 valid rows) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, log_index, token_address, from_address, to_address, amount_raw FROM $DB.token_transfers FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x') ORDER BY rand() LIMIT 10 FORMAT JSONEachRow"
echo "=== sample logs (10 valid rows) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, log_index, address FROM $DB.logs FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x') ORDER BY rand() LIMIT 10 FORMAT JSONEachRow"
