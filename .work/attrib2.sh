#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
set -a; . "$P/deploy/.env"; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
START=$(date -u +"%Y-%m-%d %H:%M:%S")
echo "START_UTC=$START"
bash "$P/scripts/perf/lan-explorer-probe.sh" attrib 1 0 /tmp/attrib
echo "== CH queries (clickhouse-go) during the window =="
curl -s "$CH" --data-binary "SELECT round(query_duration_ms/1000,3) secs, read_rows, formatReadableSize(memory_usage) mem, substring(query,1,200) q FROM system.query_log WHERE event_time >= toDateTime('$START','UTC') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query NOT LIKE '%system.query_log%' ORDER BY event_time ASC FORMAT TSV"
echo "== API http lines in window =="
journalctl --user -u blockchain-api --since "$START" --no-pager | grep -o '"path":"[^"]*"[^}]*"status":[0-9]*,"duration_ms":[0-9]*' | tail -20
