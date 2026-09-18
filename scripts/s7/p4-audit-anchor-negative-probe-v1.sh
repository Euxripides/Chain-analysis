#!/usr/bin/env bash
# p4-audit-anchor-negative-probe-v1.sh
#
# The route matrix round (p4routes-20260915T033722Z) reported, for route 18
# POST /api/admin/v1/audit/anchors, a NEGATIVE window in which the object counter
# moved by 10. The matrix counter aggregates three buckets, so that number could
# be the worker writing raw objects in the background rather than a leak. This
# probe isolates one route, one bucket at a time, and prints the object NAMES
# that appear while a REFUSED audit-anchor request is in flight.
#
# ASCII only. Prints no credential, Cookie, CSRF token or DSN.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
PORT=18197
BIN="${BIN:-$R/backend/bin.new/api-expcap-r3-calib}"
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-anchorprobe-$TS}
M=/data/blockchain-platform/minio
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/probe-console.log") 2>&1
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
snap(){
  for b in blockchain-audit blockchain-exports blockchain-raw; do
    ls -1R --time-style=full-iso "$M/$b" 2>/dev/null | sed "s|^|$b |" >> "$1"
  done
}
count_bucket(){ ls -1R "$M/$1" 2>/dev/null | grep -c . ; }
WSA_PLACEHOLDER=""
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "production_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 audit anchor probe" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
OWNEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_OWN_EMAIL)
OWNPW=$(read_cred "$CRED" C1MEM_C1LAT_OWN_PW)
OWNPW1="Pw-$TS-anchor-1a7c"
J=$EV/own.jar
c=$(curl -s -c "$J" -o "$EV/own-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"$OWNEMAIL\",\"password\":\"$OWNPW\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
CSRF=$(csrf_of "$J" cs_admin_session_csrf)
c2=$(curl -s -b "$J" -o "$EV/pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CSRF" \
  -d "{\"current_password\":\"$OWNPW\",\"new_password\":\"$OWNPW1\"}")
echo "owner_login=$c password_change=$c2"

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
  export SCOPE_CALIB_DELAY_MS=4000
  if [ -n "${CALIB_ROUTE:-}" ]; then export SCOPE_CALIB_ROUTE="$CALIB_ROUTE"; else unset SCOPE_CALIB_ROUTE; fi
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
[ "$ready" = "1" ] || { echo "PROBE_ABORT=not_ready"; exit 0; }

echo "calib_route=${CALIB_ROUTE:-<all routes>}"
echo "=== POSITIVE: POST /api/admin/v1/audit/anchors with a valid scope ==="
PJ=$EV/pos.jar
c=$(curl -s -c "$PJ" -o "$EV/pos-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"$OWNEMAIL\",\"password\":\"$OWNPW1\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
PCSRF=$(csrf_of "$PJ" cs_admin_session_csrf)
echo "pos_login=$c"
for b in blockchain-audit blockchain-exports blockchain-raw; do echo -n "before_pos $b="; count_bucket "$b"; done
: > "$EV/names-before-pos.txt"; snap "$EV/names-before-pos.txt"
CPOS=$(curl -s -b "$PJ" -o "$EV/pos.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/admin/v1/audit/anchors" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -H "X-CSRF-Token: $PCSRF" -d '{"chain":"audit","reason":"p4 anchor positive"}')
sleep 6
: > "$EV/names-after-pos.txt"; snap "$EV/names-after-pos.txt"
echo "positive_http=$CPOS"
echo "positive_new_names:"; diff "$EV/names-before-pos.txt" "$EV/names-after-pos.txt" | grep '^>' | head -20
for b in blockchain-audit blockchain-exports blockchain-raw; do echo -n "after_pos $b="; count_bucket "$b"; done
head -c 300 "$EV/pos.json"; echo
echo "anchor_rows_before=$(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc 'select count(*) from audit_chain_anchors')"

echo "=== NEGATIVE: same route, scope invalidated while the handler is delayed ==="
NJ=$EV/neg.jar
c=$(curl -s -c "$NJ" -o "$EV/neg-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"$OWNEMAIL\",\"password\":\"$OWNPW1\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
NCSRF=$(csrf_of "$NJ" cs_admin_session_csrf)
echo "neg_login=$c"
: > "$EV/names-before-neg.txt"; snap "$EV/names-before-neg.txt"
for b in blockchain-audit blockchain-exports blockchain-raw; do echo -n "before_neg $b="; count_bucket "$b"; done
curl -s -b "$NJ" -o "$EV/neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/admin/v1/audit/anchors" -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -H "X-CSRF-Token: $NCSRF" -d '{"chain":"audit","reason":"p4 anchor negative"}' > "$EV/neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CL=$(curl -s -b "$NJ" -c "$NJ" -o "$EV/neg-logout.json" -w '%{http_code}' -X POST "$API/api/auth/v1/logout" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $NCSRF" -d '{}')
wait $NPID
sleep 3
: > "$EV/names-after-neg.txt"; snap "$EV/names-after-neg.txt"
RCODE=$(cat "$EV/neg-code.txt")
echo "neg_logout=$CL negative_http=$RCODE"
echo "negative_new_names:"; diff "$EV/names-before-neg.txt" "$EV/names-after-neg.txt" | grep '^>' | head -30
for b in blockchain-audit blockchain-exports blockchain-raw; do echo -n "after_neg $b="; count_bucket "$b"; done
echo "anchor_rows_after=$(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc 'select count(*) from audit_chain_anchors')"
echo "=== invariants ==="
cleanup
echo "production_api_sha_after=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "production_ready_after=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"
echo "port_released=$(ss -ltn 2>/dev/null | grep -c ":$PORT ")"
echo "ANCHOR_PROBE_COMPLETE=1"
exit 0