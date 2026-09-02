#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT tx_hash, block_number, from_address, to_address, input, method_id, method_name, method_source, method_verified FROM $DB.transactions FINAL WHERE chain='bsc' AND (from_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' OR to_address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd') FORMAT Vertical"
