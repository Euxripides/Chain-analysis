#!/usr/bin/env bash
# p4-ch-driver-probe-v1.sh
#
# Which gated write route really issues a ClickHouse query?
#
# The frozen P4 matrix classifies a route as "clickhouse" by static reachability
# of the dependency tokens (s.deps.CH, s.query., s.deps.Query) anywhere in the
# transitive call graph. That is a candidate list, not a measurement: with a
# chain size of ~100 functions almost every admin handler reaches some shared
# helper. This probe measures instead of arguing: it opens a ClickHouse
# query_log window, issues ONE request, and prints every query the request
# really produced.
#
# The counter is exact rather than statistical:
#   type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select'
# is the API/worker Go client; a background MergeTree merge never matches.
#
# ASCII only. Prints no credential, Cookie, CSRF token or DSN.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
PORT=18192
DELAY_MS=4000
BIN=${BIN:-$R/backend/bin.new/api-expcap-r3-calib}
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-chprobe-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/probe-console.log") 2>&1

API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
chq(){ docker exec -i deploy-clickhouse-1 clickhouse-client --query "$1" 2>/dev/null | tr -d '[:space:]'; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }

echo "ts_utc=$TS probe_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "production_api_pid=$AP production_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"

echo "=== fixture (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 clickhouse driver probe" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
INVPW1="Pw-$TS-chprobe-3f21"
cat "$EV/fixture.log"

clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" aud="${5:-user}" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$WSA\"}")
  echo "clear_flag $tag login=$c"
  csrf=$(csrf_of "$jar" "cs_${aud}_session_csrf")
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  echo "clear_flag $tag password_change=$c"
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1" user
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json

RA=$EV/inv.jar
c=$(curl -s -c "$RA" -o "$EV/inv-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H "X-Auth-Audience: user" \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
INV_CSRF=$(csrf_of "$RA" cs_user_session_csrf)
echo "investigator_login=$c csrf_present=$([ -n "$INV_CSRF" ] && echo yes || echo no)"

echo "=== candidate ==="
if ss -ltn 2>/dev/null | grep -q ":$PORT "; then echo "PORT_BUSY abort"; exit 0; fi
cand_pid=""
cleanup(){
  if [ -n "$cand_pid" ] && kill -0 "$cand_pid" 2>/dev/null; then
    kill -TERM "$cand_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$cand_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export SCOPE_CALIB_ENABLE=1
  export SCOPE_CALIB_PHASE=post
  export SCOPE_CALIB_DELAY_MS="$DELAY_MS"
  unset SCOPE_CALIB_ROUTE
  nohup setsid "$BIN" >> "$EV/candidate.log" 2>&1 &
  echo $! > "$EV/candidate.pid" )
sleep 1
cand_pid=$(cat "$EV/candidate.pid")
ready=0
for _ in $(seq 1 240); do
  rc=$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
echo "candidate_ready=$ready"
if [ "$ready" != "1" ]; then
  echo "=== summary ==="; echo "PROBE_ABORT=candidate_not_ready"; exit 0
fi

window_start(){ date -u '+%Y-%m-%d %H:%M:%S'; }
window_dump(){
  echo "--- query_log window since $1 (Select, clickhouse-go, QueryFinish) ---"
  docker exec -i deploy-clickhouse-1 clickhouse-client --query "SELECT event_time, query_kind, query_duration_ms, substring(replaceAll(query,char(10),' '),1,110) FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select' ORDER BY event_time FORMAT TSV" 2>&1
  echo "--- end window ---"
}

echo "=== W0 idle calibration: GET /health/ready on the candidate issues SELECT 1 ==="
W=$(window_start); sleep 1
rc=$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")
sleep 2
echo "w0_ready=$rc"
window_dump "$W"

echo "=== W1 preview with a COVERED address (0x..01 / token_transfers) ==="
BODY_COVERED='{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["token_transfers"]}'
W=$(window_start); sleep 1
c=$(curl -s -b "$RA" -o "$EV/w1-preview.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4ch-w1-$TS" -d "$BODY_COVERED")
sleep 3
echo "w1_http=$c missing_blocks=$(jget < "$EV/w1-preview.json" "['missing_blocks']") coverage_ratio=$(jget < "$EV/w1-preview.json" "['coverage']['overall_ratio']")"
window_dump "$W"

echo "=== W2 preview with an UNCOVERED address (0x..00ff / token_transfers) ==="
BODY_GAP='{"chain":"bsc","addresses":["0x00000000000000000000000000000000000000ff"],"datasets":["token_transfers"]}'
W=$(window_start); sleep 1
c=$(curl -s -b "$RA" -o "$EV/w2-preview.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4ch-w2-$TS" -d "$BODY_GAP")
sleep 3
echo "w2_http=$c missing_blocks=$(jget < "$EV/w2-preview.json" "['missing_blocks']") coverage_ratio=$(jget < "$EV/w2-preview.json" "['coverage']['overall_ratio']")"
echo "w2_estimate_basis=$(jget < "$EV/w2-preview.json" "['estimate']['basis']")"
window_dump "$W"

echo "=== W3 preview with the UNCOVERED address and the transactions dataset ==="
BODY_GAP2='{"chain":"bsc","addresses":["0x00000000000000000000000000000000000000ff"],"datasets":["transactions"]}'
W=$(window_start); sleep 1
c=$(curl -s -b "$RA" -o "$EV/w3-preview.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4ch-w3-$TS" -d "$BODY_GAP2")
sleep 3
echo "w3_http=$c missing_blocks=$(jget < "$EV/w3-preview.json" "['missing_blocks']")"
window_dump "$W"

echo "=== invariants ==="
cleanup
echo "production_api_sha_after=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready_after=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"
echo "port_released=$(( $(ss -ltn 2>/dev/null | grep -c ":$PORT ") ))"
echo "=== summary ==="
echo "PROBE_COMPLETE=1"
exit 0
