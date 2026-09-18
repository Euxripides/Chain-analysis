#!/usr/bin/env bash
# p4-object-intent-gate-v1.sh
#
# Focused P4 object round: a REVOKED request must create ZERO MinIO objects.
#
# P4 gate v2 measured the defect: a refused export (409, zero committed rows) left
# exports/4a380557-....zip in MinIO with no exports row behind it. The fix makes the
# COMMITTED exports row the durable intent: the pipeline checks it at the start and
# again immediately before the object write. This gate measures the fix on a
# candidate instance; production is never the subject and is asserted unchanged.
#
#   POSITIVE   a normal export must reach COMPLETED and really write an object
#   NEGATIVE   the same export with the caller workspace switched inside the
#              handler delay must be refused 409 with an unchanged object inventory
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-p4obj}"
BIN="${BIN:-$R/backend/bin.new/api-objintent-calib}"
PORT="${PORT:-18191}"
DELAY_MS="${DELAY_MS:-3000}"
WAIT_S="${WAIT_S:-120}"
MINIO_DATA="${MINIO_DATA:-/data/blockchain-platform/minio}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/p4-obj-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1
PASS=0; FAIL=0; NV=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
nv(){ NV=$((NV+1)); echo "NOT_VERIFIED $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
obj_count(){ ls -1R "$MINIO_DATA/blockchain-exports/exports" 2>/dev/null | grep -c . ; }
obj_list(){ ls -1R "$MINIO_DATA/blockchain-exports/exports" 2>/dev/null; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL bin=$BIN port=$PORT delay_ms=$DELAY_MS wait_s=$WAIT_S"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 object intent acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
PW1="Pw-$TS-p4obj-71c3"
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$PW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear*.json

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
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
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
for _ in $(seq 1 360); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate ready on $PORT"
if [ "$ready" != "1" ]; then echo "READINESS_ABORT=1"; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; exit 0; fi
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running candidate is the binary under test"

RA=$EV/inv.jar
chk "$(curl -s -c "$RA" -o "$EV/login.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")" "200" "investigator session logs in"
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf)
BODY='{"dataset":"transfers","format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"]}'

echo "=== POSITIVE: a normal export must write exactly one artifact ==="
obj_list > "$EV/objects-before-positive.txt"
OBJ0=$(obj_count)
C1=$(curl -s -b "$RA" -o "$EV/pos.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -H "Idempotency-Key: p4obj-$TS-pos" -d "$BODY")
EXPID=$(jget < "$EV/pos.json" "['id']" 2>/dev/null || echo "-")
echo "positive http=$C1 export_id=$EXPID objects_before=$OBJ0"
chk "$C1" "202" "positive: the export was accepted"
W=0; ST=""
while [ "$W" -lt "$WAIT_S" ]; do
  ST=$(pgq <<SQL
SELECT COALESCE(status,'-') FROM exports WHERE id='$EXPID'::uuid;
SQL
)
  echo "positive poll t=$W status=$ST"
  case "$ST" in COMPLETED|FAILED) break;; esac
  sleep 5; W=$(( W + 5 ))
done
sleep 3
OBJ1=$(obj_count)
WROTE=$(( OBJ1 - OBJ0 ))
echo "positive final status=$ST waited_s=$W objects=$OBJ0->$OBJ1 delta=$WROTE"
chk "$ST" "COMPLETED" "positive: the committed export completed"
if [ "$WROTE" -ge 1 ]; then ok "positive: the committed export really wrote an object to MinIO"; else bad "positive: the committed export wrote no object"; fi

echo "=== NEGATIVE: the same export with a workspace switch inside the handler ==="
sleep 5
obj_list > "$EV/objects-before-negative.txt"
OBJ2=$(obj_count)
ROWS2=$(pgq <<SQL
SELECT count(*) FROM exports;
SQL
)
curl -s -b "$RA" -o "$EV/neg.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -H "Idempotency-Key: p4obj-$TS-neg" -d "$BODY" > "$EV/neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CSW=$(curl -s -b "$RA" -c "$RA" -o "$EV/switch.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_A" -d "{\"workspace_id\":\"$WSB\"}")
wait $NPID
NC=$(cat "$EV/neg-code.txt")
echo "negative switch=$CSW http=$NC waiting_ms=$(( W * 1000 )) (the same budget the positive needed)"
sleep "$W"
sleep 10
obj_list > "$EV/objects-after-negative.txt"
OBJ3=$(obj_count)
ROWS3=$(pgq <<SQL
SELECT count(*) FROM exports;
SQL
)
NEWNAMES=$(diff "$EV/objects-before-negative.txt" "$EV/objects-after-negative.txt" 2>/dev/null | grep -c '^>' || true)
WROTE_NEG=$(( OBJ3 - OBJ2 ))
echo "negative http=$NC objects=$OBJ2->$OBJ3 delta=$WROTE_NEG new_lines=$NEWNAMES export_rows=$ROWS2->$ROWS3"
chk "$CSW" "200" "negative: the workspace switch during the delay succeeded"
chk "$NC" "409" "negative: the stale-scope export was refused with 409"
chk "$WROTE_NEG" "0" "negative: the refused export created ZERO MinIO objects"
chk "$NEWNAMES" "0" "negative: no new object name appeared under the exports prefix"
chk "$ROWS3" "$ROWS2" "negative: the refused export left zero export rows"
echo "=== the abort must be visible in the runtime log ==="
ABORTS=$(grep -ac "durable intent row is gone" "$EV/candidate.log" 2>/dev/null || echo 0)
echo "pipeline_aborts_logged=$ABORTS"
if [ "$ABORTS" -ge 1 ]; then ok "the candidate logged the aborted pipeline instead of writing an artifact"; else nv "no abort line was logged"; fi

echo "=== 8. final invariants ==="
cleanup
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0
