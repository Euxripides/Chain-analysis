#!/usr/bin/env bash
# P4-FLOW-01 bounded-scan probe (candidate only; never touches the running API).
#
# GET /api/v1/address/flow-summary used to run one unbounded aggregation:
#   ... FROM blockchain.token_transfers FINAL WHERE chain=? AND (from=? OR to=?)
# Measured 2026-09-15 on the real instance: 85,074,805 rows read, 6.87-10.62 GiB
# held, 23.4-52.3 s per call, refused with Code 241 at the 19.60 GiB ceiling and
# collapsed into a blanket 500 by the 60 s router timeout.
#
# This probe asserts, against a candidate binary on its own port:
#   * the endpoint answers 200 on the bounded deduplicated scan;
#   * the answer equals an independent ClickHouse reference (LIMIT 1 BY dedup);
#   * the ClickHouse queries the candidate issues carry the bounded interactive
#     settings and read < 5,000,000 rows / < 512 MiB (measured 1.44 M / 16-35 MiB);
#   * the candidate issues no FINAL query at all;
#   * with a memory bound that cannot fit (negmem build) the endpoint answers
#     503 SCAN_TOO_LARGE instead of a blanket 500.
#
# Usage: BIN=... PORT=... EXPECT_STATUS=200 EXPECT_CODE="" bash this.sh
set -u
R=/home/euripides/projects/blockchain-platform
BIN="${BIN:-$R/backend/bin.new/api-flowfix-calib}"
PORT="${PORT:-18197}"
ADDR="${ADDR:-0xa948aaa56c7dd454b9b02be5d0a9bcd116393619}"
ADDR2="${ADDR2:-0xb6683d06714d3de5e83000c49c85804e219e6736}"
EXPECT_STATUS="${EXPECT_STATUS:-200}"
EXPECT_CODE="${EXPECT_CODE:-}"
KEY="flowfix-probe-key"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-flowfix-$TS}"
API="http://127.0.0.1:$PORT"
mkdir -p "$EV"
PASS=0
FAIL=0
chk(){ if [ "$1" = "$2" ]; then PASS=$((PASS+1)); echo "PASS $3 ($1)"; else FAIL=$((FAIL+1)); echo "FAIL $3 (got=$1 want=$2)"; fi; }
CH="docker exec -i deploy-clickhouse-1 clickhouse-client"
sql(){ printf '%s\n' "$1" | $CH --multiquery 2>&1; }

echo "=== candidate $BIN on port $PORT (evidence $EV) ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
(
  cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export GPT_ACTIONS_API_KEYS="$KEY"
  nohup setsid "$BIN" >> "$EV/candidate.log" 2>&1 &
  echo $! > "$EV/candidate.pid"
)
sleep 1
cand_pid=$(cat "$EV/candidate.pid")
READY_TRIES="${READY_TRIES:-240}"
ready=0
rc=""
for _ in $(seq 1 "$READY_TRIES"); do
  rc=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$API/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate ready on $PORT (http $rc after $(( READY_TRIES / 2 ))s max)"
if [ "$ready" != "1" ]; then
  echo "READY_ABORT=1 the candidate never became ready; no request result below is meaningful."
  kill -TERM "$cand_pid" 2>/dev/null
  sleep 1
  kill -KILL "$cand_pid" 2>/dev/null
  echo "=== summary ==="; echo "PASS=$PASS FAIL=$FAIL"; echo "PROBE_FAIL=$FAIL"
  exit 0
fi
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d' ' -f1)" "$(sha256sum "$BIN" | cut -d' ' -f1)" "running candidate is the binary under test"

WM=$(sql "SELECT toString(now() - 1) FORMAT TSV" | tr -d '\r' | head -1)
echo "watermark=$WM"

call(){
  local addr=$1 extra=$2 out=$3
  curl -s --max-time 120 -o "$out" -w '%{http_code} %{time_total}' -H "Authorization: Bearer $KEY" \
    "$API/api/v1/address/flow-summary?chain=bsc&address=$addr$extra" 2>>"$EV/candidate.log"
}

echo "=== request 1: $ADDR (no window) ==="
r1=$(call "$ADDR" "" "$EV/flow-a948.json")
chk "$(echo "$r1" | cut -d' ' -f1)" "$EXPECT_STATUS" "flow-summary status for $ADDR"
echo "1: http=$(echo "$r1" | cut -d' ' -f1) seconds=$(echo "$r1" | cut -d' ' -f2)"
if [ -n "$EXPECT_CODE" ]; then
  chk "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["error"]["code"])' "$EV/flow-a948.json" 2>/dev/null)" "$EXPECT_CODE" "flow-summary error code for $ADDR"
fi

echo "=== request 2: $ADDR2 (no window) ==="
# A second, lighter address: under the negative-control cap it may legitimately
# still fit, so its own expectation is configurable.
EXPECT_STATUS2="${EXPECT_STATUS2:-$EXPECT_STATUS}"
r2=$(call "$ADDR2" "" "$EV/flow-b668.json")
chk "$(echo "$r2" | cut -d' ' -f1)" "$EXPECT_STATUS2" "flow-summary status for $ADDR2"
echo "2: http=$(echo "$r2" | cut -d' ' -f1) seconds=$(echo "$r2" | cut -d' ' -f2)"

echo "=== request 3: $ADDR with a real window ==="
r3=$(call "$ADDR" "&start_time=2026-09-01T00:00:00Z&end_time=2026-09-15T00:00:00Z" "$EV/flow-a948-window.json")
chk "$(echo "$r3" | cut -d' ' -f1)" "$EXPECT_STATUS" "flow-summary status for $ADDR with a window"
echo "3: http=$(echo "$r3" | cut -d' ' -f1) seconds=$(echo "$r3" | cut -d' ' -f2)"

echo "=== ClickHouse footprint of the candidate (settings fingerprint max_memory_usage=2147483648) ==="
sql "SYSTEM FLUSH LOGS;" >/dev/null
sql "SELECT query_duration_ms, read_rows, memory_usage, Settings['max_execution_time'], Settings['max_memory_usage'], Settings['max_threads'] FROM system.query_log WHERE event_time > now() - INTERVAL 5 MINUTE AND event_time >= toDateTime('$WM') AND type='QueryFinish' AND query LIKE '%$ADDR%' AND query LIKE '%argMax(amount,source_ingested_at)%' AND query NOT LIKE '%LIMIT 1 BY%' AND Settings['max_memory_usage'] = '2147483648' ORDER BY event_time FORMAT TSV" | tee "$EV/ch-footprint.tsv"
EXPECT_SCANS="${EXPECT_SCANS:-2}"
chk "$(grep -c . "$EV/ch-footprint.tsv")" "$EXPECT_SCANS" "bounded scans completed in ClickHouse for $ADDR"
EXPECT_REFUSED="${EXPECT_REFUSED:-0}"
chk "$(sql "SELECT count() FROM system.query_log WHERE event_time > now() - INTERVAL 5 MINUTE AND event_time >= toDateTime('$WM') AND query LIKE '%$ADDR%' AND query LIKE '%argMax(amount,source_ingested_at)%' AND exception_code = 241 FORMAT TSV" | tr -d '\r' | head -1)" "$EXPECT_REFUSED" "scans ClickHouse refused with Code 241"
chk "$(sql "SELECT count() FROM system.query_log WHERE event_time > now() - INTERVAL 5 MINUTE AND event_time >= toDateTime('$WM') AND query LIKE '%$ADDR%' AND query LIKE '%argMax(amount,source_ingested_at)%' AND query NOT LIKE '%LIMIT 1 BY%' AND Settings['max_memory_usage'] = '' FORMAT TSV" | tr -d '\r' | head -1)" "0" "no unbounded scan of this shape ran in the window"
python3 "$R/scripts/s7/p4_flow_summary_verify.py" footprint "$EV/ch-footprint.tsv" "$EXPECT_SCANS" | tee "$EV/footprint-verdict.txt"
chk "$(tail -1 "$EV/footprint-verdict.txt")" "PASS" "every candidate scan stayed inside the interactive budget"
chk "$(sql "SELECT count() FROM system.query_log WHERE event_time > now() - INTERVAL 5 MINUTE AND Settings['max_execution_time'] = '25' AND query LIKE '%token_transfers FINAL%' FORMAT TSV" | tr -d '\r' | head -1)" "0" "the candidate issued no FINAL query"

if [ "$EXPECT_STATUS" = "200" ]; then
  echo "=== independent reference (LIMIT 1 BY dedup, direct ClickHouse) ==="
  sql "SELECT toString(sumIf(amount,to_address='$ADDR' AND from_address!='$ADDR')),toString(sumIf(amount,from_address='$ADDR' AND to_address!='$ADDR')),toString(sumIf(amount,to_address='$ADDR' AND from_address!='$ADDR')-sumIf(amount,from_address='$ADDR' AND to_address!='$ADDR')),countIf(to_address='$ADDR' AND from_address!='$ADDR'),countIf(from_address='$ADDR' AND to_address!='$ADDR'),uniqExactIf(from_address,to_address='$ADDR' AND from_address!='$ADDR'),uniqExactIf(to_address,from_address='$ADDR' AND to_address!='$ADDR'),nullIf(minIf(timestamp,to_address='$ADDR' AND from_address!='$ADDR'),toDateTime64(0,3,'UTC')),nullIf(maxIf(timestamp,to_address='$ADDR' AND from_address!='$ADDR'),toDateTime64(0,3,'UTC')),nullIf(minIf(timestamp,from_address='$ADDR' AND to_address!='$ADDR'),toDateTime64(0,3,'UTC')),nullIf(maxIf(timestamp,from_address='$ADDR' AND to_address!='$ADDR'),toDateTime64(0,3,'UTC')) FROM (SELECT amount, timestamp, from_address, to_address FROM blockchain.token_transfers WHERE chain='bsc' AND (from_address='$ADDR' OR to_address='$ADDR') ORDER BY source_ingested_at DESC LIMIT 1 BY tx_hash, log_index, token_address) FORMAT TSV" > "$EV/reference-a948.tsv"
  python3 "$R/scripts/s7/p4_flow_summary_verify.py" compare "$EV/flow-a948.json" "$EV/reference-a948.tsv" | tee "$EV/compare-verdict.txt"
  chk "$(tail -1 "$EV/compare-verdict.txt")" "PASS" "the API answer equals the independent ClickHouse reference"
fi

echo "=== cleanup ==="
kill -TERM "$cand_pid" 2>/dev/null
for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
kill -KILL "$cand_pid" 2>/dev/null
sleep 0.5
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free after cleanup"
chk "$(sha256sum $R/backend/bin/api 2>/dev/null | cut -d' ' -f1)" "910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38" "the deployed production binary is unchanged"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "PROBE_FAIL=$FAIL"
exit 0
