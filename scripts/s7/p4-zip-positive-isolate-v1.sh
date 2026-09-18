#!/usr/bin/env bash
# p4-zip-positive-isolate-v1.sh
#
# Bounded isolated rerun of the ONLY failing row of the P4 all-format gate:
# the v2 ZIP positive control (export f5029da6-3f3c-4a48-ab4b-8249c2b21fed on
# 2026-09-15, status stuck at PENDING, zero objects, and NOT a single log line
# mentioning that export id). The gate itself is frozen and is NOT modified.
#
# This script reproduces exactly ONE scenario on a fresh candidate process with
# the same calibration seam the gate used (phase=post, 3000 ms). If the pipeline
# does not finish inside the budget the script takes REAL evidence instead of a
# guess: pg_stat_activity for the candidate backends, the candidate TCP census,
# and a Go goroutine dump via SIGQUIT so the blocking point is observed.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL=p4zipiso
EV=$R/acceptance-evidence/dual-system-v1/sprint7/p4-zipiso-$LABEL-$TS
BIN=$R/backend/bin.new/api-objguard-calib
PORT=18196
DELAY_MS=3000
WAIT_S=60
MINIO_DATA=/data/blockchain-platform/minio
API_EXPECT_SHA=910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38
API=http://127.0.0.1:8080
CALAPI=http://127.0.0.1:$PORT
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/iso-console.log") 2>&1
PASS=0; FAIL=0; NV=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
nv(){ NV=$((NV+1)); echo "NOT_VERIFIED $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo __ERR__; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
obj_list(){ ls -1R "$MINIO_DATA/blockchain-exports/exports" 2>/dev/null; ls -1R "$MINIO_DATA/blockchain-raw/exports" 2>/dev/null; }
obj_count(){ obj_list | grep -c . ; }
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN port=$PORT delay_ms=$DELAY_MS wait_s=$WAIT_S evidence=$EV"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this run"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"

echo "=== fixture reset (c1lat) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name 'P4 zip isolation' -workspaces c1lat-a,c1lat-b -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
if [ -n "$WSA" ] && [ -n "$INVEMAIL" ]; then ok "fixture credentials were produced"; else bad "fixture credentials are missing"; fi

echo "=== clear the initial-password flag ==="
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
PW1="Pw-$TS-p4zipiso-7c2a"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$PW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV/clear.json" "$EV/clear-pw.json"

echo "=== candidate start (fresh process, no prior scenarios) ==="
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
for _ in $(seq 1 240); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate ready on $PORT"
if [ "$ready" != "1" ]; then echo "READINESS_ABORT=1"; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; exit 0; fi
RA=$EV/jar
chk "$(curl -s -c "$RA" -o "$EV/login.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/login" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")" "200" "investigator session logs in on the candidate"
CSRF=$(csrf_of "$RA" cs_user_session_csrf)

echo "=== baseline pool census ==="
pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE datname='blockchain_platform';
SQL
echo "=== candidate application_name census before request ==="
pgq <<SQL
SELECT COALESCE(application_name,'-'), state, count(*) FROM pg_stat_activity WHERE datname='blockchain_platform' AND pid <> pg_backend_pid() GROUP BY 1,2 ORDER BY 1,2;
SQL

echo "=== v2 ZIP positive control (the exact failing scenario) ==="
J_ZIP='{"dataset":"transactions","format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions","token_transfers"]}'
obj_list > "$EV/v2-zip-pos-before.txt"
OBJ0=$(obj_count)
C1=$(curl -s -b "$RA" -o "$EV/v2-zip-pos.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF" -H "Idempotency-Key: p4zipiso-$TS-pos" -d "$J_ZIP")
EXPID=$(jget < "$EV/v2-zip-pos.json" "['id']")
echo "v2-zip positive http=$C1 export_id=$EXPID objects_before=$OBJ0"
if [ "$EXPID" = "__ERR__" ]; then bad "v2-zip positive returned no id"; cat "$EV/v2-zip-pos.json" | head -c 300; echo; fi
chk "$C1" "202" "the ZIP export request was accepted"
W=0; ST=""
while [ "$W" -lt "$WAIT_S" ]; do
  ST=$(pgq <<SQL
SELECT COALESCE(status,'-') FROM exports WHERE id='$EXPID'::uuid;
SQL
)
  echo "v2-zip positive poll t=$W status=$ST"
  case "$ST" in COMPLETED|FAILED) break;; esac
  sleep 5; W=$(( W + 5 ))
done
echo "v2-zip positive final_status=$ST waited_s=$W"

if [ "$ST" = "COMPLETED" ]; then
  echo "=== ISOLATED REPRODUCTION RESULT: the ZIP positive PASSES alone ==="
  echo "REPRO=did_not_reproduce_in_isolation"
else
  echo "=== ISOLATED REPRODUCTION RESULT: the ZIP positive hangs alone ==="
  echo "REPRO=reproduced_in_isolation"
  echo "--- candidate backends while stuck ---"
  pgq <<SQL
SELECT pid, COALESCE(application_name,'-'), state, COALESCE(wait_event_type,'-'), COALESCE(wait_event,'-'), now()-xact_start AS xact_age, left(COALESCE(query,'-'),120) FROM pg_stat_activity WHERE datname='blockchain_platform' AND pid <> pg_backend_pid() ORDER BY xact_start NULLS LAST;
SQL
  echo "--- candidate tcp census while stuck ---"
  ss -tnp 2>/dev/null | grep -c "$cand_pid" || true
  ss -tnp 2>/dev/null | grep "$cand_pid" | head -20 || true
  echo "--- candidate /proc status while stuck ---"
  grep -E "^(Threads|VmRSS)" "/proc/$cand_pid/status" 2>/dev/null || true
  echo "--- SIGQUIT goroutine dump (the process prints all stacks then exits) ---"
  MARK=$(wc -l < "$EV/candidate.log")
  kill -QUIT "$cand_pid" 2>/dev/null || true
  sleep 4
  sed -n "$(( MARK + 1 )),\$p" "$EV/candidate.log" > "$EV/goroutine-dump.txt"
  echo "goroutine_dump_lines=$(wc -l < "$EV/goroutine-dump.txt")"
  if [ "$(wc -l < "$EV/goroutine-dump.txt")" -gt 5 ]; then ok "a real Go goroutine dump was captured"; else bad "no goroutine dump was captured"; fi
  echo "--- blocked goroutines touching the export pipeline ---"
  grep -n -A 12 "runExportMulti\|writeZipExport\|putExportStream\|exportIntent" "$EV/goroutine-dump.txt" | head -80 || true
  cand_pid=""
fi

sleep 3
obj_list > "$EV/v2-zip-pos-after.txt"
OBJ1=$(obj_count)
echo "v2-zip positive objects=$OBJ0->$OBJ1 delta=$(( OBJ1 - OBJ0 ))"
pgq <<SQL
SELECT 'v2-zip', format, status, zip, COALESCE(object_ref,''), rows, COALESCE(error,'') FROM exports WHERE id='$EXPID'::uuid;
SQL

echo "=== candidate log: every line mentioning the export id ==="
grep -c "$EXPID" "$EV/candidate.log" || true
grep -n "$EXPID" "$EV/candidate.log" | head -20 || true
echo "=== candidate log tail ==="
tail -20 "$EV/candidate.log"

echo "=== final invariants ==="
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