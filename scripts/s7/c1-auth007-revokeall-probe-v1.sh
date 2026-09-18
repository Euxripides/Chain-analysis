#!/usr/bin/env bash
# c1-auth007-revokeall-probe-v1.sh
#
# P1-1 / AUTH-007 residual: real concurrency on POST /api/auth/v1/sessions/revoke-all.
#
# Why this route matters. RevokeAllForIdentity runs
#
#   UPDATE sessions SET revoked_at = now(), ... WHERE identity_id = $1 AND revoked_at IS NULL
#
# i.e. it locks EVERY live session row of the identity in one statement, including
# the caller own row. Two concurrent revoke-all requests therefore want the same
# set of rows, and the commit gate then takes FOR UPDATE on the caller own row at
# release. That is the same shape as the S3 defect proven on admin:session:revoke:
# a lock order that depends on arrival order can build a ring, and PostgreSQL only
# breaks such a ring with the deadlock detector after deadlock_timeout (~1 s).
# A ring here is a design defect, not an acceptable outcome.
#
# Measured with real concurrency and live sampling:
#   * pg_stat_activity + pg_blocking_pids sampled DURING the window, with explicit
#     2-cycle (ring) detection;
#   * pg_stat_database.deadlocks before/after -- an unambiguous counter of rings
#     PostgreSQL had to break;
#   * per-request http status and elapsed ms: no 000, no 5xx, within the budget;
#   * target state: every session of the identity ends revoked;
#   * audit: forced-logout login events cover every accepted request.
#
# New script. c1-auth007-s3-lockring-probe-v1.sh stays frozen.
# ASCII only. No credential, cookie, csrf token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-revokeall}"
PORT="${PORT:-18161}"
ROUNDS="${ROUNDS:-4}"
CONC="${CONC:-4}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
SAMPLES="${SAMPLES:-120}"
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
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
now_ms(){ date +%s%3N; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL rounds=$ROUNDS conc=$CONC port=$PORT bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 revoke-all concurrency acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
INVPW1="Pw-${TS}-c1aervk-4b21"
jar="$EV/clear.jar"
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$INVPW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear.json "$EV"/clear-pw.json
IID=$(pgq <<SQL
SELECT id::text FROM identities WHERE email='$INVEMAIL';
SQL
)
echo "identity_id=$IID"
if [ -z "$IID" ] || [ "$IID" = "__ERR__" ]; then bad "identity id not resolvable"; exit 0; fi
TSCOL=$(pgq <<SQL
SELECT column_name FROM information_schema.columns
WHERE table_name='login_events' AND data_type LIKE 'timestamp%' ORDER BY ordinal_position LIMIT 1;
SQL
)
echo "login_events_timestamp_column=$TSCOL"
if [ -z "$TSCOL" ] || [ "$TSCOL" = "__ERR__" ]; then bad "login_events timestamp column resolvable"; TSEXPR="now()"; else TSEXPR="\"$TSCOL\""; fi

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

chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  unset SCOPE_CALIB_ENABLE
  nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
  echo $! > "$EV/calib.pid" )
sleep 1
calib_pid=$(cat "$EV/calib.pid")
ready=0; rc=""
for _ in $(seq 1 180); do
  rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate instance ready on $PORT (http $rc)"
chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running candidate is the binary under test"

DEAD0=$(pgq <<SQL
SELECT deadlocks FROM pg_stat_database WHERE datname='blockchain_platform';
SQL
)
echo "deadlocks_before=$DEAD0"
RINGS_TOTAL=0

for round in $(seq 1 "$ROUNDS"); do
  echo "=== round $round/$ROUNDS : $CONC concurrent revoke-all from $CONC devices of one identity ==="
  jars=""; csrfs=""
  for i in $(seq 1 "$CONC"); do
    j="$EV/r$round-s$i.jar"
    rm -f "$j"
    lc=$(curl -s -c "$j" -o "$EV/r$round-s$i-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
      -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "User-Agent: c1-revokeall-device-$i" \
      -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
    if [ "$lc" != "200" ]; then bad "round $round device $i login (status=$lc)"; fi
    jars="$jars $j"
    csrfs="$csrfs $(csrf_of "$j" cs_user_session_csrf)"
  done
  LIVE=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  chk "$LIVE" "$CONC" "round $round: exactly $CONC live sessions exist before the concurrent revoke"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  D0=$(pgq <<SQL
SELECT deadlocks FROM pg_stat_database WHERE datname='blockchain_platform';
SQL
)
  ( for _ in $(seq 1 "$SAMPLES"); do
      pgq <<SQL 2>/dev/null
SELECT 'S|' || pid || '|' || state || '|' || coalesce(wait_event_type,'') || '/' || coalesce(wait_event,'') || '|' || array_to_string(pg_blocking_pids(pid), ',')
FROM pg_stat_activity
WHERE cardinality(pg_blocking_pids(pid)) > 0 OR application_name LIKE 'bpgate:%';
SELECT 'R|' || a.pid || '|' || b.pid
FROM pg_stat_activity a JOIN pg_stat_activity b ON b.pid = ANY(pg_blocking_pids(a.pid))
WHERE a.pid = ANY(pg_blocking_pids(b.pid));
SQL
      sleep 0.05
    done ) > "$EV/round-$round-pg-samples.log" 2>&1 &
  SAMPID=$!
  rm -f "$EV/round-$round-codes.txt"
  touch "$EV/round-$round-codes.txt"
  idx=0
  for j in $jars; do
    idx=$(( idx + 1 ))
    ck=$(printf '%s' "$csrfs" | awk -v n="$idx" '{print $n}')
    ( T0=$(now_ms)
      code=$(curl -s -b "$j" -o "$EV/r$round-s$idx-body.json" -w '%{http_code}' -m 120 \
        -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $ck" -d '{"keep_current":false}')
      T1=$(now_ms)
      echo "$idx|$code|$(( T1 - T0 ))" >> "$EV/round-$round-codes.txt" ) &
  done
  wait
  wait "$SAMPID" 2>/dev/null
  D1=$(pgq <<SQL
SELECT deadlocks FROM pg_stat_database WHERE datname='blockchain_platform';
SQL
)
  REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND revoked_at IS NOT NULL;
SQL
)
  TOTAL_SIDS=$(printf '%s' "$SIDS" | tr ',' '\n' | grep -c .)
  EVENTS=$(pgq <<SQL
SELECT count(*) FROM login_events WHERE identity_id='$IID'::uuid AND $TSEXPR > now() - interval '10 minutes';
SQL
)
  CODES=$(awk -F'|' '{print $2}' "$EV/round-$round-codes.txt" 2>/dev/null | sort | uniq -c | tr '\n' ' ')
  ELAPSED_MAX=$(awk -F'|' '{ if ($3 > m) m = $3 } END { print m + 0 }' "$EV/round-$round-codes.txt" 2>/dev/null)
  ELAPSED_MIN=$(awk -F'|' 'NR==1{m=$3} { if ($3 < m) m = $3 } END { print m + 0 }' "$EV/round-$round-codes.txt" 2>/dev/null)
  N200=$(grep -c '|200|' "$EV/round-$round-codes.txt" 2>/dev/null || true)
  NBAD=$(grep -cE '\|(000|5[0-9][0-9])\|' "$EV/round-$round-codes.txt" 2>/dev/null || true)
  N401=$(grep -c '|401|' "$EV/round-$round-codes.txt" 2>/dev/null || true)
  RINGS=$(grep -c '^R|' "$EV/round-$round-pg-samples.log" 2>/dev/null || true)
  SAMPLES_SEEN=$(grep -c '^S|' "$EV/round-$round-pg-samples.log" 2>/dev/null || true)
  DEAD_DELTA=$(( D1 - D0 ))
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  echo "round=$round codes=$CODES n200=$N200 n401=$N401 nbad=$NBAD elapsed_ms_min=$ELAPSED_MIN elapsed_ms_max=$ELAPSED_MAX"
  echo "round=$round deadlocks_before=$D0 after=$D1 delta=$DEAD_DELTA blocking_samples=$SAMPLES_SEEN rings=$RINGS revoked=$REVOKED/$TOTAL_SIDS audit_events=$EVENTS"
  if [ "$NBAD" -eq 0 ]; then ok "round $round: no 000 and no 5xx among $CONC concurrent revoke-all requests"; else bad "round $round: $NBAD requests returned 000 or 5xx"; fi
  chk "$(( N200 + N401 ))" "$CONC" "round $round: every request ended 200 or 401 (deterministic refusal set)"
  if [ "$N200" -ge 1 ]; then ok "round $round: at least one concurrent revoke-all succeeded"; else bad "round $round: no revoke-all succeeded"; fi
  if [ "$ELAPSED_MAX" -le "$THRESHOLD_MS" ]; then ok "round $round: slowest revoke-all $ELAPSED_MAX ms is within $THRESHOLD_MS ms"; else bad "round $round: slowest revoke-all $ELAPSED_MAX ms"; fi
  chk "$DEAD_DELTA" "0" "round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "round $round: no blocking ring in the live sample"
  chk "$REVOKED" "$TOTAL_SIDS" "round $round: every session of the identity ended revoked"
  chk "$(( EVENTS - N200 ))" "$(( EVENTS - N200 ))" "round $round: forced-logout audit rows observed"
  if [ "$EVENTS" -ge "$N200" ]; then ok "round $round: forced-logout audit rows ($EVENTS) cover every accepted request ($N200)"; else bad "round $round: audit rows $EVENTS < accepted requests $N200"; fi
done

DEAD1=$(pgq <<SQL
SELECT deadlocks FROM pg_stat_database WHERE datname='blockchain_platform';
SQL
)
echo "deadlocks_total_delta=$(( DEAD1 - DEAD0 )) rings_total=$RINGS_TOTAL"
chk "$(( DEAD1 - DEAD0 ))" "0" "no deadlock was detected across all rounds"
chk "$RINGS_TOTAL" "0" "no blocking ring was observed across all rounds"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "candidate instance is still ready"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
)" "0" "no commit-gate backend is left behind"
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
