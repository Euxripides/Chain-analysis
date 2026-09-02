#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== router logs by address case ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT address, count() FROM $DB.logs FINAL WHERE chain='bsc' AND address ILIKE '0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' GROUP BY address"
echo "=== bucket 60000000-60000999 with ILIKE ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT intDiv(block_number,100)*100 AS bucket, count() FROM $DB.logs FINAL WHERE chain='bsc' AND lower(address)='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND block_number BETWEEN 60000000 AND 60000999 GROUP BY bucket ORDER BY bucket LIMIT 5"
echo "=== same-row case-duplicate check: tx+log_index groups with >1 distinct address ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM (SELECT tx_hash, log_index, uniqExact(address) AS c FROM $DB.logs FINAL WHERE chain='bsc' AND lower(address)='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' GROUP BY tx_hash, log_index HAVING c > 1)"
