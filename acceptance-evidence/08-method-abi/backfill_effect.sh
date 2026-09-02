#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== method_source distribution after backfill (all FINAL) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT method_source, method_verified, count() FROM $DB.transactions FINAL WHERE chain='bsc' GROUP BY method_source, method_verified ORDER BY count() DESC LIMIT 10"
echo "=== method_resolve_queue depth ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, count(*) FROM method_resolve_queue GROUP BY status;"
echo "=== method_signatures count ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT count(*) FROM method_signatures;"
