#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== pg_stat_statements available? =="
PSQL "SELECT count(*) FROM pg_extension WHERE extname='pg_stat_statements';"
echo "== server version =="
PSQL "SHOW server_version;"
echo "== top WAL consumers (wal_bytes, if column exists) =="
PSQL "SELECT calls, rows, pg_size_pretty(wal_bytes::bigint) AS wal, left(regexp_replace(query, '\s+', ' ', 'g'), 110) FROM pg_stat_statements WHERE wal_bytes > 0 ORDER BY wal_bytes DESC LIMIT 20;" 2>&1 | head -25
echo "== top by shared_blks_written =="
PSQL "SELECT calls, pg_size_pretty((shared_blks_written*8192)::bigint) AS written, left(regexp_replace(query, '\s+', ' ', 'g'), 110) FROM pg_stat_statements ORDER BY shared_blks_written DESC LIMIT 10;" 2>&1 | head -15
echo "== stats reset time =="
PSQL "SELECT stats_reset FROM pg_stat_statements_info;" 2>&1 | head -3
