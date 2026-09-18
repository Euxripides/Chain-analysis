#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== WAL composition delta over 60s (PG18 column names) =="
A=$(PSQL "SELECT wal_records||'|'||wal_fpi||'|'||wal_bytes FROM pg_stat_wal;")
L1=$(PSQL "SELECT pg_current_wal_lsn();")
sleep 60
B=$(PSQL "SELECT wal_records||'|'||wal_fpi||'|'||wal_bytes FROM pg_stat_wal;")
L2=$(PSQL "SELECT pg_current_wal_lsn();")
echo "A=$A"; echo "B=$B"
python3 - "$A" "$B" <<'PY'
import sys
a=[int(x) for x in sys.argv[1].split('|')]; b=[int(x) for x in sys.argv[2].split('|')]
rec,fpi,by = (b[i]-a[i] for i in range(3))
print("records/min = %d   fpi/min = %d   fpi_bytes/min = %.2f MB   wal_bytes/min = %.2f MB" % (rec,fpi,fpi*8192/1048576,by/1048576))
print("=> FPI share of WAL = %.0f%%   avg bytes/record(excl FPI) = %.0f" % (fpi*8192/by*100 if by else 0, (by-fpi*8192)/rec if rec else 0))
PY
echo "== lsn delta =="
PSQL "SELECT pg_size_pretty(pg_wal_lsn_diff('$L2','$L1')::bigint)||' per 60s';"
echo "== anything heavy running =="
PSQL "SELECT pid||' | '||state||' | '||coalesce(wait_event_type,'')||' | '||left(regexp_replace(query,'\s+',' ','g'),90) FROM pg_stat_activity WHERE state<>'idle' AND pid<>pg_backend_pid() ORDER BY query_start LIMIT 12;"
echo "== maintenance in progress =="
PSQL "SELECT 'vacuum_progress='||count(*) FROM pg_stat_progress_vacuum;"
PSQL "SELECT 'autovacuum_running='||count(*) FROM pg_stat_activity WHERE backend_type='autovacuum worker';"
echo "== checkpointer =="
PSQL "SELECT 'timed='||num_timed||' requested='||num_requested||' write_time_ms='||write_time||' buffers='||buffers_written FROM pg_stat_checkpointer;" 2>&1 | head -3
echo "== largest relations by total size =="
PSQL "SELECT relname||' '||pg_size_pretty(pg_total_relation_size(relid)) FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 8;"
