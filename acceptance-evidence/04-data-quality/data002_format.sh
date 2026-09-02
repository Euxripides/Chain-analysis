#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== DATA-002 field format checks ==="
echo "--- tx_hash format violations (transactions) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE length(tx_hash) != 66 OR NOT startsWith(tx_hash,'0x')"
echo "--- token_transfers schema ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "DESCRIBE TABLE $DB.token_transfers"
echo "--- from/to address format violations (token_transfers) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.token_transfers FINAL WHERE length(from_address) != 42 OR length(to_address) != 42 OR length(token_address) != 42"
echo "--- non-hex address sample (transactions) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE NOT match(from_address, '^0x[0-9a-f]{40}$') OR NOT match(to_address, '^0x[0-9a-f]{40}$')"
echo "--- uppercase addresses (normalization check) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE from_address != lower(from_address) OR to_address != lower(to_address)"
echo "--- timestamp sanity (bsc) ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() AS future_or_before_genesis FROM $DB.transactions FINAL WHERE chain='bsc' AND (timestamp > now() OR timestamp < toDateTime64('2020-08-29 00:00:00',3))"
echo "--- token_decimals distribution ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT token_decimals, count() FROM $DB.token_transfers FINAL GROUP BY token_decimals ORDER BY count() DESC LIMIT 15"
echo "--- block_number sanity ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT min(block_number), max(block_number) FROM $DB.transactions FINAL WHERE chain='bsc'"
echo "--- value_raw numeric string check ---"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE value_raw NOT LIKE '%e%' AND length(value_raw)>0 AND NOT match(value_raw,'^[0-9]+$')"
