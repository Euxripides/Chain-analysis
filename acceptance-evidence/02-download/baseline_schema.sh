#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== clickhouse tables ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SHOW TABLES FROM $CLICKHOUSE_DB"
echo "=== dataset_coverage ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT chain, subject, dataset, from_block, to_block, row_count, source FROM dataset_coverage ORDER BY dataset, from_block LIMIT 60;"
