#!/usr/bin/env bash
# p4-object-intent-allformat-gate-v2.sh
#
# P4 object round, ALL WRITERS. The shared pre-Put guard (putExportStream) must be
# the only way any export artifact reaches MinIO, and it must refuse the Put when
# the durable intent is gone or the live scope/authorization no longer holds.
#
# For every real writer -- legacy csv / legacy ndjson / legacy xlsx, v2 non-ZIP
# csv / jsonl / xlsx / parquet, and ZIP -- this gate runs:
#
#   POSITIVE  a normal export must reach COMPLETED and really write an object
#   NEGATIVE  the same export with the caller workspace revoked (session switched)
#             inside the calibration window must be refused 409 with ZERO new
#             MinIO objects, ZERO new object names, ZERO committed export rows
#             and a deny audit row
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL=p4allfmt
EV=$R/acceptance-evidence/dual-system-v1/sprint7/p4-allfmt-$LABEL-$TS
BIN=$R/backend/bin.new/api-objguard-calib
PORT=18197
DELAY_MS=3000
WAIT_S=90
MINIO_DATA=/data/blockchain-platform/minio
API_EXPECT_SHA=910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38
API=http://127.0.0.1:8080
CALAPI=http://127.0.0.1:$PORT
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1
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
switch_ws(){ curl -s -b "$RA" -c "$RA" -o "$EV/switch-$1.json" -w '%{http_code}' -m 60 -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d "{\"workspace_id\":\"$2\"}"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "bin=$BIN port=$PORT delay_ms=$DELAY_MS wait_s=$WAIT_S evidence=$EV"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
if [ -d "$MINIO_DATA/blockchain-exports" ]; then ok "the MinIO data directory is readable for object counting"; else bad "the MinIO data directory is missing"; fi

echo "=== fixture reset (c1lat) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name 'P4 all-format object acceptance' -workspaces c1lat-a,c1lat-b -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
if [ -n "$WSA" ] && [ -n "$WSB" ] && [ -n "$INVEMAIL" ]; then ok "fixture credentials were produced"; else bad "fixture credentials are missing"; fi

echo "=== clear the initial-password flag on the fixture identity ==="
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
PW1="Pw-$TS-p4allfmt-4d1e"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$PW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV/clear.json" "$EV/clear-pw.json"

echo "=== candidate start ==="
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
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running candidate is the binary under test"
RA=$EV/jar
chk "$(curl -s -c "$RA" -o "$EV/login.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/login" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")" "200" "investigator session logs in on the candidate"
CSRF=$(csrf_of "$RA" cs_user_session_csrf)
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE workspace_id='$WSA'::uuid AND status='active';
SQL
)" "2" "the fixture workspace has its two active memberships before the matrix"

run_scenario(){
  label=$1
  body=$2
  echo "=== scenario $label ==="
  obj_list > "$EV/$label-pos-before.txt"
  OBJ0=$(obj_count)
  C1=$(curl -s -b "$RA" -o "$EV/$label-pos.json" -w '%{http_code}' -m 180 \
    -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF" -H "Idempotency-Key: p4all-$TS-$label-pos" -d "$body")
  EXPID=$(jget < "$EV/$label-pos.json" "['id']")
  echo "$label positive http=$C1 export_id=$EXPID objects_before=$OBJ0"
  if [ "$EXPID" = "__ERR__" ]; then
    bad "$label positive: the export request returned no id (http=$C1)"
    cat "$EV/$label-pos.json" | head -c 300; echo
    return
  fi
  chk "$C1" "202" "$label positive: the export was accepted"
  W=0; ST=""
  while [ "$W" -lt "$WAIT_S" ]; do
    ST=$(pgq <<SQL
SELECT COALESCE(status,'-') FROM exports WHERE id='$EXPID'::uuid;
SQL
)
    echo "$label positive poll t=$W status=$ST"
    case "$ST" in COMPLETED|FAILED) break;; esac
    sleep 5; W=$(( W + 5 ))
  done
  sleep 3
  obj_list > "$EV/$label-pos-after.txt"
  OBJ1=$(obj_count)
  WROTE=$(( OBJ1 - OBJ0 ))
  echo "$label positive final_status=$ST waited_s=$W objects=$OBJ0->$OBJ1 delta=$WROTE"
  diff "$EV/$label-pos-before.txt" "$EV/$label-pos-after.txt" | grep '^>' > "$EV/$label-pos-new.txt" || true
  chk "$ST" "COMPLETED" "$label positive: the committed export completed"
  if [ "$WROTE" -ge 1 ]; then ok "$label positive: the committed export really wrote an object to MinIO"; else bad "$label positive: the committed export wrote no object"; fi
  pgq <<SQL
SELECT '$label', format, status, COALESCE(object_ref,''), rows, COALESCE(error,'') FROM exports WHERE id='$EXPID'::uuid;
SQL

  echo "=== scenario $label NEGATIVE (concurrent workspace revoke) ==="
  sleep 3
  obj_list > "$EV/$label-neg-before.txt"
  OBJ2=$(obj_count)
  ROWS2=$(pgq <<SQL
SELECT count(*) FROM exports;
SQL
)
  DENY2=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE route_template='/api/v1/exports' AND result='deny';
SQL
)
  curl -s -b "$RA" -o "$EV/$label-neg.json" -w '%{http_code}' -m 180 \
    -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF" -H "Idempotency-Key: p4all-$TS-$label-neg" -d "$body" > "$EV/$label-neg-code.txt" 2>/dev/null &
  NPID=$!
  sleep 1
  CSW=$(switch_ws "$label-neg" "$WSB")
  wait $NPID
  NC=$(cat "$EV/$label-neg-code.txt")
  echo "$label negative switch=$CSW http=$NC"
  NSLEEP=25
  if [ "$W" -gt 25 ]; then NSLEEP=$W; fi
  sleep "$NSLEEP"
  sleep 8
  obj_list > "$EV/$label-neg-after.txt"
  OBJ3=$(obj_count)
  ROWS3=$(pgq <<SQL
SELECT count(*) FROM exports;
SQL
)
  DENY3=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE route_template='/api/v1/exports' AND result='deny';
SQL
)
  NEWNAMES=$(diff "$EV/$label-neg-before.txt" "$EV/$label-neg-after.txt" 2>/dev/null | grep -c '^>' || true)
  WROTE_NEG=$(( OBJ3 - OBJ2 ))
  echo "$label negative http=$NC objects=$OBJ2->$OBJ3 delta=$WROTE_NEG new_names=$NEWNAMES rows=$ROWS2->$ROWS3 deny_audit=$DENY2->$DENY3"
  chk "$CSW" "200" "$label negative: the concurrent workspace revoke succeeded"
  chk "$NC" "409" "$label negative: the stale-scope export was refused with 409"
  chk "$WROTE_NEG" "0" "$label negative: the refused export created ZERO MinIO objects"
  chk "$NEWNAMES" "0" "$label negative: no new object name appeared under the exports prefix"
  chk "$ROWS3" "$ROWS2" "$label negative: the refused export left zero committed export rows"
  if [ "$DENY3" -gt "$DENY2" ]; then ok "$label negative: a deny audit row was recorded for the refused export"; else bad "$label negative: no deny audit row was recorded"; fi
  pgq <<SQL
SELECT '$label-neg', action, result, http_status, COALESCE(error_code,''), COALESCE(deny_reason,''), COALESCE(route_template,'') FROM audit_events_v2 WHERE route_template='/api/v1/exports' ORDER BY occurred_at DESC LIMIT 3;
SQL
  SB=$(switch_ws "$label-back" "$WSA")
  echo "$label switch_back=$SB"
  chk "$SB" "200" "$label: the session returned to the original workspace"
  CSRF=$(csrf_of "$RA" cs_user_session_csrf)
}

J_LEGACY_CSV='{"dataset":"transactions","format":"csv","subject":"0x0000000000000000000000000000000000000001","chain":"bsc"}'
J_LEGACY_JSON='{"dataset":"transactions","format":"json","subject":"0x0000000000000000000000000000000000000001","chain":"bsc"}'
J_LEGACY_XLSX='{"dataset":"transactions","format":"xlsx","subject":"0x0000000000000000000000000000000000000001","chain":"bsc"}'
J_V2_CSV='{"dataset":"transactions","format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions"]}'
J_V2_JSONL='{"dataset":"transactions","format":"jsonl","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions"]}'
J_V2_XLSX='{"dataset":"transactions","format":"xlsx","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions"]}'
J_V2_PARQUET='{"dataset":"transactions","format":"parquet","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions"]}'
J_ZIP='{"dataset":"transactions","format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transactions","token_transfers"]}'

run_scenario legacy-csv "$J_LEGACY_CSV"
run_scenario legacy-json "$J_LEGACY_JSON"
run_scenario legacy-xlsx "$J_LEGACY_XLSX"
run_scenario v2-csv "$J_V2_CSV"
run_scenario v2-jsonl "$J_V2_JSONL"
run_scenario v2-xlsx "$J_V2_XLSX"
run_scenario v2-parquet "$J_V2_PARQUET"
run_scenario v2-zip "$J_ZIP"

echo "=== the abort must be visible in the runtime log ==="
ABORTS=$(grep -ac "durable intent row is gone" "$EV/candidate.log" 2>/dev/null || echo 0)
echo "pipeline_aborts_logged=$ABORTS"
if [ "$ABORTS" -ge 1 ]; then ok "the candidate logged aborted pipelines instead of writing artifacts"; else nv "no abort line was logged"; fi

echo "=== final invariants ==="
cleanup
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE workspace_id='$WSA'::uuid AND status='active';
SQL
)" "2" "the fixture workspace still has its two active memberships"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0
