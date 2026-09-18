#!/usr/bin/env bash
# c1-auth007-scope-epoch-gate-v2.sh -- positive gate for the P1-A / AUTH-007
# optimistic commit token.
#
# The frozen sibling gate c1-scope-commit-latency-gate-v1.sh measures LATENCY
# only. AUTH-007 has two halves and this gate measures the second one:
#
#   S1  a scope mutation that lands while a 12 s gated handler runs must make the
#       handler ROLL BACK: HTTP 409, zero committed business rows, exactly one
#       detached deny audit row and no success audit row.
#   S2  the same 12 s gated handler with NO scope mutation must still COMMIT
#       normally: HTTP 201, exactly one business row and exactly one success row.
#   S3  two administrators revoking each other concurrently must produce a
#       deterministic, non-5xx, deadlock-free result.
#   S4  resource control: no transaction left idle in transaction, no long
#       statement, production API ready and RSS bounded.
#
# The production instance is never restarted by this gate. Fixtures live in the
# dedicated c1lat organisation; the real administrator account is untouched.
# ASCII only, no secret is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-auth007-epoch}"
HANDLER_MS="${HANDLER_MS:-12000}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
PORT="${PORT:-18134}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
now_ms(){ date +%s%3N; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL handler_ms=$HANDLER_MS threshold_ms=$THRESHOLD_MS port=$PORT"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"
chk "$(pgq <<SQL
SELECT count(*) FROM information_schema.columns WHERE table_name='sessions' AND column_name='scope_epoch';
SQL
)" "1" "migration 0059 (sessions.scope_epoch) is applied"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_trigger WHERE tgname='sessions_scope_epoch_bump';
SQL
)" "1" "the scope_epoch trigger is installed"

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
  chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no PENDING/RUNNING export exists before starting the second API process"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$PORT"
    export SCOPE_CALIB_ENABLE=1
    export SCOPE_CALIB_ROUTE=/api/v2/cases
    export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
    export SCOPE_CALIB_PHASE=post
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

echo "=== 0. runtime identity ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "prod_api_pid=$AP"

echo "=== 1. fixtures (dedicated c1lat organisation) ==="
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
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
echo "ws_a=$WSA ws_b=$WSB"
INVPW1="Pw-${TS}-c1aeinv-71c4"
SECPW1="Pw-${TS}-c1aesec-2f83"
clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
  chk "$c" "200" "$tag fixture logs in with its initial password"
  csrf=$(csrf_of "$jar" cs_user_session_csrf)
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  [ "$c" = "200" ] || [ "$c" = "204" ]; chk "$?" "0" "$tag initial-password flag cleared (status=$c)"
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1"
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1"

user_login(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
admin_login(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
    -d "{\"identifier\":\"$SECEMAIL\",\"password\":\"$SECPW1\",\"audience\":\"admin\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }

echo "=== S1: a scope change during a ${HANDLER_MS} ms handler must roll the write back ==="
W=$EV/s1-writer.jar
chk "$(user_login "$W" "$WSA")" "200" "s1 writer logs in to workspace A"
S1SID=$(sess_id "$W" user)
S1CSRF=$(csrf_of "$W" cs_user_session_csrf)
S1NAME="A7S1-$TS-rollback"
BEFORE=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid;
SQL
)
start_calib
curl -s -b "$W" -o "$EV/s1-write.json" -w '%{http_code}' \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $S1CSRF" \
  -d "{\"case_no\":\"$S1NAME\",\"name\":\"$S1NAME\"}" > "$EV/s1-write-code.txt" 2>/dev/null &
WPID=$!
sleep 1
M0=$(now_ms)
SWCODE=$(curl -s -m 120 -b "$W" -c "$W" -o "$EV/s1-switch.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $S1CSRF" -d "{\"workspace_id\":\"$WSB\"}")
M1=$(now_ms)
SWMS=$(( M1 - M0 ))
wait $WPID
WCODE=$(cat "$EV/s1-write-code.txt" 2>/dev/null || echo n/a)
AFTER=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid;
SQL
)
ROWS=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND (case_no='$S1NAME' OR name='$S1NAME');
SQL
)
chk "$SWCODE" "200" "s1 workspace switch succeeded"
if [ "$SWMS" -le "$THRESHOLD_MS" ]; then ok "s1 switch stayed within AUTH-007 ($SWMS ms <= $THRESHOLD_MS ms)"; else bad "s1 switch took $SWMS ms (> $THRESHOLD_MS ms)"; fi
chk "$WCODE" "409" "s1 the gated write was refused with 409, not committed under the stale scope"
chk "$ROWS" "0" "s1 zero business rows for the refused case (transaction rolled back)"
chk "$AFTER" "$BEFORE" "s1 the workspace case count is unchanged"
chk "$(jget < "$EV/s1-write.json" "['error']['code']" 2>/dev/null || echo x)" "AUTHZ_WORKSPACE_SCOPE_STALE" "s1 the refusal carries AUTHZ_WORKSPACE_SCOPE_STALE"
chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template='/api/v2/cases'
  AND session_id='$S1SID'::uuid AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)" "1" "s1 exactly one detached deny audit row for the rolled back commit"
chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE session_id='$S1SID'::uuid AND result='success' AND route_template='/api/v2/cases';
SQL
)" "0" "s1 no success audit row exists for the rolled back write"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "s1 production API is still ready"
cleanup

echo "=== S2: the same handler with no scope change must commit normally ==="
W2=$EV/s2-writer.jar
chk "$(user_login "$W2" "$WSA")" "200" "s2 writer logs in to workspace A"
S2SID=$(sess_id "$W2" user)
S2CSRF=$(csrf_of "$W2" cs_user_session_csrf)
S2NAME="A7S2-$TS-commit"
start_calib
S0=$(now_ms)
C2=$(curl -s -m 60 -b "$W2" -o "$EV/s2-write.json" -w '%{http_code}' \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $S2CSRF" \
  -d "{\"case_no\":\"$S2NAME\",\"name\":\"$S2NAME\"}")
S1T=$(now_ms)
SCHK=$(( S1T - S0 ))
chk "$C2" "201" "s2 the gated write committed (http $C2 after $SCHK ms)"
chk "$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND case_no='$S2NAME';
SQL
)" "1" "s2 exactly one committed business row (atomic, not duplicated)"
chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE session_id='$S2SID'::uuid AND route_template='/api/v2/cases' AND result='success';
SQL
)" "1" "s2 exactly one success audit row for the committed write"
cleanup

echo "=== S3: two administrators revoking each other concurrently ==="
A1=$EV/s3-admin1.jar; A2=$EV/s3-admin2.jar
chk "$(admin_login "$A1" "$WSA")" "200" "s3 admin session 1 logged in"
chk "$(admin_login "$A2" "$WSA")" "200" "s3 admin session 2 logged in"
A1SID=$(sess_id "$A1" admin); A2SID=$(sess_id "$A2" admin)
A1CSRF=$(csrf_of "$A1" cs_admin_session_csrf); A2CSRF=$(csrf_of "$A2" cs_admin_session_csrf)
echo "s3 sessions: a1=$A1SID a2=$A2SID"
D0=$(now_ms)
curl -s -m 60 -b "$A1" -o "$EV/s3-r1.json" -w '%{http_code}' \
  -X POST "$API/api/admin/v1/sessions/$A2SID/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1CSRF" \
  -d '{"confirm":true,"reason":"c1auth007-cross-revoke-1"}' > "$EV/s3-r1-code.txt" 2>/dev/null &
P1=$!
curl -s -m 60 -b "$A2" -o "$EV/s3-r2.json" -w '%{http_code}' \
  -X POST "$API/api/admin/v1/sessions/$A1SID/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A2CSRF" \
  -d '{"confirm":true,"reason":"c1auth007-cross-revoke-2"}' > "$EV/s3-r2-code.txt" 2>/dev/null &
P2=$!
wait $P1; wait $P2
D1=$(now_ms)
R1=$(cat "$EV/s3-r1-code.txt" 2>/dev/null || echo n/a)
R2=$(cat "$EV/s3-r2-code.txt" 2>/dev/null || echo n/a)
DMS=$(( D1 - D0 ))
echo "s3 codes: r1=$R1 r2=$R2 elapsed_ms=$DMS"
case "$R1$R2" in *5*) bad "s3 a cross revoke returned a 5xx (r1=$R1 r2=$R2): the outcome must be deterministic";; *) ok "s3 neither cross revoke returned a 5xx (r1=$R1 r2=$R2)";; esac
if [ "$DMS" -lt 30000 ]; then ok "s3 both concurrent revokes settled without deadlock ($DMS ms)"; else bad "s3 concurrent revokes took $DMS ms: suspected lock wait or deadlock"; fi
WON=0
case "$R1" in 200|204) WON=$((WON+1));; esac
case "$R2" in 200|204) WON=$((WON+1));; esac
REV=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id IN ('$A1SID'::uuid,'$A2SID'::uuid) AND revoked_at IS NOT NULL;
SQL
)
echo "s3 won=$WON revoked=$REV"
if [ "$WON" = "2" ]; then
  chk "$REV" "2" "s3 both cross revokes committed and both sessions are revoked"
else
  # The loser own session was revoked by the winner before the loser reached its
  # own commit check, so the loser write is rolled back. That is the same
  # stale-scope rule the gate applies everywhere, and it is a defined refusal --
  # never a random 500 and never a half-applied revoke.
  chk "$WON" "1" "s3 exactly one cross revoke committed (the other lost its own session first)"
  chk "$REV" "1" "s3 exactly the winning target is revoked; the loser write rolled back"
  if [ "$R1" = "409" ] || [ "$R2" = "409" ]; then ok "s3 the losing cross revoke was refused with the defined 409 scope code"; else bad "s3 the losing cross revoke did not carry the defined 409 refusal (r1=$R1 r2=$R2)"; fi
fi
chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/admin/v1/sessions/%/commands/revoke'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE' AND occurred_at > now() - interval '5 minutes';
SQL
)" "$(( 2 - WON ))" "s3 the refused cross revoke left exactly one deny audit row (exactly-once)"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query ILIKE '%sessions%';
SQL
)" "0" "s3 no session-row lock wait is left behind"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "s3 production API is still ready"

echo "=== S4: resource control ==="
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction';
SQL
)" "0" "no transaction is left idle in transaction"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE state='active' AND now()-query_start > interval '30 seconds';
SQL
)" "0" "no long-running statement is left behind"
RSS_KB=$(awk '/VmRSS/{print $2}' /proc/$AP/status 2>/dev/null || echo 0)
echo "prod_api_rss_kb=$RSS_KB"
if [ "${RSS_KB:-0}" -lt 4194304 ]; then ok "production API RSS is bounded ($RSS_KB kB)"; else bad "production API RSS is $RSS_KB kB"; fi
echo "audit_rows_total=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2;
SQL
)"

echo "=== cleanup ==="
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT has no listener after cleanup"
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary unchanged after the gate"
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_AUTH007_EPOCH_${LABEL}_PASS=1"; else echo "C1_AUTH007_EPOCH_${LABEL}_PASS=0"; fi
