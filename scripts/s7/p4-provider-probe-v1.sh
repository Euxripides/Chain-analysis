#!/usr/bin/env bash
# p4-provider-probe-v1.sh
#
# Is the provider outbound-call instrument live, and can any commit-gated WRITE
# route move it? The frozen v2 gate measured a zero delta on
# POST /api/v2/downloads/validate-addresses and could not tell "the route makes
# no outbound call" apart from "the counter is dead". This probe separates the
# two: it first calibrates the counter on a route that is known to call the
# provider readers, then measures the write routes with the same instrument.
#
# Counter = the candidate own Prometheus series the Ankr/RPC adapters increment
# per outbound request (ankr_request_duration_seconds_count + rpc_batch_calls_total).
#
# ASCII only. Prints no credential, Cookie, CSRF token or DSN.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
PORT=18194
DELAY_MS=4000
BIN=${BIN:-$R/backend/bin.new/api-expcap-r3-calib}
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-provprobe-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/probe-console.log") 2>&1

API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
metric_sum(){ curl -s "$CALAPI/metrics" 2>/dev/null | awk -v m="$1" '$1 ~ ("^" m) { s += $2 } END { printf "%d", s + 0 }'; }
provider_calls(){ echo $(( $(metric_sum ankr_request_duration_seconds_count) + $(metric_sum rpc_batch_calls_total) )); }
ptable(){
  echo "  series: ankr_request_duration_seconds_count=$(metric_sum ankr_request_duration_seconds_count) rpc_batch_calls_total=$(metric_sum rpc_batch_calls_total)"
}
# A fresh address the process can never have cached: the runtime route has to ask
# the provider for eth_getCode / eth_getBalance for it.
FRESH=0x$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-40)
echo "fresh_address=$FRESH"

echo "ts_utc=$TS probe_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "production_api_pid=$AP production_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 provider probe" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL); INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
OWNEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_OWN_EMAIL);  OWNPW=$(read_cred "$CRED" C1MEM_C1LAT_OWN_PW)
INVPW1="Pw-$TS-prov-3f21"; OWNPPW1="Pw-$TS-prov-1a7c"
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
clear_flag own "$OWNEMAIL" "$OWNPW" "$OWNPPW1" admin
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json
login_ws(){
  local j=$1 email=$2 pw=$3 ws=$4 aud=$5 out=$6
  rm -f "$j"
  curl -s -c "$j" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$ws\"}"
}
RA=$EV/inv.jar; echo "inv_user_login=$(login_ws "$RA" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/inv-login.json")"
INV_CSRF=$(csrf_of "$RA" cs_user_session_csrf)
OA=$EV/own.jar;  echo "own_admin_login=$(login_ws "$OA" "$OWNEMAIL" "$OWNPPW1" "$WSA" admin "$EV/own-login.json")"
OWN_CSRF=$(csrf_of "$OA" cs_admin_session_csrf)
OU=$EV/ownu.jar; echo "own_user_login=$(login_ws "$OU" "$OWNEMAIL" "$OWNPPW1" "$WSA" user "$EV/ownu-login.json")"
OWNU_CSRF=$(csrf_of "$OU" cs_user_session_csrf)

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
echo "provider_calls_at_start=$(provider_calls)"; ptable

echo "=== K0: settle 20 s (background rate of the candidate own provider pollers) ==="
P0=$(provider_calls); sleep 20; P1=$(provider_calls)
echo "k0_idle_20s delta=$(( P1 - P0 ))"; ptable

echo "=== K1: CALIBRATION GET /api/v1/addresses/bsc/{fresh}/runtime (chain readers, cache miss) ==="
P0=$(provider_calls)
c=$(curl -s -b "$OU" -o "$EV/k1.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/addresses/bsc/$FRESH/runtime" -H 'X-Auth-Audience: user')
sleep 6; P1=$(provider_calls)
echo "k1_http=$c delta=$(( P1 - P0 ))"; ptable; head -c 240 "$EV/k1.json"; echo

echo "=== K2: same route second time (cache hit must be distinguishable) ==="
P0=$(provider_calls)
c=$(curl -s -b "$OU" -o "$EV/k2.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/addresses/bsc/$FRESH/runtime" -H 'X-Auth-Audience: user')
sleep 6; P1=$(provider_calls)
echo "k2_http=$c delta=$(( P1 - P0 ))"; ptable

echo "=== K3: WRITE route POST /api/v2/downloads/validate-addresses (provider-classified) ==="
BODY='{"chain":"bsc","items":[{"row":1,"address":"0x0000000000000000000000000000000000000002"},{"row":2,"address":"'$FRESH'"}]}'
P0=$(provider_calls)
c=$(curl -s -b "$RA" -o "$EV/k3.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/validate-addresses" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -d "$BODY")
sleep 6; P1=$(provider_calls)
echo "k3_http=$c delta=$(( P1 - P0 ))"; ptable

echo "=== K4: WRITE route POST /api/v1/admin/tokens/bsc/{fresh}/resolve (provider-classified) ==="
P0=$(provider_calls)
c=$(curl -s -b "$OA" -o "$EV/k4.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v1/admin/tokens/bsc/$FRESH/resolve" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $OWN_CSRF" -d '{}')
sleep 6; P1=$(provider_calls)
echo "k4_http=$c delta=$(( P1 - P0 ))"; ptable

echo "=== K5: WRITE route POST /api/v1/admin/methods/signatures (provider-classified) ==="
P0=$(provider_calls)
c=$(curl -s -b "$OA" -o "$EV/k5.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v1/admin/methods/signatures" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -H "X-CSRF-Token: $OWN_CSRF" -d '{"signature":"p4probe(uint256,address)","chain":"bsc"}')
sleep 6; P1=$(provider_calls)
echo "k5_http=$c delta=$(( P1 - P0 ))"; ptable

echo "=== K6: WRITE route PATCH /api/admin/v1/providers/ankr-multichain (provider-classified) ==="
REV=$(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "select revision from provider_descriptors where provider_key='ankr-multichain'" | tr -d '[:space:]')
P0=$(provider_calls)
c=$(curl -s -b "$OA" -o "$EV/k6.json" -w '%{http_code}' -m 120 \
  -X PATCH "$CALAPI/api/admin/v1/providers/ankr-multichain" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -H "X-CSRF-Token: $OWN_CSRF" -H "If-Match: $REV" -d '{"reason":"p4 provider instrument probe","priority":1}')
sleep 6; P1=$(provider_calls)
echo "k6_http=$c revision_before=$REV delta=$(( P1 - P0 ))"; ptable; head -c 240 "$EV/k6.json"; echo

echo "=== invariants ==="
cleanup
echo "production_api_sha_after=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready_after=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"
echo "port_released=$(ss -ltn 2>/dev/null | grep -c ":$PORT ")"
echo "=== summary ==="
echo "PROVIDER_PROBE_COMPLETE=1"
exit 0
