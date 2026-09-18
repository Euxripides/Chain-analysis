#!/usr/bin/env bash
# c1-auth007-response-bound-gate-v1.sh
#
# P1 negative calibration for the commit-gate response bound.
#
# The defect under test: the first implementation buffered the handler response
# up to 256 KiB and then STREAMED everything past that bound to the client. If a
# scope mutation landed while such a handler ran, the client already held a
# success (status + bytes) that the database was about to roll back.
#
# The scenario: a gated handler that returns more than 256 KiB, delayed while it
# runs, with a workspace switch landing inside the delay.
#
#   EXPECT=old  the frozen pre-fix candidate must EXPOSE the inconsistency:
#               a 2xx status and more than 256 KiB of body, with ZERO committed
#               business rows and no success audit row.
#   EXPECT=new  the fixed candidate must produce ONE consistent result: a 409
#               refusal, no success bytes at all, zero business rows, exactly one
#               deny audit row, and no spool file left behind.
#
# ASCII only. No credential, cookie, csrf token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
EXPECT="${EXPECT:-new}"
LABEL="${LABEL:-respbound-$EXPECT}"
PORT="${PORT:-18143}"
HANDLER_MS="${HANDLER_MS:-12000}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
BOUND_BYTES="${BOUND_BYTES:-262144}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
now_ms(){ date +%s%3N; }
spool_count(){ ls -1 /tmp/scope-commit-gate-*.spool 2>/dev/null | wc -l; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL expect=$EXPECT port=$PORT handler_ms=$HANDLER_MS bound_bytes=$BOUND_BYTES"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"

echo "=== fixtures (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 commit gate latency acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
INVPW1="Pw-${TS}-c1aeinv-71c4"
jar="$EV/clear-inv.jar"
c=$(curl -s -c "$jar" -o "$EV/clear-inv.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "investigator fixture logs in with its initial password"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-inv-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$INVPW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "investigator initial-password flag cleared"; else bad "investigator initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear-inv*.json

W=$EV/writer.jar
rm -f "$W"
c=$(curl -s -c "$W" -o "$EV/writer-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "writer logs in to workspace A"
WSID=$(sess_id "$W" user)
WCSRF=$(csrf_of "$W" cs_user_session_csrf)
if [ -z "$WSID" ] || [ "$WSID" = "__ERR__" ]; then bad "writer session id not resolvable"; exit 0; fi

echo "=== the >$BOUND_BYTES byte request payload ==="
python3 - "$EV/payload.json" "$TS" <<'PY'
import json, sys
pad = "x" * 980
groups = [{"index": i, "pad": pad} for i in range(320)]
json.dump({"name": "A7BIG-" + sys.argv[2], "groups": groups}, open(sys.argv[1], "w"))
PY
PAYLOAD_BYTES=$(wc -c < "$EV/payload.json")
echo "payload_bytes=$PAYLOAD_BYTES"
if [ "$PAYLOAD_BYTES" -gt "$BOUND_BYTES" ]; then ok "the calibration payload is larger than the retractable bound"; else bad "the calibration payload must exceed the bound (got $PAYLOAD_BYTES)"; fi

calib_pid=""
cleanup(){
  if [ -n "$calib_pid" ] && kill -0 "$calib_pid" 2>/dev/null; then
    kill -TERM "$calib_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$calib_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$calib_pid" 2>/dev/null || true
    sleep 1
  fi
  calib_pid=""
}
trap cleanup EXIT

start_calib(){
  cleanup
  chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$PORT"
    export SCOPE_CALIB_ENABLE=1
    export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
    export SCOPE_CALIB_PHASE=post
    unset SCOPE_CALIB_ROUTE
    nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
    echo $! > "$EV/calib.pid" )
  sleep 1
  calib_pid=$(cat "$EV/calib.pid")
  local ready=0 rc=""
  for _ in $(seq 1 40); do
    rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "calibration instance ready on $PORT (http $rc)"
  chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process is the binary under test"
}

start_calib
CALRSS0=$(ps -o rss= -p "$calib_pid" | tr -d ' ')
echo "calib_rss_kb_before=$CALRSS0"
SPOOL0=$(spool_count)
chk "$SPOOL0" "0" "no response spool file exists before the gate"

# ---- the case the canvas belongs to (gated write, committed) ----
CNAME="A7BIGCASE-$TS"
CCODE=$(curl -s -b "$W" -o "$EV/case.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $WCSRF" -d "{\"case_no\":\"$CNAME\",\"name\":\"$CNAME\"}")
chk "$CCODE" "201" "the parent case was created (delayed gated write, committed)"
CASEID=$(jget < "$EV/case.json" "['data']['id']")
echo "case_id=$CASEID"
if [ -z "$CASEID" ] || [ "$CASEID" = "__ERR__" ]; then bad "case id not resolvable"; exit 0; fi

echo "=== positive control: the same request with NO scope change must commit and deliver the whole body ==="
PCODE=$(curl -s -b "$W" -o "$EV/pos-body.bin" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $WCSRF" --data-binary @"$EV/payload.json")
PSIZE=$(wc -c < "$EV/pos-body.bin")
echo "positive_control http=$PCODE downloaded_bytes=$PSIZE"
if [ "$PCODE" = "201" ] || [ "$PCODE" = "200" ]; then ok "positive control committed ($PCODE)"; else bad "positive control must commit (got $PCODE)"; fi
if [ "$PSIZE" -gt "$BOUND_BYTES" ]; then ok "positive control delivered more than the retractable bound ($PSIZE > $BOUND_BYTES)"; else bad "positive control must deliver more than the bound (got $PSIZE)"; fi
chk "$(pgq <<SQL
SELECT count(*) FROM investigation_canvases WHERE case_id='$CASEID'::uuid;
SQL
)" "1" "positive control committed exactly one canvas row"

echo "=== negative control: the same request with a workspace switch inside the handler ==="
BEFORE_CANVAS=$(pgq <<SQL
SELECT count(*) FROM investigation_canvases WHERE case_id='$CASEID'::uuid;
SQL
)
BEFORE_DENY=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/v1/cases/%/canvases'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
curl -s -b "$W" -o "$EV/neg-body.bin" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $WCSRF" --data-binary @"$EV/payload.json" > "$EV/neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
M0=$(now_ms)
SWCODE=$(curl -s -m 120 -b "$W" -c "$W" -o "$EV/switch.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $WCSRF" -d "{\"workspace_id\":\"$WSB\"}")
M1=$(now_ms); SWMS=$(( M1 - M0 ))
wait $NPID
NCODE=$(cat "$EV/neg-code.txt" 2>/dev/null || echo n/a)
NSIZE=$(wc -c < "$EV/neg-body.bin")
AFTER_CANVAS=$(pgq <<SQL
SELECT count(*) FROM investigation_canvases WHERE case_id='$CASEID'::uuid;
SQL
)
AFTER_DENY=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/v1/cases/%/canvases'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
SUCCESS_AUDIT=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='canvas.create' AND result='success' AND session_id='$WSID'::uuid
  AND occurred_at > now() - interval '3 minutes';
SQL
)
SPOOL_LEFT=$(spool_count)
CALRSS1=$(ps -o rss= -p "$calib_pid" | tr -d ' ')
NERR=$(jget < "$EV/neg-body.bin" "['error']['code']" 2>/dev/null || echo "-")

echo "negative_control http=$NCODE body_bytes=$NSIZE case_error=$NERR switch=$SWCODE switch_ms=$SWMS"
echo "canvas_rows before=$BEFORE_CANVAS after=$AFTER_CANVAS deny_rows_delta=$(( AFTER_DENY - BEFORE_DENY )) success_audit=$SUCCESS_AUDIT"
echo "spool_files_left=$SPOOL_LEFT calib_rss_kb_before=$CALRSS0 calib_rss_kb_after=$CALRSS1"
chk "$SWCODE" "200" "the workspace switch itself succeeded"
if [ "$SWMS" -le "$THRESHOLD_MS" ]; then ok "the switch stayed within AUTH-007 ($SWMS ms <= $THRESHOLD_MS ms)"; else bad "the switch took $SWMS ms"; fi
chk "$AFTER_CANVAS" "$BEFORE_CANVAS" "the rolled-back canvas did not commit a business row"
chk "$SUCCESS_AUDIT" "0" "no success audit row exists for the rolled-back canvas"
chk "$SPOOL_LEFT" "0" "no response spool file is left behind"

if [ "$EXPECT" = "old" ]; then
  echo "=== negative calibration of the PRE-FIX candidate: the inconsistency must be visible ==="
  case "$NCODE" in 2*) ok "PRE-FIX: the caller was handed a success status ($NCODE) for a write that was rolled back";; *) bad "PRE-FIX: expected a success status to be exposed, got $NCODE";; esac
  if [ "$NSIZE" -gt "$BOUND_BYTES" ]; then ok "PRE-FIX: the caller received $NSIZE bytes it cannot un-see"; else bad "PRE-FIX: expected more than $BOUND_BYTES bytes on the wire, got $NSIZE"; fi
  echo "OLD_INCONSISTENT=1 old_http=$NCODE old_bytes=$NSIZE old_rows_before=$BEFORE_CANVAS old_rows_after=$AFTER_CANVAS"
else
  echo "=== the FIXED candidate must produce one consistent result ==="
  chk "$NCODE" "409" "the gated write was refused with 409, not committed under the stale scope"
  chk "$NERR" "AUTHZ_WORKSPACE_SCOPE_STALE" "the refusal carries the stable scope code"
  if [ "$NSIZE" -lt "$BOUND_BYTES" ]; then ok "the refusal body stayed far below the retractable bound ($NSIZE bytes)"; else bad "the refusal body is $NSIZE bytes"; fi
  chk "$(( AFTER_DENY - BEFORE_DENY ))" "1" "exactly one detached deny audit row (exactly-once)"
  echo "NEW_CONSISTENT=1 new_http=$NCODE new_bytes=$NSIZE new_error=$NERR"
fi

chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
