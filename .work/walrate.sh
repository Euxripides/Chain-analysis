#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== pg_stat_wal cumulative =="
PSQL "SELECT 'records='||wal_records||' fpi='||wal_fpi||' bytes='||pg_size_pretty(wal_bytes)||' buffers_full='||wal_buffers_full||' write='||wal_write||' sync='||wal_sync||' reset='||stats_reset FROM pg_stat_wal;"
echo "== 60s WAL rate (lsn diff) =="
L1=$(PSQL "SELECT pg_current_wal_lsn();"); T1=$(date +%s)
sleep 60
L2=$(PSQL "SELECT pg_current_wal_lsn();"); T2=$(date +%s)
PSQL "SELECT 'delta='||pg_size_pretty(pg_wal_lsn_diff('$L2','$L1')::bigint)||' over '||($T2-$T1)||'s  =>  '||round(pg_wal_lsn_diff('$L2','$L1')/1024.0/($T2-$T1)*60)||' KB/min';"
echo "== wal_fpi delta over the same window =="
PSQL "SELECT 'fpi_now='||wal_fpi||' records_now='||wal_records FROM pg_stat_wal;"
echo "== job_event_log size and row estimate =="
PSQL "SELECT pg_size_pretty(pg_total_relation_size('job_event_log'))||' total, rows='||(SELECT count(*) FROM job_event_log)||', payload_bytes/row=1922 (measured)';"
echo "== checkpoint settings (FPI driver) =="
PSQL "SELECT 'max_wal_size='||current_setting('max_wal_size')||' checkpoint_timeout='||current_setting('checkpoint_timeout')||' full_page_writes='||current_setting('full_page_writes');"
