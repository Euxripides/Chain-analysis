#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
export PATH=/usr/local/go/bin:$PATH
set -a; . "$P/deploy/.env"; set +a
CH="http://127.0.0.1:8123/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
echo "== 每个 5 分钟桶里 >1s 的 ClickHouse 查询（最近 45 分钟，含改前/改后）=="
curl -s "$CH" --data-binary "SELECT toStartOfFiveMinute(event_time) AS bucket, count() AS n, max(round(query_duration_ms/1000)) AS max_secs, formatReadableSize(max(memory_usage)) AS max_mem FROM system.query_log WHERE event_time > now() - INTERVAL 45 MINUTE AND type='QueryFinish' AND query_duration_ms > 1000 AND query NOT LIKE '%system.query_log%' GROUP BY bucket ORDER BY bucket FORMAT TSV"
echo
echo "== 新 worker 上线后（2026-09-17 01:29:29 +07 = 18:29:29 UTC）的 >60s / >20s 查询 =="
curl -s "$CH" --data-binary "SELECT countIf(query_duration_ms > 60000) AS over_60s, countIf(query_duration_ms > 20000) AS over_20s, count() AS over_1s, round(max(query_duration_ms)/1000,2) AS max_secs FROM system.query_log WHERE event_time >= toDateTime('2026-09-16 18:29:29','UTC') AND type='QueryFinish' AND query_duration_ms > 1000 AND query NOT LIKE '%system.query_log%' FORMAT TSV"
echo
echo "== 改后窗口里最慢的 5 条 =="
curl -s "$CH" --data-binary "SELECT round(query_duration_ms/1000,2) AS secs, read_rows, formatReadableSize(memory_usage) AS mem, substring(query,1,100) AS q FROM system.query_log WHERE event_time >= toDateTime('2026-09-16 18:29:29','UTC') AND type='QueryFinish' AND query_duration_ms > 1000 AND query NOT LIKE '%system.query_log%' ORDER BY query_duration_ms DESC LIMIT 5 FORMAT TSV"
echo
echo "== 改前窗口仍存在的 >60s（冻结 worker 期间）=="
curl -s "$CH" --data-binary "SELECT count() AS over_60s, round(max(query_duration_ms)/1000) AS max_secs, formatReadableSize(max(memory_usage)) AS max_mem, substring(argMax(query, query_duration_ms),1,90) AS worst FROM system.query_log WHERE event_time >= toDateTime('2026-09-16 17:00:00','UTC') AND event_time < toDateTime('2026-09-16 18:29:29','UTC') AND type='QueryFinish' AND query_duration_ms > 60000 AND query NOT LIKE '%system.query_log%' FORMAT TSV"
echo
echo "== 定向 fresh 测试（-count=1，只跑受影响包与合并改动相关包）=="
cd "$P/backend"
go test -count=1 ./internal/clickhouse/... ./internal/addressmeta/... ./internal/ingest/... ./internal/api/... ./internal/download/... ./internal/explorer/... ./internal/analysis/... ./internal/query/... ./internal/tron/... 2>&1 | tail -20
echo "targeted_test_exit=${PIPESTATUS[0]}"
