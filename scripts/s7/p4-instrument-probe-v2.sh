#!/usr/bin/env bash
# p4-instrument-probe-v2.sh
#
# Two open questions from the frozen v2 side-effect gate, answered by
# measurement instead of by argument.
#
# (1) ClickHouse. v2 drove its ClickHouse round with POST /api/v2/downloads/preview
#     and an address whose coverage was complete, so the handler never reached
#     downloadEstimate and the instrument never moved. The handler only issues a
#     ClickHouse query when missing_blocks > 0, and missing_blocks is only
#     computed when the request carries a BOUNDED range (wantInterval requires
#     range.type=block with both block numbers). This probe tests exactly that:
#     an uncovered address plus an explicit block range.
#
# (2) Provider. v2 drove its provider round with POST /api/v2/downloads/validate-addresses
#     and measured a zero delta. This probe measures the candidate own provider
#     counters on the provider-classified write routes AND on one route that is
#     known to make real outbound provider calls (GET /api/v1/addresses/{chain}/{address}/runtime
#     calls Head.Head + AddressType.Classify + BalanceRead.Native), so the
#     instrument itself is proven live before any zero is believed.
#
# ASCII only. Prints no credential, Cookie, CSRF token or DSN.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
PORT=18193
DELAY_MS=4000
BIN=${BIN:-$R/backend/bin.new/api-expcap-r3-calib}
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-instrprobe-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/probe-console.log") 2>&1

API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
chq(){ docker exec -i deploy-clickhouse-1 clickhouse-client --query "$1" 2>/dev/null | tr -d '[:space:]'; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
metric_sum(){ curl -s "$CALAPI/metrics" 2>/dev/null | awk -v m="$1" '$1 ~ ("^" m) { s += $2 } END { printf "%d", s + 0 }'; }
provider_calls(){ echo $(( $(metric_sum ankr_request_duration_seconds_count) + $(metric_sum rpc_batch_calls_total) )); }
ch_since(){
  chq "SELECT count() FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select'"
}
ch_since_like(){
  chq "SELECT count() FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select' AND positionCaseInsensitive(query,'$2') > 0"
}
ch_dump(){
  docker exec -i deploy-clickhouse-1 clickhouse-client --query "SELECT event_time, substring(replaceAll(query,char(10),' '),1,100) FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select' ORDER BY event_time FORMAT TSV" 2>&1
}
wstart(){ date -u '+%Y-%m-%d %H:%M:%S'; }

echo "ts_utc=$TS probe_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "production_api_pid=$AP production_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"

echo "=== fixture ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 instrument probe" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL); INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
OWNEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_OWN_EMAIL);  OWNPW=$(read_cred "$CRED" C1MEM_C1LAT_OWN_PW)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL);  SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
echo "credentials_read inv=$([ -n "$INVEMAIL" ] && echo yes || echo no) own=$([ -n "$OWNEMAIL" ] && echo yes || echo no) sec=$([ -n "$SECEMAIL" ] && echo yes || echo no)"
INVPW1="Pw-$TS-instr-3f21"; SECPW1="Pw-$TS-instr-88a0"; OWNPPW1="Pw-$TS-instr-1a7c"

clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" aud="${5:-user}" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$WSA\"}")
  csrf=$(csrf_of "$jar" "cs_${aud}_session_csrf")
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  echo "clear_flag $tag audience=$aud password_change=$c"
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1" user
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1" admin
clear_flag own "$OWNEMAIL" "$OWNPW" "$OWNPPW1" admin
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json

login_ws(){
  local j=$1 email=$2 pw=$3 ws=$4 aud=$5 out=$6
  rm -f "$j"
  curl -s -c "$j" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$ws\"}"
}
RA=$EV/inv.jar
echo "investigator_user_login=$(login_ws "$RA" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/inv-login.json")"
INV_CSRF=$(csrf_of "$RA" cs_user_session_csrf)
OA=$EV/own-admin.jar
echo "owner_admin_login=$(login_ws "$OA" "$OWNEMAIL" "$OWNPPW1" "$WSA" admin "$EV/own-login.json")"
OWN_CSRF=$(csrf_of "$OA" cs_admin_session_csrf)
OU=$EV/own-user.jar
echo "owner_user_login=$(login_ws "$OU" "$OWNEMAIL" "$OWNPPW1" "$WSA" user "$EV/own-user-login.json")"
OWNU_CSRF=$(csrf_of "$OU" cs_user_session_csrf)

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
echo "candidate_ready=$ready pid=$cand_pid"
if [ "$ready" != "1" ]; then echo "PROBE_ABORT=candidate_not_ready"; exit 0; fi
echo "provider_metric_names:"
curl -s "$CALAPI/metrics" | awk '$1 ~ /^(ankr|rpc_batch|provider|chainbase)/ { print "  " $1 }' | sort -u | head -30
echo "provider_calls_at_start=$(provider_calls)"

echo "=== A: preview, uncovered address, BOUNDED block range (the ClickHouse driver candidate) ==="
BODY_A='{"chain":"bsc","addresses":["0x00000000000000000000000000000000000000ff"],"datasets":["token_transfers"],"range":{"type":"block","from_block":1000000,"to_block":2000000}}'
W=$(wstart); sleep 1; CH0=$(ch_since_like "$W" 'token_transfers FINAL')
c=$(curl -s -b "$RA" -o "$EV/a-preview.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4ip-a-$TS" -d "$BODY_A")
sleep 3; CH1=$(ch_since_like "$W" 'token_transfers FINAL')
echo "a_http=$c missing_blocks=$(jget < "$EV/a-preview.json" "['data']['missing_blocks']") estimate_basis=$(jget < "$EV/a-preview.json" "['data']['estimate']['basis']") est_rows=$(jget < "$EV/a-preview.json" "['data']['estimate']['estimated_rows']") ch_delta=$(( CH1 - CH0 ))"
ch_dump "$W"

echo "=== B: preview, covered address, bounded range that is fully covered (must NOT query ClickHouse) ==="
BODY_B='{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["token_transfers"],"range":{"type":"block","from_block":1000000,"to_block":2000000}}'
W=$(wstart); sleep 1; CH0=$(ch_since_like "$W" 'token_transfers FINAL')
c=$(curl -s -b "$RA" -o "$EV/b-preview.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4ip-b-$TS" -d "$BODY_B")
sleep 3; CH1=$(ch_since_like "$W" 'token_transfers FINAL')
echo "b_http=$c missing_blocks=$(jget < "$EV/b-preview.json" "['data']['missing_blocks']") ch_delta=$(( CH1 - CH0 ))"
ch_dump "$W"

echo "=== C: PROVIDER INSTRUMENT CALIBRATION - GET /api/v1/addresses/bsc/{addr}/runtime calls Head + AddressType + BalanceRead ==="
PC0=$(provider_calls)
c=$(curl -s -b "$OU" -o "$EV/c-runtime.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/addresses/bsc/0x0000000000000000000000000000000000000001/runtime" -H 'X-Auth-Audience: user')
sleep 5
PC1=$(provider_calls)
echo "c_http=$c provider_calls=$PC0->$PC1 delta=$(( PC1 - PC0 ))"
head -c 300 "$EV/c-runtime.json"; echo

echo "=== D: provider-classified write route - POST /api/v2/downloads/validate-addresses ==="
BODY_D='{"chain":"bsc","items":[{"row":1,"address":"0x0000000000000000000000000000000000000002"},{"row":2,"address":"0x0000000000000000000000000000000000000003"}]}'
PC0=$(provider_calls)
c=$(curl -s -b "$RA" -o "$EV/d-validate.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/validate-addresses" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -d "$BODY_D")
sleep 5
PC1=$(provider_calls)
echo "d_http=$c provider_calls=$PC0->$PC1 delta=$(( PC1 - PC0 ))"

echo "=== E: provider-classified write route - POST /api/v1/admin/tokens/bsc/{contract}/resolve ==="
PC0=$(provider_calls)
c=$(curl -s -b "$OA" -o "$EV/e-token.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v1/admin/tokens/bsc/0x0000000000000000000000000000000000000009/resolve" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $OWN_CSRF" -d '{}')
sleep 5
PC1=$(provider_calls)
echo "e_http=$c provider_calls=$PC0->$PC1 delta=$(( PC1 - PC0 ))"

echo "=== F: idle control window (no request at all) ==="
W=$(wstart); PC0=$(provider_calls); CH0=$(ch_since "$W"); sleep 12; PC1=$(provider_calls); CH1=$(ch_since "$W")
echo "f_idle_12s provider_delta=$(( PC1 - PC0 )) ch_select_delta=$(( CH1 - CH0 ))"
ch_dump "$W"

echo "=== invariants ==="
cleanup
echo "production_api_sha_after=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready_after=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"
echo "port_released=$(ss -ltn 2>/dev/null | grep -c ":$PORT ")"
echo "=== summary ==="
echo "PROBE2_COMPLETE=1"
exit 0
