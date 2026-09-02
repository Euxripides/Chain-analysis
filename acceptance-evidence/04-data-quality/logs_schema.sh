#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== logs schema ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "DESCRIBE TABLE $CLICKHOUSE_DB.logs"
echo "=== benchmark logs count ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $CLICKHOUSE_DB.logs WHERE chain='bsc' AND address='0x9b77e3b4f85c7cea7da451f454895edbd10b80dd'"
