#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
echo "== processes detail =="
curl -s "$CH" --data-binary "SELECT query_id, user, elapsed, interface, client_name, substring(query,1,150) AS q FROM system.processes WHERE query NOT LIKE '%system.processes%' ORDER BY elapsed DESC LIMIT 12 FORMAT Vertical"
echo "== recent api/worker journal =="
journalctl --user -u blockchain-worker -n 25 --no-pager | tail -25
echo "== download jobs running =="
docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -c "select id, status, chain, created_at, updated_at from download_jobs where status in ('running','queued') order by updated_at desc limit 10;" 2>&1 | head -25
