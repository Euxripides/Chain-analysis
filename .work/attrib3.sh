#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
set -a; . "$P/deploy/.env"; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
for EP in activity flow-profile overview assets; do
  S=$(date -u +"%Y-%m-%d %H:%M:%S")
  echo "########## $EP (start $S UTC) ##########"
  bash "$P/scripts/perf/lan-explorer-probe.sh" "attrib-$EP" 1 0 /tmp/attrib >/dev/null 2>&1 || true
  python3 - "/tmp/attrib/attrib-$EP-$EP.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
print("  endpoint %s: p50=%.1f ms status=%s bytes=%s" % (d["endpoint"], d["p50_ms"], d["status_counts"], d["avg_bytes"]))
PY
  sleep 12
  curl -s "$CH" --data-binary "SELECT round(query_duration_ms/1000,3) secs, read_rows, formatReadableSize(memory_usage) mem, substring(query,1,160) q FROM system.query_log WHERE event_time >= toDateTime('$S','UTC') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query NOT LIKE '%system.query_log%' ORDER BY event_time ASC FORMAT TSV" | sed 's/^/    CH /'
done
