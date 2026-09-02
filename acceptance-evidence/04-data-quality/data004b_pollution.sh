#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DB=$CLICKHOUSE_DB
echo "=== valid tx_hash rows per dataset ==="
echo "transactions:"; docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.transactions FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x')"
echo "token_transfers:"; docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.token_transfers FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x')"
echo "logs:"; docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT count() FROM $DB.logs FINAL WHERE length(tx_hash)=66 AND startsWith(tx_hash,'0x')"
echo "=== tx_hash length distribution token_transfers ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT length(tx_hash) AS l, count() FROM $DB.token_transfers FINAL GROUP BY l ORDER BY count() DESC LIMIT 6"
echo "=== tx_hash length distribution logs ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT length(tx_hash) AS l, count() FROM $DB.logs FINAL GROUP BY l ORDER BY count() DESC LIMIT 6"
echo "=== ingest time distribution (transactions) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT toDate(source_ingested_at) AS d, count() FROM $DB.transactions FINAL GROUP BY d ORDER BY d"
echo "=== ingest time distribution (token_transfers) ==="
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT toDate(source_ingested_at) AS d, count() FROM $DB.token_transfers FINAL GROUP BY d ORDER BY d"
echo "=== jobs from 08-31 with placeholder subjects ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT j.job_no, j.created_at, j.status, j.datasets, s.subject FROM download_jobs j JOIN download_subjects s ON s.job_id=j.id WHERE j.created_at < '2026-09-01' ORDER BY j.created_at LIMIT 40;"
