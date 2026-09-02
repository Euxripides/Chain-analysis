#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== address_balances schema ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "DESCRIBE TABLE $DB.address_balances"
echo "=== benchmark balances ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT * FROM $DB.address_balances FINAL WHERE chain='bsc' AND address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd' FORMAT Vertical"
