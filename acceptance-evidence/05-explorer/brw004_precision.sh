#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== amount extremes (valid rows, benchmark) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT amount_raw, length(amount_raw) AS len FROM $DB.token_transfers FINAL WHERE chain='bsc' AND (from_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' OR to_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd') ORDER BY length(amount_raw) ASC LIMIT 5"
echo "=== zero amounts count ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.token_transfers FINAL WHERE chain='bsc' AND (from_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' OR to_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd') AND amount_raw='0'"
echo "=== trailing zeros sample ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT amount_raw, amount FROM $DB.token_transfers FINAL WHERE chain='bsc' AND amount_raw LIKE '%0000' AND length(tx_hash)=66 LIMIT 3"
echo "=== largest amounts (valid rows) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT amount_raw FROM $DB.token_transfers FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x') ORDER BY length(amount_raw) DESC LIMIT 3"
