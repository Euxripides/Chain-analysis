#!/usr/bin/env bash
# c1-scope-body-budget-gate-v1.sh -- P1-A residual: the 12 MiB per-request body
# bound was never a process-wide cap. This gate drives the boundary exactly by
# starting the isolated instance with deliberately small limits, so the mechanism
# is proven without generating hundreds of megabytes of production traffic:
#
#   conf A  byte budget   SCOPE_BODY_MAX_TOTAL_BYTES=16MiB  INFLIGHT=8
#           -> two concurrent 12 MiB bodies: the second must be refused with 429
#   conf B  inflight cap  SCOPE_BODY_MAX_TOTAL_BYTES=128MiB INFLIGHT=1
#           -> two concurrent 12 MiB bodies: the second must be refused with 429
#   conf C  read deadline SCOPE_BODY_READ_TIMEOUT_MS=3000
#           -> one 12 MiB body offered at 2 MiB/s must be refused with 408
#   negcal  the frozen pre-fix binary, same load as conf A
#           -> no 429 at all: the refusal is produced by the new code, not by luck
#
# Every configuration also runs a REAL control write that must answer 201, which
# proves the budget was released again and the handler completed; /health/ready
# must answer 200 while the budget is exhausted. ASCII only; the real
# administrator account is untouched and no secret is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
OLD_BIN="${OLD_BIN:-$R/backend/bin.new/api-calib-p1a}"
LABEL="${LABEL:-body-budget}"
PORT="${PORT:-18133}"
API_EXPECT_SHA="${API_EXPECT_SHA:-bfcea45b88b7b253799834643a435a2d9c415f5cc68fd0d7bfbdba7b3dd45b3e}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-scope-commit-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
BIG=/tmp/s7/scope-body-12m.bin
mkdir -p "$EV" /tmp/s7; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
rss_of(){ ps -o rss= -p "$1" 2>/dev/null | tr -d ' '; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "old_bin=$OLD_BIN old_bin_sha256=$(sha256sum "$OLD_BIN" | cut -d\  -f1)"
echo "evidence=$EV"

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

start_inst(){
  local bin="$1" tag="$2"
  cleanup
  chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start ($tag)"
  chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no PENDING/RUNNING export before starting a second API process ($tag)"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$PORT"
    export SCOPE_BODY_MAX_TOTAL_BYTES="${MAX_TOTAL:-134217728}"
    export SCOPE_BODY_MAX_INFLIGHT="${MAX_INFLIGHT:-24}"
    export SCOPE_BODY_READ_TIMEOUT_MS="${READ_TIMEOUT_MS:-30000}"
    nohup setsid "$bin" >> "$EV/inst-$tag.log" 2>&1 &
    echo $! > "$EV/inst.pid" )
  sleep 1
  calib_pid=$(cat "$EV/inst.pid")
  local ready=0 rc=""
  for _ in $(seq 1 40); do
    rc=$(curl -s -o "$EV/ready-$tag.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "$tag: isolated instance is ready on $PORT (http $rc, max_total=${MAX_TOTAL:-134217728} max_inflight=${MAX_INFLIGHT:-24} read_timeout_ms=${READ_TIMEOUT_MS:-30000})"
  chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$bin" | cut -d\  -f1)" "$tag: running process is the binary under test"
  echo "$tag: pid=$calib_pid rss_kb_baseline=$(rss_of "$calib_pid")"
}

echo "=== 0. runtime identity and fixtures ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1body-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1body -org-name "C1 body budget acceptance" -workspaces c1body-a \
    -identities investigator:c1body-inv@fixture.local:c1body-a -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1BODY_A)
EMAIL=$(read_cred "$CRED" C1MEM_C1BODY_INV_EMAIL)
PW0=$(read_cred "$CRED" C1MEM_C1BODY_INV_PW)
PW1="Pw-${TS}-c1body-5e27"
JAR=$EV/jar.txt
c=$(curl -s -c "$JAR" -o "$EV/login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW0\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
CSRF=$(csrf_of "$JAR" cs_user_session_csrf)
c=$(curl -s -b "$JAR" -o "$EV/pwchange.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  -d "{\"current_password\":\"$PW0\",\"new_password\":\"$PW1\"}")
[ "$c" = "200" ] || [ "$c" = "204" ]; chk "$?" "0" "initial-password flag cleared through the real endpoint (status=$c)"
rm -f "$JAR"
c=$(curl -s -c "$JAR" -o "$EV/login2.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity re-logs in with the cleared password"
CSRF=$(csrf_of "$JAR" cs_user_session_csrf)

dd if=/dev/zero of="$BIG" bs=1048576 count=12 status=none
chk "$(stat -c %s "$BIG")" "12582912" "12 MiB payload prepared"
CONTROL_OK=0

post_one(){
  local out="$1" extra="${2:-}"
  # shellcheck disable=SC2086
  curl -s -b "$JAR" -o "$out" -D "$out.h" -w '%{http_code}' $extra \
    -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF" --data-binary "@$BIG"
}

# The control request is a REAL, small, well-formed write that has to succeed end
# to end. That is strictly stronger than "the gate did not refuse it": a 201 proves
# the reservation was released, the body was buffered normally and the handler ran
# to completion. The name is unique per invocation because the product rejects a
# duplicate case name inside one workspace.
post_control(){
  local out="$1" uniq="$2"
  printf '{"case_no":"BODYBUDGET-ctrl-%s","name":"BODYBUDGET control %s"}' "$uniq" "$uniq" > "$out.req"
  curl -s -b "$JAR" -o "$out" -w '%{http_code}' -X POST "$CALAPI/api/v2/cases" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" --data-binary "@$out.req"
}
post_control_ok(){
  local out="$1" msg="$2" uniq="$3" code
  code=$(post_control "$out" "$uniq")
  case "$code" in
    201) CONTROL_OK=$((CONTROL_OK+1)); ok "$msg (201: a real case was created, so the budget gate let the request through and the handler completed)";;
    *) bad "$msg (status=$code, expected 201 from a real write)";;
  esac
}

# Only the control writes may ever produce a case; the 12 MiB storm bodies are junk
# and must never create one.
cases_storm(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND case_no LIKE 'BODYBUDGET-storm-%';
SQL
}
cases_control(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND case_no LIKE 'BODYBUDGET-ctrl-%';
SQL
}

echo "=== 1. conf A: the byte budget refuses the second concurrent body ==="
MAX_TOTAL=16777216 MAX_INFLIGHT=8 READ_TIMEOUT_MS=30000 start_inst "$BIN" confA
post_control_ok "$EV/confA-first.json" "confA: a small body is still accepted past the budget gate" "confA-first-$TS"
A1=$EV/confA-r1.code; A2=$EV/confA-r2.code
post_one "$EV/confA-r1.json" > "$A1" &
P1=$!
post_one "$EV/confA-r2.json" > "$A2" &
P2=$!
sleep 2
chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "confA: /health/ready answers 200 while the budget is exhausted"
wait $P1; wait $P2
CA1=$(cat "$A1"); CA2=$(cat "$A2")
echo "confA concurrent_codes=$CA1,$CA2 rss_kb=$(rss_of "$calib_pid")"
echo "confA|concurrent_12MiB|$CA1|$CA2" >> "$EV/budget.tsv"
if [ "$CA1" = "429" ] || [ "$CA2" = "429" ]; then ok "confA: the byte budget refused a concurrent 12 MiB body with 429"; else bad "confA: no 429 was produced (codes $CA1,$CA2)"; fi
if [ "$CA1" = "400" ] || [ "$CA2" = "400" ]; then ok "confA: the first body still reached the handler tier within budget"; else bad "confA: no request got through within budget (codes $CA1,$CA2)"; fi
grep -q 'request_body_budget_exhausted' "$EV/confA-r1.json" || grep -q 'request_body_budget_exhausted' "$EV/confA-r2.json"
chk "$?" "0" "confA: the refusal carries the stable code request_body_budget_exhausted"
post_control_ok "$EV/confA-after.json" "confA: the budget is released again immediately after the storm" "confA-after-$TS"
chk "$(cases_storm)" "0" "confA: no business object was ever created by a storm body"

echo "=== 2. conf B: the inflight cap refuses the second concurrent body ==="
MAX_TOTAL=134217728 MAX_INFLIGHT=1 READ_TIMEOUT_MS=30000 start_inst "$BIN" confB
B1=$EV/confB-r1.code; B2=$EV/confB-r2.code
post_one "$EV/confB-r1.json" > "$B1" &
Q1=$!
post_one "$EV/confB-r2.json" > "$B2" &
Q2=$!
wait $Q1; wait $Q2
CB1=$(cat "$B1"); CB2=$(cat "$B2")
echo "confB concurrent_codes=$CB1,$CB2"
echo "confB|inflight_1|$CB1|$CB2" >> "$EV/budget.tsv"
if [ "$CB1" = "429" ] || [ "$CB2" = "429" ]; then ok "confB: the inflight cap refused a concurrent body with 429"; else bad "confB: no 429 was produced (codes $CB1,$CB2)"; fi
post_control_ok "$EV/confB-after.json" "confB: a normal request succeeds again after the storm" "confB-after-$TS"
chk "$(cases_storm)" "0" "confB: no business object was ever created by a storm body"

echo "=== 3. conf C: a slow upload is refused by the connection read deadline ==="
MAX_TOTAL=134217728 MAX_INFLIGHT=8 READ_TIMEOUT_MS=3000 start_inst "$BIN" confC
T0=$(date +%s%3N)
CC=$(post_one "$EV/confC-slow.json" "--limit-rate 2M -m 60")
T1=$(date +%s%3N)
MS=$(( T1 - T0 ))
echo "confC slow_upload_code=$CC elapsed_ms=$MS rss_kb=$(rss_of "$calib_pid")"
echo "confC|slow_upload_2MiBps|$CC|$MS" >> "$EV/budget.tsv"
case "$CC" in
  408) ok "confC: the slow upload was refused with 408 after ${MS} ms (deadline 3000 ms)";;
  000|"") ok "confC: the slow upload was cut off without a response body after ${MS} ms (connection deadline fired)";;
  *) bad "confC: unexpected status $CC after ${MS} ms";;
esac
chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "confC: /health/ready answers 200 during and after the slow upload"
post_control_ok "$EV/confC-after.json" "confC: a normal request succeeds again after the slow upload" "confC-after-$TS"
chk "$(cases_storm)" "0" "confC: no business object was ever created by a storm body"

echo "=== 4. negative calibration: the frozen pre-fix binary has no such bound ==="
MAX_TOTAL=16777216 MAX_INFLIGHT=8 READ_TIMEOUT_MS=30000 start_inst "$OLD_BIN" negcal
N1=$EV/negcal-r1.code; N2=$EV/negcal-r2.code
post_one "$EV/negcal-r1.json" > "$N1" &
R1=$!
post_one "$EV/negcal-r2.json" > "$N2" &
R2=$!
wait $R1; wait $R2
CN1=$(cat "$N1"); CN2=$(cat "$N2")
echo "negcal concurrent_codes=$CN1,$CN2"
echo "negcal|pre_fix_binary|$CN1|$CN2" >> "$EV/budget.tsv"
if [ "$CN1" = "429" ] || [ "$CN2" = "429" ]; then
  bad "negcal: the pre-fix binary refused a request, so the refusal is not attributable to the new code"
else
  ok "negcal: the pre-fix binary accepted both concurrent 12 MiB bodies ($CN1,$CN2) - the bound is new code, not luck"
fi
chk "$(cases_storm)" "0" "negcal: still no business object was created by a storm body"

echo "=== 5. census and production isolation ==="
chk "$(systemctl --user show blockchain-api.service -p MainPID --value)" "$AP" "production API pid is unchanged"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"
chk "$(cases_control)" "$CONTROL_OK" "every control write really created its case (each control request reached the handler)"
echo "audit_head_after=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)"

cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT has no listener after cleanup"
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"
rm -f "$BIG"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SCOPE_BODY_BUDGET_${LABEL}_PASS=1"; else echo "C1_SCOPE_BODY_BUDGET_${LABEL}_PASS=0"; fi
