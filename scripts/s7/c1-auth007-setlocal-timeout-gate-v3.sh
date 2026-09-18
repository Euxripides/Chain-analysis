#!/usr/bin/env bash
# c1-auth007-setlocal-timeout-gate-v1.sh
#
# P1-1 / AUTH-007 residual: the SET LOCAL bounds of the commit-gate transaction.
#
# The gate transaction applies, with set_config(..., is_local=true):
#   lock_timeout                        SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS      (default 8 s)
#   statement_timeout                   SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS (default 60 s)
#   idle_in_transaction_session_timeout SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS   (default 120 s)
#   application_name                    bpgate:<request id>
# and the documentation claims all four are real. This gate stops claiming and measures:
#
#   A. LOCK TIMEOUT. An external transaction holds FOR UPDATE on the caller session row,
#      so the release re-read has to wait. The request must be refused, fail closed and
#      commit nothing, at the configured budget -- not at the 60 s statement timeout and
#      not after an unbounded wait.
#   B. STATEMENT TIMEOUT. An external transaction holds ACCESS EXCLUSIVE on public.cases,
#      so the handler statement itself has to wait. lock_timeout is set far above
#      statement_timeout so the statement bound is the one that has to fire.
#   C. IDLE IN TRANSACTION. The calibration build delays the request inside the gate
#      critical section, so the transaction stops doing work while it is open. PostgreSQL
#      must terminate that backend at the configured idle bound, and the request must
#      still fail closed with nothing committed.
#   D. CORRELATION. While A and C are waiting, pg_stat_activity must show
#      application_name = 'bpgate:<request id>' for the request under test.
#   E. BUDGET. The long admin routes are measured with the real default budget, and the
#      statement bound is then lowered below the measured value to prove the bound
#      governs those routes instead of a global 60 s blind cut.
#
# WHY v2 EXISTS (proven from the preserved first run, evidence
# c1-auth007-setlocal-r1-20260914T162756Z)
# ---------------------------------------------------------------------------
# v1 asserted the login INSIDE new_session(), whose whole stdout is captured by
# the caller with SIDA=$(new_session ...). The assertion therefore landed in the
# variable, so session_a was the two lines
#
#     PASS login for .../A.jar
#     1931f407-cf5d-492d-8d8c-84a9376796b1
#
# and the external transaction that section A builds from that id was
#
#     SELECT 1 FROM sessions WHERE id='PASS login for .../A.jar
#     1931f407-...' FOR UPDATE
#
# which is not a valid uuid: psql failed, no row lock was ever taken, the gate
# then measured an unblocked request (201 in 53 ms) and eight assertions cascaded
# from a harness defect. The same shape is used by sections B and C. v2 never
# asserts inside a captured function: it returns a session id or the marker
# __ERR__, and every caller states the login assertion itself. The v1 run is kept
# as evidence and is not rewritten.
#
# WHY v3 EXISTS (the v2 round is preserved: c1-auth007-setlocal-r2-20260914T164602Z,
# PASS=40 FAIL=7 NV=1)
# ---------------------------------------------------------------------------
# A, C and D are closed on v2 (lock_timeout really fires at 1500 ms with a real
# PostgreSQL 55P03 and one deny audit row; idle_in_transaction really terminates
# the gate backend; application_name really is bpgate:<request id>). v3 fixes the
# two sections v2 could not settle:
#
#   B. v2 held ACCESS EXCLUSIVE on public.cases BEFORE the request started, so the
#      statement it blocked was the AUTHORIZATION read that runs before the gate
#      opens (application_name empty), not a handler statement. The gate
#      statement_timeout governs only statements inside the gate transaction, so
#      the assertion was aimed at the wrong object and the 201/8028 ms was a gate
#      design defect, not a product failure. v3 therefore starts the request
#      FIRST with the calibration seam at phase "post", waits until the request is
#      inside the gate critical section, and only THEN takes ACCESS EXCLUSIVE on
#      cases. The handler INSERT is now the blocked statement and the configured
#      statement_timeout is the only bound that can fire.
#
#   E. v2 logged in with c1lat security_admin, which has admin:audit:verify and
#      admin:killswitch:engage but NOT admin:backup:restore, so
#      /api/admin/v1/restores/preview answered 403 and no budget could be derived;
#      the candidate port had also been taken by another process. v3 adds a
#      platform_admin identity (owner/platform_admin hold all four permissions)
#      and re-checks the port before the candidate starts.
#
# F is new: the availability problem B exposed. A long ACCESS EXCLUSIVE lock on
# public.cases blocks the pre-gate authorization read, and the application pool
# has no statement_timeout, so an ordinary request can be postponed arbitrarily.
# v3 measures that window explicitly instead of leaving it as a remark.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-setlocal3}"
MAIN_BIN="${MAIN_BIN:-$R/backend/bin.new/api-auth007-deny}"
CALIB_BIN="${CALIB_BIN:-$R/backend/bin.new/api-auth007-deny-calib}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0; NV=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
nv(){ NV=$((NV+1)); echo "NOT_VERIFIED $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F"|" -v ON_ERROR_STOP=1; }
now_ms(){ date +%s%3N; }
csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" | tail -1; }
csrf_admin_of(){ awk '$6=="cs_admin_session_csrf"{print $7}' "$1" | tail -1; }
case_count(){ pgq <<SQL
SELECT count(*) FROM cases WHERE name LIKE 'SLT-$TS%';
SQL
}
bpgate_count(){ pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
}
audit_row(){ pgq <<SQL
SELECT count(*) || '|' || coalesce(min(error_code),'') || '|' || coalesce(min(deny_reason),'') || '|' || coalesce(min(detail->>'gate_error'),'')
FROM audit_events_v2 WHERE request_id='$1' AND action='authorization.denied';
SQL
}

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d" " -f1)"
echo "label=$LABEL main_bin=$MAIN_BIN calib_bin=$CALIB_BIN"
echo "main_sha=$(sha256sum "$MAIN_BIN" | cut -d" " -f1)"
echo "calib_sha=$(sha256sum "$CALIB_BIN" 2>/dev/null | cut -d" " -f1)"
echo "evidence=$EV"

echo "=== 0. identity ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d" " -f1)" "$API_EXPECT_SHA" "production API binary is unchanged before this gate starts"

BIN=$MAIN_BIN
cand_pid=""; cand_port=""
cleanup(){
  if [ -n "$cand_pid" ] && kill -0 "$cand_pid" 2>/dev/null; then
    kill -TERM "$cand_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$cand_pid" 2>/dev/null || true
  fi
  cand_pid=""
}
trap cleanup EXIT

start_cand(){
  cand_port=$1; local log=$2; shift 2
  chk "$(ss -ltn 2>/dev/null | grep -c ":$cand_port ")" "0" "port $cand_port is free before start ($log)"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$cand_port"
    unset SCOPE_CALIB_ENABLE
    for kv in "$@"; do export "$kv"; done
    nohup setsid "$BIN" >> "$EV/$log" 2>&1 &
    echo $! > "$EV/$log.pid" )
  sleep 1
  cand_pid=$(cat "$EV/$log.pid")
  local ready=0 rc=""
  for _ in $(seq 1 180); do
    rc=$(curl -s -o "$EV/$log-ready.json" -w '%{http_code}' "http://127.0.0.1:$cand_port/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "candidate ready on $cand_port ($log, http $rc)"
  chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d" " -f1)" "$(sha256sum "$BIN" | cut -d" " -f1)" "running candidate is $BIN"
}
stop_cand(){
  cleanup
  chk "$(ss -ltn 2>/dev/null | grep -c ":$cand_port ")" "0" "candidate port $cand_port is released"
}
hold_lock(){
  ( docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1 -c "$2" ) > "$1" 2>&1 &
  echo $!
}
sampler(){
  ( for _ in $(seq 1 120); do
      pgq <<SQL 2>/dev/null
SELECT 'A|' || application_name || '|' || state || '|' || coalesce(wait_event_type,'') || '|' || coalesce(wait_event,'')
FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
      sleep 0.1
    done ) > "$1" 2>&1 &
  echo $!
}

echo "=== 1. fixture ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 set local timeout acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities platform_admin:c1lat-plat@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
PLATEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_PLAT_EMAIL)
PLATPW=$(read_cred "$CRED" C1MEM_C1LAT_PLAT_PW)
IID=$(pgq <<SQL
SELECT id::text FROM identities WHERE email='$INVEMAIL';
SQL
)
echo "ws_a=$WSA identity=$IID"
if [ -z "$IID" ] || [ "$IID" = "__ERR__" ]; then bad "fixture identity not resolvable"; exit 0; fi

PW1="Pw-$TS-c1slt-3c07"
login_one(){
  local jar=$1 email=$2 pw=$3 ws=$4 aud=${5:-user}
  curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H "Content-Type: application/json" -H "X-Auth-Audience: $aud" \
    -d "$(printf '{"identifier":"%s","password":"%s","audience":"%s","workspace_id":"%s"}' "$email" "$pw" "$aud" "$ws")"
}
jar=$EV/clear.jar
c=$(login_one "$jar" "$INVEMAIL" "$INVPW" "$WSA" user)
chk "$c" "200" "fixture identity logs in with its initial password"
csrf=$(csrf_of "$jar")
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-CSRF-Token: $csrf" \
  -d "$(printf '{"current_password":"%s","new_password":"%s"}' "$INVPW" "$PW1")")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared through the real endpoint"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear.json "$EV"/clear-pw.json

PLATPW1="Pw-$TS-c1slt-plat"
platjar=$EV/clear-plat.jar
c=$(login_one "$platjar" "$PLATEMAIL" "$PLATPW" "$WSA" admin)
chk "$c" "200" "platform_admin fixture logs in with its initial password (admin audience)"
csrfplat=$(csrf_admin_of "$platjar")
c=$(curl -s -b "$platjar" -o "$EV/clear-plat-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: admin" -H "X-CSRF-Token: $csrfplat" \
  -d "$(printf '{"current_password":"%s","new_password":"%s"}' "$PLATPW" "$PLATPW1")")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "platform_admin initial-password flag cleared"; else bad "platform_admin initial-password flag cleared (status=$c)"; fi
rm -f "$platjar" "$EV"/clear-plat*.json

# new_session prints EXACTLY ONE token: the session id, or __ERR__. It must not
# assert and must not print anything else, because every caller captures its
# stdout into a variable that is later interpolated into SQL.
new_session(){
  local jar=$1 c sid
  rm -f "$jar"
  c=$(login_one "$jar" "$INVEMAIL" "$PW1" "$WSA" user)
  if [ "$c" != "200" ]; then printf '%s' "__ERR__"; return 0; fi
  sid=$(curl -s -b "$jar" -H "X-Auth-Audience: user" "$API/api/auth/v1/session" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['session_id'])" 2>/dev/null)
  if [ -z "$sid" ]; then printf '%s' "__ERR__"; return 0; fi
  printf '%s' "$sid"
}

echo "=== A. lock_timeout on the release re-read (1500 ms) ==="
BIN=$MAIN_BIN
start_cand 18181 A.log SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=1500 SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=60000 SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=120000
jarA=$EV/A.jar
SIDA=$(new_session "$jarA")
CSRFA=$(csrf_of "$jarA")
echo "session_a=$SIDA"
if [ -z "$SIDA" ] || [ "$SIDA" = "__ERR__" ]; then bad "A: session id not resolvable"; else
  ok "A: the caller session logged in with the reset password"
  BEFORE=$(case_count)
  RIDA="c1slt-$TS-A"
  HOLD=$(hold_lock "$EV/A-hold.log" "BEGIN; SELECT 1 FROM sessions WHERE id='$SIDA' FOR UPDATE; SELECT pg_sleep(9); ROLLBACK;")
  sleep 1
  # The holder is NOT a waiter: pg_sleep leaves it active on its own query text,
  # and the counting query itself contains the session id, so it has to exclude
  # itself. v1 asked for wait_event_type='Lock' and therefore counted 0 even when
  # the lock was held perfectly.
  chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND state='active'
  AND query LIKE '%' || '$SIDA' || '%' AND query LIKE '%FOR UPDATE%';
SQL
)" "1" "A: the external transaction really holds the caller session row lock"
  SAMP=$(sampler "$EV/A-samples.log")
  T0=$(now_ms)
  CODEA=$(curl -s -b "$jarA" -o "$EV/A-body.json" -D "$EV/A.h" -w '%{http_code}' -m 60 \
    -X POST "http://127.0.0.1:18181/api/v2/cases" -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
    -H "X-Request-Id: $RIDA" -H "X-CSRF-Token: $CSRFA" \
    -d "$(printf '{"case_no":"SLT-%s-A","name":"SLT-%s-A","description":"lock timeout fixture"}' "$TS" "$TS")")
  T1=$(now_ms)
  wait "$SAMP" 2>/dev/null
  wait "$HOLD" 2>/dev/null
  AFTER=$(case_count)
  ELA=$(( T1 - T0 ))
  echo "A code=$CODEA elapsed_ms=$ELA created=$(( AFTER - BEFORE ))"
  chk "$CODEA" "409" "A: a lock wait on the caller row is refused with 409"
  grep -q "AUTHZ_WORKSPACE_SCOPE_STALE" "$EV/A-body.json" && ok "A: the refusal carries the stable scope-stale code" || bad "A: the refusal lost AUTHZ_WORKSPACE_SCOPE_STALE"
  if [ "$ELA" -ge 1300 ] && [ "$ELA" -le 6000 ]; then ok "A: the refusal happened at the configured 1500 ms lock bound, not at the 60 s statement bound ($ELA ms)"; else bad "A: lock bound not honoured ($ELA ms)"; fi
  chk "$(( AFTER - BEFORE ))" "0" "A: the refused request committed no case row (fail closed)"
  grep -q "bpgate:$RIDA" "$EV/A-samples.log" && ok "A: pg_stat_activity showed application_name=bpgate:<request id> while the request waited" || bad "A: the waiting backend could not be attributed to the request"
  grep -q "bpgate:$RIDA|.*|Lock|" "$EV/A-samples.log" && ok "A: the attributed backend was observed in a Lock wait" || nv "A: no sample caught the Lock wait window (sampling only)"
  ROW=$(audit_row "$RIDA")
  echo "A_audit=$ROW"
  printf '%s' "$ROW" | grep -q "AUTHZ_WORKSPACE_SCOPE_STALE|session_scope_unverifiable|" && ok "A: the deny audit row is reason session_scope_unverifiable" || bad "A: deny audit row is wrong ($ROW)"
  printf '%s' "$ROW" | grep -qi "lock timeout" && ok "A: the audit gate_error carries the real PostgreSQL lock timeout" || bad "A: gate_error does not carry the lock timeout ($ROW)"
fi
stop_cand

echo "=== B. statement_timeout inside the handler (1500 ms; the lock is taken AFTER the gate opens) ==="
if [ ! -x "$CALIB_BIN" ]; then
  nv "B: calibration binary $CALIB_BIN is missing; the handler statement bound is NOT measured this round"
else
  BIN=$CALIB_BIN
  start_cand 18182 B.log SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=60000 SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=1500 SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=120000 SCOPE_CALIB_ENABLE=1 SCOPE_CALIB_ROUTE=/api/v2/cases SCOPE_CALIB_PHASE=post SCOPE_CALIB_DELAY_MS=9000
  jarB=$EV/B.jar
  SIDB=$(new_session "$jarB")
  CSRFB=$(csrf_of "$jarB")
  if [ -z "$SIDB" ] || [ "$SIDB" = "__ERR__" ]; then bad "B: session id not resolvable"; else
    ok "B: the caller session logged in with the reset password"
    BEFORE=$(case_count)
    RIDB="c1slt-$TS-B"
    SAMP=$(sampler "$EV/B-samples.log")
    T0=$(now_ms)
    # The request goes FIRST, with the seam holding it inside the gate critical
    # section. Only then is the table locked, so the statement that blocks is the
    # handler INSERT and not the authorization read that runs before the gate.
    curl -s -b "$jarB" -o "$EV/B-body.json" -D "$EV/B.h" -w '%{http_code}' -m 90 \
      -X POST "http://127.0.0.1:18182/api/v2/cases" -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
      -H "X-Request-Id: $RIDB" -H "X-CSRF-Token: $CSRFB" \
      -d "$(printf '{"case_no":"SLT-%s-B","name":"SLT-%s-B","description":"statement timeout fixture"}' "$TS" "$TS")" > "$EV/B-code.txt" 2>/dev/null &
    BPID=$!
    sleep 2
    chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND state='active' AND query LIKE '%ACCESS EXCLUSIVE%';
SQL
)" "0" "B: no ACCESS EXCLUSIVE lock is held before the handler starts"
    HOLD=$(hold_lock "$EV/B-hold.log" "BEGIN; LOCK TABLE cases IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(12); ROLLBACK;")
    sleep 1
    chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND state='active' AND query LIKE '%ACCESS EXCLUSIVE%';
SQL
)" "1" "B: the external transaction really holds ACCESS EXCLUSIVE on cases while the request is inside the gate"
    wait $BPID
    T1=$(now_ms)
    wait "$SAMP" 2>/dev/null
    wait "$HOLD" 2>/dev/null
    AFTER=$(case_count)
    CODEB=$(cat "$EV/B-code.txt")
    ELB=$(( T1 - T0 ))
    echo "B code=$CODEB elapsed_ms=$ELB created=$(( AFTER - BEFORE )) calib_delay_ms=9000 statement_bound_ms=1500"
    if [ "$CODEB" = "409" ] || [ "$CODEB" = "500" ] || [ "$CODEB" = "503" ]; then ok "B: a handler statement past the bound is refused fail-closed ($CODEB)"; else bad "B: unexpected status for a timed-out handler statement ($CODEB)"; fi
    if [ "$ELB" -ge 10000 ] && [ "$ELB" -le 16000 ]; then ok "B: the request lasted the 9 s seam plus about the configured 1500 ms statement bound ($ELB ms), not the 60 s lock bound"; else bad "B: statement bound not honoured around the seam ($ELB ms)"; fi
    chk "$(( AFTER - BEFORE ))" "0" "B: the timed-out statement left no case row behind (fail closed)"
    grep -q "bpgate:$RIDB" "$EV/B-samples.log" && ok "B: the waiting handler backend was attributed to the request while it waited" || bad "B: the waiting backend could not be attributed to the request"
    if grep -q "bpgate:$RIDB|.*|Lock|" "$EV/B-samples.log"; then ok "B: the attributed backend was observed in a Lock wait"; else nv "B: no sample caught the Lock wait window (sampling only)"; fi
    ROW=$(audit_row "$RIDB")
    echo "B_audit=$ROW"
    printf '%s' "$ROW" | grep -q "session_scope_unverifiable" && ok "B: the deny audit row is reason session_scope_unverifiable" || bad "B: deny audit row is wrong ($ROW)"
    printf '%s' "$ROW" | grep -qi "statement timeout" && ok "B: the audit gate_error carries the real PostgreSQL statement timeout" || nv "B: gate_error does not name a statement timeout ($ROW)"
  fi
  stop_cand
fi
BIN=$MAIN_BIN

echo "=== C. idle_in_transaction_session_timeout (2000 ms, calibration build) ==="
if [ ! -x "$CALIB_BIN" ]; then
  nv "C: calibration binary $CALIB_BIN is missing; the idle bound is NOT measured this round"
else
  BIN=$CALIB_BIN
  start_cand 18183 C.log SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=2000 SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=60000 SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=60000 SCOPE_CALIB_ENABLE=1 SCOPE_CALIB_ROUTE=/api/v2/cases SCOPE_CALIB_PHASE=post SCOPE_CALIB_DELAY_MS=9000
  jarC=$EV/C.jar
  SIDC=$(new_session "$jarC")
  CSRFC=$(csrf_of "$jarC")
  if [ -z "$SIDC" ] || [ "$SIDC" = "__ERR__" ]; then bad "C: session id not resolvable"; else
    ok "C: the caller session logged in with the reset password"
    BEFORE=$(case_count)
    RIDC="c1slt-$TS-C"
    SAMP=$(sampler "$EV/C-samples.log")
    T0=$(now_ms)
    CODEC=$(curl -s -b "$jarC" -o "$EV/C-body.json" -D "$EV/C.h" -w '%{http_code}' -m 60 \
      -X POST "http://127.0.0.1:18183/api/v2/cases" -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
      -H "X-Request-Id: $RIDC" -H "X-CSRF-Token: $CSRFC" \
      -d "$(printf '{"case_no":"SLT-%s-C","name":"SLT-%s-C","description":"idle in transaction fixture"}' "$TS" "$TS")")
    T1=$(now_ms)
    wait "$SAMP" 2>/dev/null
    AFTER=$(case_count)
    ELC=$(( T1 - T0 ))
    echo "C code=$CODEC elapsed_ms=$ELC created=$(( AFTER - BEFORE ))"
    grep -q "bpgate:$RIDC|idle in transaction" "$EV/C-samples.log" && ok "C: pg_stat_activity showed the gate transaction idle in transaction with application_name=bpgate:<request id>" || nv "C: no sample caught the idle-in-transaction window (sampling only)"
    chk "$(bpgate_count)" "0" "C: the idle gate backend is gone at the end of the request (PostgreSQL terminated it)"
    if [ "$CODEC" = "409" ] || [ "$CODEC" = "500" ] || [ "$CODEC" = "503" ]; then ok "C: the request failed closed after its transaction was terminated ($CODEC)"; else bad "C: unexpected status after the idle bound fired ($CODEC)"; fi
    if [ "$ELC" -lt 20000 ]; then ok "C: the request ended within 20 s instead of hanging on a dead transaction ($ELC ms)"; else bad "C: the request hung past 20 s ($ELC ms)"; fi
    chk "$(( AFTER - BEFORE ))" "0" "C: nothing was committed by the terminated transaction"
    ROW=$(audit_row "$RIDC")
    echo "C_audit=$ROW"
  fi
  stop_cand
fi
BIN=$MAIN_BIN

echo "=== E. real duration of the long admin routes and the statement budget ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":18184 ")" "0" "E: port 18184 is free before the candidate starts"
start_cand 18184 E.log SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=8000 SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=60000 SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=120000
jarE=$EV/E-admin.jar
rm -f "$jarE"
CE=$(login_one "$jarE" "$PLATEMAIL" "$PLATPW1" "$WSA" admin)
echo "admin_audience_login_status=$CE identity=platform_admin"
if [ "$CE" != "200" ]; then
  nv "E: no admin-audience session could be obtained with the c1lat platform_admin fixture (status=$CE); long-route durations NOT measured this round"
fi
CSRFEA=$(csrf_admin_of "$jarE")
MAXX=0
for spec in "audit/verify" "audit/integrity/preview" "restores/preview" "control/kill-switch/preview"; do
  T0=$(now_ms)
  code=$(curl -s -b "$jarE" -o "$EV/E-$(printf '%s' "$spec" | tr '/' '_').json" -w '%{http_code}' -m 120 \
    -X POST "http://127.0.0.1:18184/api/admin/v1/$spec" -H "Content-Type: application/json" \
    -H "X-Auth-Audience: admin" -H "X-CSRF-Token: $CSRFEA" -d '{}')
  T1=$(now_ms)
  ms=$(( T1 - T0 ))
  echo "route=$spec status=$code elapsed_ms=$ms statement_budget_ms=60000"
  if [ "$code" = "200" ] && [ "$ms" -gt "$MAXX" ]; then MAXX=$ms; fi
done
echo "measured_max_ok_route_ms=$MAXX"
if [ "$MAXX" -gt 0 ]; then
  if [ "$MAXX" -lt 60000 ]; then ok "E: every measured long route stayed inside the current 60 s statement bound (max $MAXX ms)"; else bad "E: a long route exceeded the current 60 s statement bound ($MAXX ms)"; fi
  echo "E: per-route evidence recorded; a route-specific budget must be derived from these numbers, not assumed"
else
  nv "E: no long route returned 200 with this fixture, so no duration budget could be derived (statuses above are the evidence)"
fi
stop_cand

echo "=== F. availability: a long ACCESS EXCLUSIVE lock on cases and the pre-gate authorization read ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":18185 ")" "0" "F: port 18185 is free before the candidate starts"
start_cand 18185 F.log SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=8000 SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=60000 SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=120000
echo "F: application role statement_timeout default: $(pgq <<SQL
SELECT coalesce(array_to_string(rolconfig, ','), '<none>') FROM pg_roles WHERE rolname='blockchain_api';
SQL
)"
echo "F: server statement_timeout default: $(pgq <<SQL
SELECT setting FROM pg_settings WHERE name='statement_timeout';
SQL
)"
jarF=$EV/F.jar
SIDF=$(new_session "$jarF")
CSRFF=$(csrf_of "$jarF")
if [ -z "$SIDF" ] || [ "$SIDF" = "__ERR__" ]; then bad "F: session id not resolvable"; else
  BEFORE=$(case_count)
  RIDF="c1slt-$TS-F"
  SAMP=$(sampler "$EV/F-samples.log")
  # An ordinary request with NO seam: the lock is held before it starts, exactly
  # like a concurrent maintenance transaction would be.
  HOLD=$(hold_lock "$EV/F-hold.log" "BEGIN; LOCK TABLE cases IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(10); ROLLBACK;")
  sleep 1
  chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND state='active' AND query LIKE '%ACCESS EXCLUSIVE%';
SQL
)" "1" "F: the external transaction really holds ACCESS EXCLUSIVE on cases"
  T0=$(now_ms)
  CODEF=$(curl -s -b "$jarF" -o "$EV/F-body.json" -w '%{http_code}' -m 90 \
    -X POST "http://127.0.0.1:18185/api/v2/cases" -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
    -H "X-Request-Id: $RIDF" -H "X-CSRF-Token: $CSRFF" \
    -d "$(printf '{"case_no":"SLT-%s-F","name":"SLT-%s-F","description":"availability fixture"}' "$TS" "$TS")")
  T1=$(now_ms)
  wait "$HOLD" 2>/dev/null
  wait "$SAMP" 2>/dev/null
  AFTER=$(case_count)
  ELF=$(( T1 - T0 ))
  echo "F code=$CODEF elapsed_ms=$ELF created=$(( AFTER - BEFORE ))"
  chk "$CODEF" "201" "F: the request eventually succeeded once the lock was released"
  if [ "$ELF" -ge 2000 ]; then ok "F: the request was postponed by the ACCESS EXCLUSIVE lock ($ELF ms) instead of failing fast"; else nv "F: the request was not visibly postponed ($ELF ms)"; fi
  if grep -q "A|.*|Lock|relation" "$EV/F-samples.log"; then ok "F: a NON-gate backend was observed waiting on Lock/relation (the pre-gate authorization read)"; else nv "F: no sample caught the pre-gate Lock wait (sampling only)"; fi
  echo "F_observation: the blocked statement is outside the gate transaction, so the gate statement_timeout cannot bound it; the application pool carries no statement_timeout (see the two lines above) and PostgreSQL's own default is 0."
fi
stop_cand

echo "=== 8. final invariants ==="
chk "$(bpgate_count)" "0" "no commit-gate backend is left behind"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d" " -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":1818" )" "0" "no candidate port is left listening"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0