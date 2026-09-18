#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
curl -s "$CH" --data-binary "KILL QUERY WHERE elapsed > 5 AND query NOT LIKE '%system.processes%' SYNC FORMAT TSV" | wc -l
echo "--- t0 remaining ---"
curl -s "$CH" --data-binary "SELECT count() FROM system.processes WHERE query NOT LIKE '%system.processes%'"
echo "--- watching 75s for new heavy queries ---"
for i in 1 2 3; do
  sleep 25
  echo "t=$((i*25))s: $(curl -s "$CH" --data-binary "SELECT count() FROM system.processes WHERE query NOT LIKE '%system.processes%'") running; max_elapsed=$(curl -s "$CH" --data-binary "SELECT round(max(elapsed),2) FROM system.processes WHERE query NOT LIKE '%system.processes%'")"
done
free -g | head -2
uptime
