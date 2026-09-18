#!/usr/bin/env bash
# c1-sse-cross-instance-gate-v1.sh -- P1-B: cross-instance SSE scope invalidation.
#
# The defect: the SSE hub is per process. A subscription opened on instance A
# under workspace A used to survive a workspace switch, a forced logout or a
# logout performed through instance B, and no subscription audit row existed.
#
# What this gate proves with two real API processes against the real PostgreSQL
# and the real NATS, with the production process untouched:
#   * both instances are ready and both run the binary under test;
#   * the bus control message closes A's stream within the AUTH-007 budget after
#     a mutation executed on B (workspace switch, admin forced logout, logout);
#   * after EOF the stream emits zero bytes and zero frames across a full
#     heartbeat window;
#   * the scope re-check tick gives a deterministic bound (default 2 s) that does
#     not depend on the control message: the same measurement passes with
#     SSE_SCOPE_CONTROL_DISABLE=1;
#   * the old behaviour still fails, and the gate proves it instead of assuming
#     it: with the control message disabled and the re-check period at the old
#     heartbeat value the stream is still open well past the budget;
#   * a real short NATS outage does not change the verdict;
#   * open / close audit rows are exactly-once and carry request, trace, session,
#     workspace, job, route and closed_by;
#   * the abandoned job is 404 for the moved session through the other instance;
#   * the standby startup switch suppresses only the startup export
#     reconciliation: two instances of the same binary, one with and one without
#     the switch, differ exactly in whether an interrupted fixture export is
#     flipped, and the standby instance serves the same API surface.
#
# ASCII only. Fixtures live in the dedicated c1sse organisation. The real
# administrator account, the real exports and every pre-existing row are left
# alone; the two fixture jobs and the one fixture export row created here are
# preserved for the record and never deleted.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-sse-cross}"
PORTA="${PORTA:-18141}"
PORTB="${PORTB:-18142}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
HB_WINDOW_MS="${HB_WINDOW_MS:-16000}"
RECHECK_MS="${RECHECK_MS:-2000}"
OLD_RECHECK_MS="${OLD_RECHECK_MS:-15000}"
NATS_BREAK_MS="${NATS_BREAK_MS:-2000}"
API_EXPECT_SHA="${API_EXPECT_SHA:-c853acca6b770ab23d4f8f65f81295ffa884c8fae726572296aeab960e0bdc53}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-sse-cross-instance-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
APIA="http://127.0.0.1:$PORTA"
APIB="http://127.0.0.1:$PORTB"
WATCHDOG_HOLD="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/blockchain/containers-watchdog.hold"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
chk_le(){ if [ "${1:-0}" -le "${2:-0}" ] 2>/dev/null; then ok "$3 ($1 <= $2)"; else bad "$3 (got=$1 want<=$2)"; fi; }
note(){ echo "NOTE: $1"; }

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
now_ms(){ date +%s%3N; }
hdr_of(){ awk -v h="$2" 'tolower($1)==tolower(h":"){print $2}' "$1" 2>/dev/null | tr -d '\015' | tail -1; }
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
sha1of(){ sha256sum "$1" 2>/dev/null | cut -d" " -f1; }

echo "ts_utc=$TS script_sha256=$(sha1of "$0")"
echo "label=$LABEL bin=$BIN bin_sha256=$(sha1of "$BIN")"
echo "port_a=$PORTA port_b=$PORTB threshold_ms=$THRESHOLD_MS recheck_ms=$RECHECK_MS old_recheck_ms=$OLD_RECHECK_MS"
echo "evidence=$EV"

PA=""; PB=""
stop_inst(){
  local pid="$1"
  [ -n "$pid" ] || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  kill -TERM "$pid" 2>/dev/null || true
  for _ in $(seq 1 24); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
  kill -KILL "$pid" 2>/dev/null || true
  sleep 0.5
}
stop_all(){ stop_inst "$PA"; stop_inst "$PB"; PA=""; PB=""; }
cleanup(){ stop_all; rm -f "$WATCHDOG_HOLD" 2>/dev/null || true; }
trap cleanup EXIT

start_inst(){ # port recheck ctrl logfile pidfile -> pid
  local port="$1" recheck="$2" ctrl="$3" log="$4" pf="$5"
  rm -f "$pf"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$port"
    export API_STANDBY_STARTUP=1
    export SSE_SCOPE_RECHECK_MS="$recheck"
    if [ "$ctrl" = "1" ]; then export SSE_SCOPE_CONTROL_DISABLE=1; fi
    nohup setsid "$BIN" >> "$log" 2>&1 &
    echo $! > "$pf" )
  sleep 1
  cat "$pf"
}

wait_ready(){ # port out -> 1|0
  local port="$1" out="$2" rc=""
  for _ in $(seq 1 80); do
    rc=$(curl -s -o "$out" -w '%{http_code}' "http://127.0.0.1:$port/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then echo 1; return 0; fi
    sleep 0.5
  done
  echo 0
}

# EOF is detected through a marker file the streaming subshell writes only after
# curl has returned, so "closed" never depends on process-table timing.
wait_closed_from(){ # t0_ms limit_ms tag -> elapsed_ms or -1
  local t0="$1" limit="$2" tag="$3" t
  while :; do
    if [ -f "$EV/$tag-curl-rc.txt" ]; then t=$(now_ms); echo $((t-t0)); return 0; fi
    t=$(now_ms)
    if [ $((t-t0)) -ge "$limit" ]; then echo -1; return 1; fi
    sleep 0.05
  done
}

frames_of(){ local n; n=$(grep -c -E '^(data:|event:|: ping)' "$1" 2>/dev/null); echo "${n:-0}"; }
bytes_of(){ wc -c < "$1" 2>/dev/null | tr -d ' '; }

echo "=== 0. preflight: the production process and the shared dependencies ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha1of /proc/$AP/exe)
echo "prod_api_pid=$AP prod_api_sha256=$API_SHA"
chk "$API_SHA" "$API_EXPECT_SHA" "production API binary is the frozen build of this round"
chk "$(curl -s -o "$EV/prod-ready.json" -w '%{http_code}' "$API/health/ready")" "200" "production API is ready before the gate"
for k in postgres clickhouse minio nats; do
  chk "$(jget "['checks']['$k']" < "$EV/prod-ready.json")" "ok" "production readiness reports $k ok"
done
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no real export is PENDING or RUNNING before any second instance starts"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real administrator still carries its must-change flag (untouched)"
for p in "$PORTA" "$PORTB"; do
  chk "$(ss -ltn 2>/dev/null | grep -c ":$p ")" "0" "port $p is free before start"
done
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"
echo "census_before=$(pgq <<SQL
SELECT 'jobs='||(SELECT count(*) FROM download_jobs)||' shards='||(SELECT count(*) FROM download_shards)||' checkpoints='||(SELECT count(*) FROM checkpoints)||' outbox='||(SELECT count(*) FROM outbox_events)||' exports='||(SELECT count(*) FROM exports)||' audit_head='||(SELECT max(seq) FROM audit_events_v2);
SQL
)"

echo "=== 1. fixtures (dedicated c1sse organisation) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1sse-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1sse -org-name "C1 SSE cross-instance acceptance" -workspaces c1sse-a,c1sse-b \
    -identities investigator:c1sse-inv@fixture.local:c1sse-a+c1sse-b -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1SSE_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1SSE_B)
EMAIL=$(read_cred "$CRED" C1MEM_C1SSE_INV_EMAIL)
PW0=$(read_cred "$CRED" C1MEM_C1SSE_INV_PW)
IDID=$(read_cred "$CRED" C1MEM_C1SSE_INV_IDENTITY_ID)
echo "org=$ORG ws_a=$WSA ws_b=$WSB identity=$IDID"
[ -n "$WSA" ] && [ -n "$IDID" ] && [ -n "$WSB" ]; chk "$?" "0" "SSE fixture resolved (workspaces and identity present)"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account is outside the fixture organisation"

JOB=$(cat /proc/sys/kernel/random/uuid)
JOBB=$(cat /proc/sys/kernel/random/uuid)
for pair in "$JOB|$WSA|SSEX-$TS" "$JOBB|$WSB|SSEX-$TS-b"; do
  JID=$(printf '%s' "$pair" | cut -d'|' -f1)
  JWS=$(printf '%s' "$pair" | cut -d'|' -f2)
  JNO=$(printf '%s' "$pair" | cut -d'|' -f3)
  pgq > "$EV/job-insert-$JNO.txt" <<SQL
INSERT INTO download_jobs (id, job_no, status, chain, range_type, datasets,
                           organization_id, workspace_id, created_by_identity, source)
VALUES ('$JID'::uuid, '$JNO', 'RUNNING', 'tron', 'block_range', ARRAY['transactions'],
        '$ORG'::uuid, '$JWS'::uuid, '$IDID'::uuid, 'acceptance_probe');
SQL
done
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id='$JOB'::uuid AND workspace_id='$WSA'::uuid AND created_by_identity='$IDID'::uuid;
SQL
)" "1" "fixture job A exists in fixture workspace A only"
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id='$JOBB'::uuid AND workspace_id='$WSB'::uuid AND created_by_identity='$IDID'::uuid;
SQL
)" "1" "fixture job B exists in fixture workspace B only"

INVPW1="Pw-$TS-ssex-4d21"
login(){ # jar ws pw -> http code
  local jar="$1" ws="$2" pw="$3"
  rm -f "$jar"
  curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$EMAIL\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}"
}
chk "$(login "$EV/clear.jar" "$WSA" "$PW0")" "200" "fixture logs in with its initial password"
c=$(curl -s -b "$EV/clear.jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $(csrf_of "$EV/clear.jar" cs_user_session_csrf)" \
  -d "{\"current_password\":\"$PW0\",\"new_password\":\"$INVPW1\"}")
note "fixture initial-password flag cleared with status=$c"
chk "$(login "$EV/sess.jar" "$WSA" "$INVPW1")" "200" "fixture logs in to workspace A with its settled password"

sess_ws(){ curl -s -b "$1" -H "X-Auth-Audience: user" "$API/api/auth/v1/session" | jget "['data']['workspace_id']"; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: user" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }

SPID=""
open_stream_host(){ # tag host job jar
  local tag="$1" host="$2" job="$3" jar="$4"
  : > "$EV/$tag-stream.txt"
  rm -f "$EV/$tag-curl-rc.txt"
  ( curl -s -N -m 180 -b "$jar" -H 'X-Auth-Audience: user' -H 'Accept: text/event-stream' \
      -D "$EV/$tag-headers.txt" "$host/api/v2/jobs/$job/events" > "$EV/$tag-stream.txt" 2> "$EV/$tag-err.txt"
    echo $? > "$EV/$tag-curl-rc.txt" ) &
  SPID=$!
  sleep 2
}

audit_open(){ pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE action='job.events.subscribed' AND job_id='$JOB'::uuid AND request_id='$1';
SQL
}

run_cross(){ # tag action recheck ctrl expect
  local tag="$1" action="$2" recheck="$3" ctrl="$4" expect="$5"
  echo "--- case $tag action=$action ctrl_disable=$ctrl recheck_ms=$recheck expect_close=$expect ---"
  stop_all
  PA=$(start_inst "$PORTA" "$recheck" "$ctrl" "$EV/$tag-a.log" "$EV/$tag-a.pid")
  PB=$(start_inst "$PORTB" "$recheck" "$ctrl" "$EV/$tag-b.log" "$EV/$tag-b.pid")
  chk "$(wait_ready "$PORTA" "$EV/$tag-a-ready.json")" "1" "$tag: instance A is ready on $PORTA"
  chk "$(wait_ready "$PORTB" "$EV/$tag-b-ready.json")" "1" "$tag: instance B is ready on $PORTB"
  chk "$(jget "['checks']['nats']" < "$EV/$tag-a-ready.json")" "ok" "$tag: instance A readiness reports nats ok"
  chk "$(sha1of /proc/$PA/exe)" "$(sha1of "$BIN")" "$tag: instance A runs the binary under test"
  chk "$(sha1of /proc/$PB/exe)" "$(sha1of "$BIN")" "$tag: instance B runs the binary under test"
  chk "$([ "$(grep -c 'standby startup: skipping' "$EV/$tag-a.log")" -ge 1 ] && echo yes || echo no)" "yes" "$tag: instance A logged the safe standby startup line"
  if [ "$ctrl" = "1" ]; then note "$tag: instance A was started with SSE_SCOPE_CONTROL_DISABLE=1"; fi

  local jar="$EV/$tag.jar"
  chk "$(login "$jar" "$WSA" "$INVPW1")" "200" "$tag: session logs in to workspace A"
  local SID; SID=$(sess_id "$jar")
  local CSRF; CSRF=$(csrf_of "$jar" cs_user_session_csrf)
  chk "$(sess_ws "$jar")" "$WSA" "$tag: session is scoped to workspace A before subscribing"

  open_stream_host "$tag" "$APIA" "$JOB" "$jar"
  local RID TID
  RID=$(hdr_of "$EV/$tag-headers.txt" "X-Request-Id")
  TID=$(hdr_of "$EV/$tag-headers.txt" "X-Trace-Id")
  chk "$( [ -f "$EV/$tag-curl-rc.txt" ] && echo closed || echo open )" "open" "$tag: the stream opened on instance A and is still open"
  chk "$([ "$(frames_of "$EV/$tag-stream.txt")" -ge 1 ] && echo yes || echo no)" "yes" "$tag: the stream received its initial snapshot frame"
  echo "$tag|stream_request_id=$RID|stream_trace_id=$TID|session_id=$SID|job=$JOB|workspace=$WSA" >> "$EV/cases.txt"

  local T0 T1 ELAPSED MUT_MS MCODE RIDQ
  T0=$(now_ms)
  case "$action" in
    workspace_switch)
      MCODE=$(curl -s -m 60 -b "$jar" -c "$jar" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate-headers.txt" -w '%{http_code}' \
        -X POST "$APIB/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d "{\"workspace_id\":\"$WSB\"}") ;;
    logout)
      MCODE=$(curl -s -m 60 -b "$jar" -c "$jar" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate-headers.txt" -w '%{http_code}' \
        -X POST "$APIB/api/auth/v1/logout" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d '{}') ;;
    admin_revoke)
      local ajar="$EV/$tag-admin.jar" ACSRF
      rm -f "$ajar"
      chk "$(curl -s -c "$ajar" -o "$ajar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
        -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
        -d "{\"identifier\":\"$EMAIL\",\"password\":\"$INVPW1\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")" "200" "$tag: fixture administrator logs in"
      ACSRF=$(csrf_of "$ajar" cs_admin_session_csrf)
      MCODE=$(curl -s -m 60 -b "$ajar" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate-headers.txt" -w '%{http_code}' \
        -X POST "$APIB/api/admin/v1/sessions/$SID/commands/revoke" \
        -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $ACSRF" \
        -d '{"confirm":true,"reason":"p1b-cross-instance"}') ;;
  esac
  T1=$(now_ms)
  MUT_MS=$(( T1 - T0 ))
  echo "$tag mutation_http=$MCODE mutation_ms=$MUT_MS"
  chk_le "$MUT_MS" "$THRESHOLD_MS" "$tag: the scope mutation on instance B returned within AUTH-007"
  RIDQ=$(hdr_of "$EV/$tag-mutate-headers.txt" "X-Request-Id")

  if [ "$expect" = "close" ]; then
    ELAPSED=$(wait_closed_from "$T0" 40000 "$tag")
    [ "$ELAPSED" = "-1" ] && kill -KILL "$SPID" 2>/dev/null
    chk_le "$ELAPSED" "$THRESHOLD_MS" "$tag: instance A closed the abandoned stream within AUTH-007 of the mutation"
    echo "$tag|eof_after_mutation_ms=$ELAPSED|mutation_ms=$MUT_MS|mutation_http=$MCODE|closed_by_request_id=$RIDQ" >> "$EV/cases.txt"
    local B1 F1 B2 F2
    B1=$(bytes_of "$EV/$tag-stream.txt"); F1=$(frames_of "$EV/$tag-stream.txt")
    sleep "$((HB_WINDOW_MS/1000))"
    B2=$(bytes_of "$EV/$tag-stream.txt"); F2=$(frames_of "$EV/$tag-stream.txt")
    chk "$B2" "$B1" "$tag: zero bytes were written after EOF across the full heartbeat window"
    chk "$F2" "$F1" "$tag: zero frames were written after EOF across the full heartbeat window"
    chk "$(audit_open "$RID")" "1" "$tag: exactly one subscribed audit row for this subscription"
    chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.subscribed' AND job_id='$JOB'::uuid AND request_id='$RID'
  AND trace_id='$TID' AND session_id='$SID'::uuid AND workspace_id='$WSA'::uuid
  AND route_template='/api/v2/jobs/$JOB/events';
SQL
)" "1" "$tag: the open row carries trace, session, workspace and route"
    chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid AND session_id='$SID'::uuid;
SQL
)" "1" "$tag: exactly one scope-invalidated audit row for this subscription"
    chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid AND session_id='$SID'::uuid
  AND workspace_id='$WSA'::uuid AND route_template='/api/v2/jobs/$JOB/events'
  AND request_id='$RID' AND trace_id='$TID';
SQL
)" "1" "$tag: the close row names session, abandoned workspace, route, request and trace"
    if [ -n "$RIDQ" ]; then
      chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid
  AND detail->>'closed_by_request_id' = '$RIDQ';
SQL
)" "1" "$tag: the close row records closed_by_request_id = the instance B mutation request"
    else
      note "$tag: instance B mutation exposed no request id header; closed_by not asserted"
    fi
    chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action IN ('job.events.subscribed','job.events.unsubscribed','job.events.scope_invalidated')
  AND job_id='$JOB'::uuid AND session_id='$SID'::uuid;
SQL
)" "2" "$tag: the whole subscription is exactly two audit rows (open + close)"
    local JCODE CURWS
    JCODE=$(curl -s -m 30 -b "$jar" -o "$EV/$tag-job.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$APIB/api/v2/jobs/$JOB")
    CURWS=$(sess_ws "$jar")
    if [ "$CURWS" = "$WSB" ]; then
      chk "$JCODE" "404" "$tag: the job of the abandoned workspace is 404 through instance B"
    else
      note "$tag: session workspace is now '$CURWS' (logout/revoke case); job status recorded as $JCODE"
    fi
    echo "$tag|job_detail_http=$JCODE|session_workspace_after=$CURWS" >> "$EV/cases.txt"
  else
    ELAPSED=$(wait_closed_from "$T0" 6000 "$tag")
    chk "$ELAPSED" "-1" "$tag: NEGATIVE CALIBRATION -- the abandoned stream was still open 6 s after the mutation"
    local LATE
    LATE=$(wait_closed_from "$T0" 40000 "$tag")
    echo "$tag|still_open_after_6s=yes|eventual_close_ms=$LATE|mutation_ms=$MUT_MS" >> "$EV/cases.txt"
    note "$tag: the stream finally closed after $LATE ms, i.e. bounded by the old heartbeat, not by the fix"
    [ "$LATE" = "-1" ] && kill -KILL "$SPID" 2>/dev/null
  fi
  kill -KILL "$SPID" 2>/dev/null
  return 0
}

echo "=== 2. positive: control message enabled, deterministic re-check at $RECHECK_MS ms ==="
run_cross x1-workspace-switch workspace_switch "$RECHECK_MS" 0 close
run_cross x2-admin-revoke admin_revoke "$RECHECK_MS" 0 close
run_cross x3-logout logout "$RECHECK_MS" 0 close

echo "=== 3. positive: control message disabled (an instance that never saw the bus message) ==="
run_cross x4-switch-no-control workspace_switch "$RECHECK_MS" 1 close

echo "=== 4. negative calibration: the pre-fix bound (no control message, heartbeat re-check) ==="
run_cross n1-old-behaviour workspace_switch "$OLD_RECHECK_MS" 1 keep_open

echo "=== 5. negative calibration: a real short NATS outage ==="
run_cross n0-warmup workspace_switch "$RECHECK_MS" 0 close
stop_all
mkdir -p "$(dirname "$WATCHDOG_HOLD")" 2>/dev/null
touch "$WATCHDOG_HOLD" 2>/dev/null
chk "$([ -f "$WATCHDOG_HOLD" ] && echo yes || echo no)" "yes" "the containers watchdog hold is in place before the NATS outage"
bash "$R/scripts/backup/containers-watchdog.sh" > "$EV/watchdog-during-hold.txt" 2>&1
chk "$(grep -c 'WATCHDOG: hold=' "$EV/watchdog-during-hold.txt")" "1" "the watchdog reports the hold path instead of acting"
PA=$(start_inst "$PORTA" "$RECHECK_MS" 0 "$EV/n2-a.log" "$EV/n2-a.pid")
PB=$(start_inst "$PORTB" "$RECHECK_MS" 0 "$EV/n2-b.log" "$EV/n2-b.pid")
chk "$(wait_ready "$PORTA" "$EV/n2-a-ready.json")" "1" "n2: instance A is ready before the outage"
chk "$(wait_ready "$PORTB" "$EV/n2-b-ready.json")" "1" "n2: instance B is ready before the outage"
chk "$(login "$EV/n2.jar" "$WSA" "$INVPW1")" "200" "n2: session logs in to workspace A"
SID=$(sess_id "$EV/n2.jar")
CSRF=$(csrf_of "$EV/n2.jar" cs_user_session_csrf)
open_stream_host n2 "$APIA" "$JOB" "$EV/n2.jar"
RID=$(hdr_of "$EV/n2-headers.txt" "X-Request-Id"); TID=$(hdr_of "$EV/n2-headers.txt" "X-Trace-Id")
chk "$([ "$(frames_of "$EV/n2-stream.txt")" -ge 1 ] && echo yes || echo no)" "yes" "n2: the stream received its initial snapshot frame"
docker pause deploy-nats-1 > "$EV/nats-pause.txt" 2>&1
sleep "$((NATS_BREAK_MS/1000))"
docker unpause deploy-nats-1 >> "$EV/nats-pause.txt" 2>&1
sleep 1
chk "$(docker inspect -f '{{.State.Status}}' deploy-nats-1)" "running" "n2: the NATS container is running again after the short outage"
rm -f "$WATCHDOG_HOLD"
T0=$(now_ms)
N2CODE=$(curl -s -m 60 -b "$EV/n2.jar" -c "$EV/n2.jar" -o "$EV/n2-mutate.json" -D "$EV/n2-mutate-headers.txt" -w '%{http_code}' \
  -X POST "$APIB/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d "{\"workspace_id\":\"$WSB\"}")
N2MS=$(( $(now_ms) - T0 ))
echo "n2 mutation_http=$N2CODE mutation_ms=$N2MS"
chk_le "$N2MS" "$THRESHOLD_MS" "n2: the workspace switch through instance B returned within AUTH-007"
N2EOF=$(wait_closed_from "$T0" 40000 n2)
chk_le "$N2EOF" "$THRESHOLD_MS" "n2: instance A closed the abandoned stream within AUTH-007 after a real NATS outage"
echo "n2|eof_after_mutation_ms=$N2EOF|mutation_ms=$N2MS|mutation_http=$N2CODE" >> "$EV/cases.txt"
kill -KILL "$SPID" 2>/dev/null
chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid AND session_id='$SID'::uuid;
SQL
)" "1" "n2: exactly one scope-invalidated row after the outage"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production readiness is still ok after the NATS outage"

echo "=== 6. the standby startup switch only suppresses the startup export reconciliation ==="
stop_all
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "still no real export is PENDING or RUNNING before the fixture export row is created"
FEXP=$(cat /proc/sys/kernel/random/uuid)
pgq > "$EV/fixture-export-insert.txt" <<SQL
INSERT INTO exports (id, dataset, format, status, object_ref, rows, error,
                     organization_id, workspace_id, created_by, zip, query_snapshot, sha256, started_at)
VALUES ('$FEXP'::uuid, 'transactions', 'csv', 'RUNNING', '', 0, '',
        '$ORG'::uuid, '$WSA'::uuid, '$IDID'::uuid, false, '{}'::jsonb, '', now());
SQL
chk "$(pgq <<SQL
SELECT status FROM exports WHERE id='$FEXP'::uuid;
SQL
)" "RUNNING" "one labelled fixture export row exists in the fixture organisation"
PA=$(start_inst "$PORTA" "$RECHECK_MS" 0 "$EV/s1-a.log" "$EV/s1-a.pid")
chk "$(wait_ready "$PORTA" "$EV/s1-a-ready.json")" "1" "s1: the standby instance is ready"
sleep 2
chk "$(pgq <<SQL
SELECT status FROM exports WHERE id='$FEXP'::uuid;
SQL
)" "RUNNING" "the standby instance did NOT touch the interrupted fixture export"
chk "$(grep -c 'standby startup: skipping interrupted-export reconciliation' "$EV/s1-a.log")" "1" "the standby instance logged exactly one standby startup line"
chk "$(login "$EV/s1.jar" "$WSA" "$INVPW1")" "200" "s1: the standby instance serves the login route"
chk "$(sess_ws "$EV/s1.jar")" "$WSA" "s1: the standby instance serves the session route with the right workspace"
chk "$(curl -s -m 20 -b "$EV/s1.jar" -H 'X-Auth-Audience: user' -o "$EV/s1-job.json" -w '%{http_code}' "$APIA/api/v2/jobs/$JOB")" "200" "s1: the standby instance serves the job detail route"
chk "$(curl -s -m 20 -o /dev/null -w '%{http_code}' "$APIA/health/ready")" "200" "s1: the standby instance reports ready"
stop_inst "$PA"; PA=""
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORTA"
  nohup setsid "$BIN" >> "$EV/s2-a.log" 2>&1 &
  echo $! > "$EV/s2-a.pid" )
sleep 1
PB=$(cat "$EV/s2-a.pid")
chk "$(wait_ready "$PORTA" "$EV/s2-a-ready.json")" "1" "s2: the same binary without the standby switch is ready"
sleep 2
chk "$(pgq <<SQL
SELECT status FROM exports WHERE id='$FEXP'::uuid;
SQL
)" "FAILED" "the same binary without the switch flipped the interrupted fixture export to FAILED"
chk "$(grep -c 'standby startup: skipping' "$EV/s2-a.log")" "0" "the non-standby instance logged no standby startup line"
stop_inst "$PB"; PB=""
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no export is left PENDING or RUNNING at the end of the gate"

echo "=== 7. cleanup and residue ==="
stop_all
rm -f "$WATCHDOG_HOLD" 2>/dev/null
for p in "$PORTA" "$PORTB"; do
  chk "$(ss -ltn 2>/dev/null | grep -c ":$p ")" "0" "port $p has no listener after cleanup"
done
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports after cleanup"
chk "$(sha1of /proc/$AP/exe)" "$API_EXPECT_SHA" "the production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready after the gate"
chk "$(pgq <<SQL
SELECT count(*) FROM workspaces WHERE id IN ('$WSA'::uuid,'$WSB'::uuid);
SQL
)" "2" "both fixture workspaces are preserved"
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id IN ('$JOB'::uuid,'$JOBB'::uuid);
SQL
)" "2" "both fixture jobs are preserved"
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE id='$FEXP'::uuid;
SQL
)" "1" "the fixture export row is preserved for the record"
echo "census_after=$(pgq <<SQL
SELECT 'jobs='||(SELECT count(*) FROM download_jobs)||' shards='||(SELECT count(*) FROM download_shards)||' checkpoints='||(SELECT count(*) FROM checkpoints)||' outbox='||(SELECT count(*) FROM outbox_events)||' exports='||(SELECT count(*) FROM exports)||' audit_head='||(SELECT max(seq) FROM audit_events_v2);
SQL
)"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real administrator is still untouched at the end of the gate"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SSE_CROSS_INSTANCE_${LABEL}_PASS=1"; else echo "C1_SSE_CROSS_INSTANCE_${LABEL}_PASS=0"; fi
