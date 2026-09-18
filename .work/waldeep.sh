#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== raw sample #1 =="
PSQL "SELECT 'lsn='||pg_current_wal_lsn()||' insert='||pg_current_wal_insert_lsn()||' flush='||pg_current_wal_flush_lsn();"
PSQL "SELECT 'wal_bytes='||wal_bytes||' records='||wal_records||' fpi='||wal_fpi||' buffers_full='||wal_buffers_full FROM pg_stat_wal;"
echo "== pg_stat_io (wal) =="
PSQL "SELECT string_agg(backend_type||'/'||object||'/'||context||' w='||writes||' bytes='||write_bytes, ' | ') FROM pg_stat_io WHERE object='wal' OR context LIKE '%wal%';" 2>&1 | head -5
echo "== archiving / slots / replicas =="
PSQL "SELECT 'archive_mode='||current_setting('archive_mode')||' wal_level='||current_setting('wal_level')||' slots='||(SELECT count(*) FROM pg_replication_slots)||' replicas='||(SELECT count(*) FROM pg_stat_replication);"
sleep 120
echo "== raw sample #2 (after 120s) =="
PSQL "SELECT 'lsn='||pg_current_wal_lsn()||' insert='||pg_current_wal_insert_lsn();"
PSQL "SELECT 'wal_bytes='||wal_bytes||' records='||wal_records||' fpi='||wal_fpi FROM pg_stat_wal;"
PSQL "SELECT string_agg(backend_type||'/'||object||'/'||context||' w='||writes||' bytes='||write_bytes, ' | ') FROM pg_stat_io WHERE object='wal' OR context LIKE '%wal%';"
echo "== WAL directory size =="
docker exec deploy-postgres-1 sh -c 'du -sh ${PGDATA}/pg_wal 2>/dev/null; ls -1 ${PGDATA}/pg_wal | wc -l'
