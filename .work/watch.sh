#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
echo "== currently running =="
curl -s "$CH" --data-binary "SELECT elapsed, substring(query,1,120) q FROM system.processes WHERE query NOT LIKE '%system.processes%' ORDER BY elapsed DESC LIMIT 8"
echo "== idle-ish check =="
top -bn1 | head -5
echo "== recent finished queries (>5s, last 10 min) =="
curl -s "$CH" --data-binary "SELECT round(query_duration_ms/1000,1) secs, read_rows, formatReadableSize(memory_usage) mem, substring(query,1,110) q FROM system.query_log WHERE event_time > now() - INTERVAL 10 MINUTE AND type='QueryFinish' AND query_duration_ms > 5000 AND query NOT LIKE '%system.query_log%' ORDER BY query_duration_ms DESC LIMIT 12"
