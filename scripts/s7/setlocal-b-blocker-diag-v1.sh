#!/usr/bin/env bash
# setlocal-b-blocker-diag-v1.sh
#
# DIAGNOSTIC, not an acceptance gate. The first execution of
# c1-auth007-setlocal-timeout-gate-v1.sh produced
#
#   B code=201 elapsed_ms=8032 created=1
#
# with SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS=1500 while an external transaction
# held LOCK TABLE cases IN ACCESS EXCLUSIVE MODE. Two explanations are possible
# and they mean opposite things:
#
#   (a) the blocked statement ran INSIDE the gate transaction, in which case
#       statement_timeout did not cut it and the SET LOCAL bound is not real;
#   (b) the blocked statement ran BEFORE the gate opened (or on another
#       connection), in which case the bound was never supposed to apply to it.
#
# This script decides that with raw rows: it samples EVERY non-idle backend of
# the database while the request is blocked, records application_name, state,
# wait event, transaction age and the statement text, and prints the holder and
# the blocked statement side by side.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-bblocker}"
BIN="${BIN:-$R/backend/bin.new/api-auth007-deny}"
PORT="${PORT:-18182}"
STMT_MS="${STMT_MS:-1500}"
HOLD_S="${HOLD_S:-9}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/diag-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/diag-console.log") 2>&1
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
now_ms(){ date +%s%3N; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN port=$PORT statement_timeout_ms=$STMT_MS hold_s=$HOLD_S evidence=$EV"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "prod_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1) expect=$API_EXPECT_SHA"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "SET LOCAL blocker diagnostic" \
    -workspaces c1lat-a,c1lat-b \
    -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
PW1="Pw-$TS-bblocker-9a13"
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
echo "fixture_login=$c"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$PW1\"}")
echo "password_change=$c"
rm -f "$jar"

RA=$EV/ra.jar
c=$(curl -s -c "$RA" -o "$EV/ra-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
echo "session_login=$c"
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf)
SID_A=$(curl -s -b "$RA" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['session_id']")
echo "session_id=$SID_A"
case "$SID_A" in *[!0-9a-f-]*|"") echo "SESSION_ID_NOT_RESOLVABLE"; exit 0;; esac

cand_pid=""
cleanup(){
  if [ -n "$cand_pid" ] && kill -0 "$cand_pid" 2>/dev/null; then
    kill -TERM "$cand_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$cand_pid" 2>/dev/null || true
  fi
  cand_pid=""
}
trap cleanup EXIT

( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  unset SCOPE_CALIB_ENABLE
  export SCOPE_COMMIT_GATE_LOCK_TIMEOUT_MS=60000
  export SCOPE_COMMIT_GATE_STATEMENT_TIMEOUT_MS="$STMT_MS"
  export SCOPE_COMMIT_GATE_IDLE_TX_TIMEOUT_MS=120000
  nohup setsid "$BIN" >> "$EV/candidate.log" 2>&1 &
  echo $! > "$EV/candidate.pid" )
sleep 1
cand_pid=$(cat "$EV/candidate.pid")
ready=0
for _ in $(seq 1 240); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
echo "candidate_ready=$ready"
[ "$ready" = "1" ] || exit 0

# Sample EVERY non-idle backend, with the statement text: the whole point is to
# see whether the blocked statement carries application_name bpgate:<rid>.
( for _ in $(seq 1 90); do
    echo "### utc=$(date -u +%FT%T.%3NZ)"
    pgq <<SQL 2>/dev/null
SELECT pid, COALESCE(NULLIF(application_name,''),'-'), COALESCE(state,'-'),
       COALESCE(wait_event_type,'-'), COALESCE(wait_event,'-'),
       COALESCE(EXTRACT(milliseconds FROM now()-xact_start)::bigint::text,'-'),
       LEFT(regexp_replace(COALESCE(query,''),'\s+',' ','g'),110)
FROM pg_stat_activity
WHERE datname='blockchain_platform' AND pid <> pg_backend_pid()
  AND (COALESCE(state,'') <> 'idle' OR xact_start IS NOT NULL)
ORDER BY pid;
SQL
    sleep 0.2
  done ) > "$EV/backends.log" 2>&1 &
SAMP=$!

( docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1 \
    -c "BEGIN; LOCK TABLE cases IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep($HOLD_S); ROLLBACK;" ) > "$EV/holder.log" 2>&1 &
HOLD=$!
sleep 1
echo "holder_rows=$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state='active' AND query LIKE '%ACCESS EXCLUSIVE%';
SQL
)"

T0=$(now_ms)
CODE=$(curl -s -b "$RA" -o "$EV/b-body.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -d "{\"case_no\":\"BBLK-$TS\",\"name\":\"BBLK-$TS\"}")
T1=$(now_ms)
wait "$HOLD" 2>/dev/null
wait "$SAMP" 2>/dev/null
echo "blocked_request http=$CODE elapsed_ms=$(( T1 - T0 )) rows=$(pgq <<SQL
SELECT count(*) FROM cases WHERE case_no='BBLK-$TS';
SQL
)"

echo "=== backends that were waiting on a lock ==="
grep -E '^[0-9]+\|' "$EV/backends.log" | awk -F'|' '$5 ~ /Lock|transactionid|tuple|relation/ {print}' | sort | uniq -c | sort -rn | head -20
echo "=== distinct application_name of every sampled backend ==="
grep -E '^[0-9]+\|' "$EV/backends.log" | awk -F'|' '{print $2" state="$3" wait="$4"/"$5}' | sort | uniq -c | sort -rn | head -20
echo "=== statements that mention cases ==="
grep -E '^[0-9]+\|' "$EV/backends.log" | grep -i 'cases' | awk -F'|' '{print $2" | "$3" | "$4"/"$5" | xact_ms="$6" | "$7}' | sort | uniq -c | sort -rn | head -20
echo "=== candidate http line ==="
grep -a 'api/v2/cases' "$EV/candidate.log" | tail -2 | cut -c1-400
echo "=== holder log ==="
head -5 "$EV/holder.log"
cleanup
echo "=== summary ==="
echo "DIAG_DONE=1"
exit 0
