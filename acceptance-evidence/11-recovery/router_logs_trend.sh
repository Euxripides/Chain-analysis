#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== router logs per 100-block bucket (60000000-60001200) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT intDiv(block_number, 100) * 100 AS bucket, count() FROM $DB.logs FINAL WHERE chain='bsc' AND address='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND block_number BETWEEN 60000000 AND 60001200 GROUP BY bucket ORDER BY bucket"
echo "=== per 10-block bucket tail ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT intDiv(block_number, 10) * 10 AS bucket, count() FROM $DB.logs FINAL WHERE chain='bsc' AND address='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' AND block_number BETWEEN 60000900 AND 60001200 GROUP BY bucket ORDER BY bucket"
