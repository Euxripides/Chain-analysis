#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
set -a; . "$P/deploy/.env"; set +a
[ -f /tmp/dual-system-fixture.env ] && . /tmp/dual-system-fixture.env
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
BASE=http://127.0.0.1:8080
ADDR=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
CK=$(awk '/^#HttpOnly_/ { sub(/^#HttpOnly_/,""); printf "%s=%s; ", $6,$7; next } /^#/||/^$/ {next} {printf "%s=%s; ", $6,$7}' /tmp/ckprobe)
EP="${1:-activity}"
case "$EP" in
  activity) PATHQ="/api/v2/explorer/addresses/tron/$ADDR/activity?limit=200";;
  flow-profile) PATHQ="/api/v2/explorer/addresses/tron/$ADDR/flow-profile";;
  overview) PATHQ="/api/v2/explorer/addresses/tron/$ADDR/overview";;
  assets) PATHQ="/api/v2/explorer/addresses/tron/$ADDR/assets";;
esac
START=$(date -u +"%Y-%m-%d %H:%M:%S")
echo "== single request: $EP =="
curl -s -o /dev/null -b "$CK" -w 'http=%{http_code} total=%{time_total}s bytes=%{size_download}\n' "$BASE$PATHQ"
echo "== CH queries issued since $START =="
curl -s "$CH" --data-binary "SELECT round(query_duration_ms/1000,3) secs, read_rows, formatReadableSize(memory_usage) mem, substring(query,1,190) q FROM system.query_log WHERE event_time >= toDateTime('$START','UTC') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query NOT LIKE '%system.query_log%' ORDER BY event_time ASC FORMAT Vertical"
echo "== api journal tail =="
journalctl --user -u blockchain-api --since "$START" --no-pager | tail -20
