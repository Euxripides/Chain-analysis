#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== ankr_scheduler_state worker_ids =="
PSQL "SELECT count(*) AS rows, count(DISTINCT worker_id) AS workers FROM ankr_scheduler_state;"
echo "== ankr_scheduler_state rows by worker =="
PSQL "SELECT worker_id, updated_at, active_slots, concurrency_target FROM ankr_scheduler_state ORDER BY updated_at DESC LIMIT 5;"
echo "== ankr_performance_control =="
PSQL "SELECT count(*) FROM ankr_performance_control;"
echo "== table sizes / dead tuples (top 15 by n_dead_tup) =="
PSQL "SELECT relname, n_live_tup, n_dead_tup, pg_size_pretty(pg_total_relation_size(relid)) FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 15;"
