#!/usr/bin/env bash
# c1-auth007-s3-lockring-probe-v1.sh
#
# S3 root-cause probe. It answers one question with evidence collected WHILE the
# requests are still running (never after a timeout): where does a concurrent
# mutual administrator revoke actually wait?
#
# The sampler runs for the whole request window and records, every 300 ms:
#   pg_stat_activity  pid / application_name / state / wait_event_type /
#                     wait_event / pg_blocking_pids / xact age / query
#   pg_locks          pid / locktype / mode / granted / relation / transactionid
#
# ASCII only. No credential, cookie, csrf token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-s3-probe}"
PORT="${PORT:-18141}"
HANDLER_MS="${HANDLER_MS:-3000}"
ROUNDS="${ROUNDS:-4}"
CALIB_ROUTE="${CALIB_ROUTE:-}"
REQ_TIMEOUT="${REQ_TIMEOUT:-60}"
SAMPLES="${SAMPLES:-40}"
SAMPLE_INTERVAL="${SAMPLE_INTERVAL:-0.3}"
# v2 repairs the readiness budget. v1 waited 20 s (40 x 0.5 s) and then ran round
# 1 anyway, so the first round raced a calibration process that is not yet
# listening: the preserved v1 round s3-lockring-s3lockring-denycal-20260914T153429Z
# shows FAIL ready (http 000) followed by r1=000 r2=000 in 8 ms, while its own
# calib.log proves the process only logged "api listening" about 32 s after
# start. The other three FAILs of that round (no response, not exactly one
# target revoked, no exactly-once deny row) are cascades of that one readiness
# race, not product behaviour. v2 waits up to READY_TRIES x 0.5 s and, if the
# instance still is not ready, records the FAIL and stops BEFORE any round so a
# readiness race can never again be reported as a round result.
READY_TRIES="${READY_TRIES:-360}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/s3-lockring-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/probe-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
now_ms(){ date +%s%3N; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL port=$PORT handler_ms=$HANDLER_MS rounds=$ROUNDS calib_route=${CALIB_ROUTE:-ALL}"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this probe"
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
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
SECPW1="Pw-${TS}-c1aesec-2f83"
INVPW1="Pw-${TS}-c1aeinv-71c4"
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
  if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "$tag initial-password flag cleared"; else bad "$tag initial-password flag cleared (status=$c)"; fi
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1"
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1"
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json
admin_login(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
    -d "{\"identifier\":\"$SECEMAIL\",\"password\":\"$SECPW1\",\"audience\":\"admin\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }

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
    export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
    export SCOPE_CALIB_PHASE=post
    if [ -n "$CALIB_ROUTE" ]; then export SCOPE_CALIB_ROUTE="$CALIB_ROUTE"; fi
    nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
    echo $! > "$EV/calib.pid" )
  sleep 1
  calib_pid=$(cat "$EV/calib.pid")
  local ready=0 rc="" waited=0
  for _ in $(seq 1 "$READY_TRIES"); do
    rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
    waited=$(( waited + 1 ))
  done
  chk "$ready" "1" "calibration instance ready on $PORT (http $rc after $(( waited / 2 ))s)"
  if [ "$ready" != "1" ]; then
    echo "READINESS_ABORT=1 the calibration instance never became ready within $(( READY_TRIES / 2 ))s."
    echo "READINESS_ABORT=1 No round was started, so no round result can be a cascade of this race."
    echo "=== summary ==="
    echo "PASS=$PASS FAIL=$FAIL"
    echo "PROBE_FAIL=$FAIL"
    exit 0
  fi
  chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process is the binary under test"
}

# The sampler is the whole point of this script: it observes the wait while it is
# happening, so it runs in parallel with the requests and writes raw rows.
sample_pg(){
  local out="$1" n="$2" i
  for i in $(seq 1 "$n"); do
    {
      echo "### sample $i utc=$(date -u +%FT%T.%3NZ)"
      docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -c "
        SELECT a.pid,
               COALESCE(NULLIF(a.application_name,''),'-'),
               COALESCE(a.state,'-'),
               COALESCE(a.wait_event_type,'-'),
               COALESCE(a.wait_event,'-'),
               COALESCE(NULLIF(array_to_string(pg_blocking_pids(a.pid),','),''),'-'),
               COALESCE(EXTRACT(milliseconds FROM now()-a.xact_start)::bigint::text,'-'),
               LEFT(regexp_replace(COALESCE(a.query,''),'\s+',' ','g'),120)
        FROM pg_stat_activity a
        WHERE a.datname='blockchain_platform' AND a.pid <> pg_backend_pid()
          AND (COALESCE(a.state,'') <> 'idle' OR a.xact_start IS NOT NULL OR a.wait_event_type='Lock')
        ORDER BY a.pid" 2>&1
      echo "--- locks ---"
      docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -c "
        SELECT l.pid, l.locktype, l.mode, l.granted,
               COALESCE(l.relation::regclass::text,'-'),
               COALESCE(l.transactionid::text,'-')
        FROM pg_locks l
        WHERE l.pid <> pg_backend_pid()
          AND (NOT l.granted OR l.locktype IN ('transactionid','tuple'))
        ORDER BY l.pid, l.granted" 2>&1
    } >> "$out"
    sleep "$SAMPLE_INTERVAL"
  done
}

echo "=== S3: concurrent mutual revoke, $ROUNDS rounds ==="
start_calib
ROUND=0
while [ "$ROUND" -lt "$ROUNDS" ]; do
  ROUND=$((ROUND+1))
  A1=$EV/r$ROUND-admin1.jar; A2=$EV/r$ROUND-admin2.jar
  c1=$(admin_login "$A1" "$WSA"); c2=$(admin_login "$A2" "$WSA")
  chk "$c1$c2" "200200" "round $ROUND: both admin sessions logged in"
  A1SID=$(sess_id "$A1" admin); A2SID=$(sess_id "$A2" admin)
  A1CSRF=$(csrf_of "$A1" cs_admin_session_csrf); A2CSRF=$(csrf_of "$A2" cs_admin_session_csrf)
  if [ -z "$A1SID" ] || [ -z "$A2SID" ] || [ "$A1SID" = "__ERR__" ] || [ "$A2SID" = "__ERR__" ]; then bad "round $ROUND: admin session ids not resolvable"; break; fi
  echo "round $ROUND sessions a1=$A1SID a2=$A2SID"
  SAMPLE_OUT=$EV/round-$ROUND-pg-samples.log
  rb=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/admin/v1/sessions/%/commands/revoke'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
  sample_pg "$SAMPLE_OUT" "$SAMPLES" &
  SPID=$!
  D0=$(now_ms)
  curl -s -m "$REQ_TIMEOUT" -b "$A1" -o "$EV/r$ROUND-1.json" -w '%{http_code}' \
    -X POST "$CALAPI/api/admin/v1/sessions/$A2SID/commands/revoke" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1CSRF" \
    -d '{"confirm":true,"reason":"s3-lockring-round-1"}' > "$EV/r$ROUND-1-code.txt" 2>/dev/null &
  P1=$!
  curl -s -m "$REQ_TIMEOUT" -b "$A2" -o "$EV/r$ROUND-2.json" -w '%{http_code}' \
    -X POST "$CALAPI/api/admin/v1/sessions/$A1SID/commands/revoke" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A2CSRF" \
    -d '{"confirm":true,"reason":"s3-lockring-round-2"}' > "$EV/r$ROUND-2-code.txt" 2>/dev/null &
  P2=$!
  wait $P1; wait $P2
  D1=$(now_ms); DMS=$(( D1 - D0 ))
  wait $SPID 2>/dev/null
  R1=$(cat "$EV/r$ROUND-1-code.txt" 2>/dev/null || echo n/a)
  R2=$(cat "$EV/r$ROUND-2-code.txt" 2>/dev/null || echo n/a)
  REV=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id IN ('$A1SID'::uuid,'$A2SID'::uuid) AND revoked_at IS NOT NULL;
SQL
)
  ra=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/admin/v1/sessions/%/commands/revoke'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
  DENY=$(( ra - rb ))
  WAITROWS=$(grep -c '|Lock|' "$SAMPLE_OUT" 2>/dev/null || echo 0)
  echo "round $ROUND codes r1=$R1 r2=$R2 elapsed_ms=$DMS revoked=$REV deny_rows=$DENY lock_wait_samples=$WAITROWS"
  if [ "$R1" = "000" ] || [ "$R2" = "000" ]; then bad "round $ROUND: a cross revoke produced no response within ${REQ_TIMEOUT}s (r1=$R1 r2=$R2, $DMS ms)"; else ok "round $ROUND: both cross revokes answered (r1=$R1 r2=$R2, $DMS ms)"; fi
  case "$R1$R2" in *5*) bad "round $ROUND: a cross revoke returned a 5xx (r1=$R1 r2=$R2)";; *) ok "round $ROUND: neither cross revoke returned a 5xx";; esac
  if [ "$DMS" -lt 5000 ]; then ok "round $ROUND: settled within the AUTH-007 budget ($DMS ms < 5000 ms)"; else bad "round $ROUND: settled in $DMS ms (>= 5000 ms)"; fi
  chk "$REV" "1" "round $ROUND: exactly one target revoked, the loser write rolled back"
  chk "$DENY" "1" "round $ROUND: exactly one detached deny audit row (exactly-once)"
  chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query ILIKE '%sessions%';
SQL
)" "0" "round $ROUND: no session-row lock wait is left behind"
  chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction';
SQL
)" "0" "round $ROUND: no transaction left idle in transaction"
done

echo "=== sampling summary (first round) ==="
if [ -f "$EV/round-1-pg-samples.log" ]; then
  echo "sample_lines=$(wc -l < "$EV/round-1-pg-samples.log")"
  echo "distinct_application_names:"
  grep -E '^[0-9]+\|' "$EV/round-1-pg-samples.log" | awk -F'|' '{print $2}' | sort | uniq -c | sort -rn | head -12
  echo "distinct_wait_events:"
  grep -E '^[0-9]+\|' "$EV/round-1-pg-samples.log" | awk -F'|' '{print $4"|"$5}' | sort | uniq -c | sort -rn | head -12
  echo "blocking_edges:"
  grep -E '^[0-9]+\|' "$EV/round-1-pg-samples.log" | awk -F'|' '$6 != "-" {print $1" blocked_by "$6" state="$3" wait="$4"/"$5" xact_ms="$7}' | sort | uniq -c | sort -rn | head -12
fi
echo "=== calib log: gate lines ==="
grep -E 'scope commit gate|scope_commit|lock_timeout|deadlock|level":"ERROR|panic' "$EV/calib.log" 2>/dev/null | tail -20
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "PROBE_FAIL=$FAIL"
exit 0
