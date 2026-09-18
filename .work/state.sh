#!/usr/bin/env bash
set -uo pipefail
echo "== top =="
top -bn1 | head -18
echo "== running CH queries =="
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
curl -s "$CH" --data-binary "SELECT elapsed, read_rows, formatReadableSize(memory_usage) mem, substring(query,1,110) q FROM system.processes WHERE query NOT LIKE '%system.processes%' ORDER BY elapsed DESC LIMIT 10"
echo "== api unit =="
systemctl --user status blockchain-api --no-pager | head -12
