#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== contract-call txs sample (input present) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, to_address, substring(input,1,12) AS sel, method_id, method_name, method_source, method_verified FROM $DB.transactions FINAL WHERE chain='bsc' AND length(tx_hash)=66 AND input != '0x' AND length(input)>=10 ORDER BY rand() LIMIT 8 FORMAT JSONEachRow"
echo "=== method source distribution (valid rows) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT method_source, method_verified, count() FROM $DB.transactions FINAL WHERE chain='bsc' AND length(tx_hash)=66 GROUP BY method_source, method_verified ORDER BY count() DESC LIMIT 20"
