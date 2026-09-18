#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
echo "== killing orphaned long queries (>60s, clickhouse-go) =="
curl -s "$CH" --data-binary "KILL QUERY WHERE elapsed > 60 AND query NOT LIKE '%system.processes%' SYNC FORMAT TSV" | head -20
echo "== remaining =="
sleep 3
curl -s "$CH" --data-binary "SELECT count() FROM system.processes WHERE query NOT LIKE '%system.processes%'"
echo "== memory/load =="
free -g | head -2
uptime
