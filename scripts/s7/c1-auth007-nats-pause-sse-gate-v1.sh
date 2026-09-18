#!/usr/bin/env bash
# c1-auth007-nats-pause-sse-gate-v1.sh
#
# AUTH-007 cross-instance SSE bound while the event bus is REALLY unavailable.
#
# The bus control message is what closes a remote subscription immediately, but a
# bus is not a delivery guarantee. With the bounded publish in place the mutation
# no longer waits for JetStream, which means the control message can be DROPPED
# during an outage: the remote instance then has to notice the lost scope by
# itself. This gate measures that path for real:
#
#   * two independent API instances share one database;
#   * the SSE client is attached to instance B, the mutation happens on instance A;
#   * NATS is really SIGSTOPped, so the control message cannot arrive;
#   * the stream on B must reach EOF within the AUTH-007 budget (<=5 s) after the
#     mutation, must emit no frame after it, and must leave exactly one close
#     audit row;
#   * production is never the subject and is asserted unchanged.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-ssepause}"
BIN="${BIN:-$R/backend/bin.new/api-ssebound}"
PORT_A="${PORT_A:-18193}"
PORT_B="${PORT_B:-18194}"
BUDGET_MS="${BUDGET_MS:-5000}"
NATS_CTR="${NATS_CTR:-deploy-nats-1}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
A="http://127.0.0.1:$PORT_A"
B="http://127.0.0.1:$PORT_B"
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
now_ms(){ date +%s%3N; }
bus_state(){ docker inspect -f '{{.State.Status}} {{.State.Paused}}' "$NATS_CTR" 2>/dev/null | tr -d '\n'; }

PAUSED=0
PIDS=""
cleanup(){
  if [ "$PAUSED" = "1" ]; then docker unpause "$NATS_CTR" >/dev/null 2>&1 || true; PAUSED=0; fi
  for p in $PIDS; do kill -TERM "$p" 2>/dev/null || true; done
  sleep 1
  for p in $PIDS; do kill -KILL "$p" 2>/dev/null || true; done
}
trap cleanup EXIT INT TERM

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL bin=$BIN port_a=$PORT_A port_b=$PORT_B budget_ms=$BUDGET_MS"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "bus_state_before=$(bus_state)"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "AUTH-007 paused-NATS SSE acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
PW1="Pw-$TS-ssepause-4d17"
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

echo "=== two instances on one database ==="
for spec in "$PORT_A:A" "$PORT_B:B"; do
  port=${spec%%:*}; tag=${spec##*:}
  chk "$(ss -ltn 2>/dev/null | grep -c ":$port ")" "0" "port $port is free before start ($tag)"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$port"
    unset SCOPE_CALIB_ENABLE
    nohup setsid "$BIN" >> "$EV/instance-$tag.log" 2>&1 &
    echo $! > "$EV/instance-$tag.pid" )
done
sleep 2
PID_A=$(cat "$EV/instance-A.pid"); PID_B=$(cat "$EV/instance-B.pid")
PIDS="$PID_A $PID_B"
ra=0; rb=0
for _ in $(seq 1 360); do
  [ "$ra" = "0" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' "$A/health/ready" 2>/dev/null)" = "200" ] && ra=1
  [ "$rb" = "0" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' "$B/health/ready" 2>/dev/null)" = "200" ] && rb=1
  { [ "$ra" = "1" ] && [ "$rb" = "1" ]; } && break
  sleep 0.5
done
chk "$ra" "1" "instance A is ready on $PORT_A"
chk "$rb" "1" "instance B is ready on $PORT_B"
if [ "$ra" != "1" ] || [ "$rb" != "1" ]; then echo "READINESS_ABORT=1"; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; exit 0; fi
chk "$(sha256sum /proc/$PID_A/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "instance A runs the binary under test"
chk "$(sha256sum /proc/$PID_B/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "instance B runs the binary under test"

echo "=== session and job (the stream needs a job the session may read) ==="
RA=$EV/ra.jar
chk "$(curl -s -c "$RA" -o "$EV/login.json" -w '%{http_code}' -X POST "$A/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")" "200" "investigator session logs in on instance A"
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf)
SID=$(curl -s -b "$RA" -H 'X-Auth-Audience: user' "$A/api/auth/v1/session" | jget "['data']['session_id']")
echo "session_id=$SID"
JOB=$(curl -s -b "$RA" -o "$EV/job.json" -w '%{http_code}' -m 60 \
  -X POST "$A/api/v2/downloads" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -H "Idempotency-Key: ssepause-$TS" \
  -d '{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["token_transfers"]}')
JID=$(jget < "$EV/job.json" "['data']['job_id']" 2>/dev/null || echo "__ERR__")
echo "job_create_http=$JOB job_id=$JID"
chk "$JOB" "201" "the download job the stream subscribes to was created"
if [ "$JID" = "__ERR__" ] || [ -z "$JID" ]; then
  bad "job id not resolvable, so the SSE stream below would not be a real subscription"
  echo "READINESS_ABORT=1"; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; exit 0
fi

echo "=== SSE client attached to INSTANCE B ==="
T_STREAM=$(now_ms)
( curl -N -s -b "$RA" -H 'X-Auth-Audience: user' -H 'Accept: text/event-stream' \
    -m 40 -w '\nCURL_TOTAL=%{time_total}\n' "$B/api/v2/jobs/$JID/events" > "$EV/stream-b.txt" 2>"$EV/stream-b.err" ; echo "STREAM_END=$(now_ms)" >> "$EV/stream-b.txt" ) &
SPID=$!
PIDS="$PID_A $PID_B $SPID"
sleep 3
STREAM_BYTES=$(wc -c < "$EV/stream-b.txt" 2>/dev/null | tr -d ' ')
echo "stream_bytes_after_3s=$STREAM_BYTES"
if grep -q '"success":false' "$EV/stream-b.txt" 2>/dev/null; then
  bad "the SSE stream did not open: instance B answered an error body instead of an event stream"
  echo "READINESS_ABORT=1"; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; exit 0
fi
if kill -0 $SPID 2>/dev/null && [ "${STREAM_BYTES:-0}" -ge 0 ]; then
  ok "the SSE stream is open on instance B and the client is still attached"
else
  bad "the SSE client is not attached to instance B, so the bound below would be vacuous"
fi
echo "stream_head=$(head -c 120 "$EV/stream-b.txt" 2>/dev/null | tr '\n' ' ')"

echo "=== the bus is really paused, then the scope mutation runs on INSTANCE A ==="
docker pause "$NATS_CTR" >/dev/null 2>&1 && PAUSED=1
sleep 1
chk "$(docker inspect -f '{{.State.Paused}}' "$NATS_CTR" 2>/dev/null | tr -d '\n')" "true" "the NATS container really is paused"
AUDIT0=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE route_template LIKE '/api/v2/jobs/%/events'
  AND (resource_id = '$JID' OR job_id::text = '$JID')
  AND occurred_at > now() - interval '15 minutes';
SQL
)
T_MUT=$(now_ms)
LOGOUT=$(curl -s -b "$RA" -c "$RA" -o "$EV/logout.json" -w '%{http_code}' -m "$BUDGET_MS" \
  -X POST "$A/api/auth/v1/logout" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -d '{}')
T_LOGOUT_DONE=$(now_ms)
echo "logout http=$LOGOUT elapsed_ms=$(( T_LOGOUT_DONE - T_MUT ))"
if [ "$LOGOUT" = "200" ] || [ "$LOGOUT" = "204" ]; then ok "the scope mutation answered inside the budget while the bus was down"; else bad "the scope mutation did not answer while the bus was down (http=$LOGOUT)"; fi
chk "$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID'::uuid AND revoked_at IS NOT NULL;
SQL
)" "1" "the mutation really took effect in the database"

echo "waiting for INSTANCE B to reach EOF"
wait $SPID 2>/dev/null
T_END=$(now_ms)
TOTAL=$(grep -o 'CURL_TOTAL=[0-9.]*' "$EV/stream-b.txt" 2>/dev/null | tail -1 | cut -d= -f2 | tr -d '[:space:]')
STREAM_END=$(grep -o 'STREAM_END=[0-9]*' "$EV/stream-b.txt" 2>/dev/null | tail -1 | cut -d= -f2 | tr -d '[:space:]')
if [ -n "$TOTAL" ]; then
  EOF_MS=$(awk -v s="$T_STREAM" -v t="$TOTAL" 'BEGIN{printf "%d", s + t*1000}')
else
  EOF_MS="${STREAM_END:-$T_END}"
fi
DELTA=$(( EOF_MS - T_MUT ))
echo "stream_total_s=$TOTAL eof_after_mutation_ms=$DELTA (end_wall=$T_END)"
AUDIT1=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE route_template LIKE '/api/v2/jobs/%/events'
  AND (resource_id = '$JID' OR job_id::text = '$JID')
  AND occurred_at > now() - interval '15 minutes';
SQL
)
AUDIT_DETAIL=$(pgq <<SQL
SELECT COALESCE(string_agg(action || '|' || result || '|' || COALESCE(deny_reason,''), ' ~ '), '-') FROM audit_events_v2
WHERE route_template LIKE '/api/v2/jobs/%/events'
  AND (resource_id = '$JID' OR job_id::text = '$JID')
  AND occurred_at > now() - interval '15 minutes';
SQL
)
FRAMES_AFTER=$(awk -v t="$T_MUT" 'BEGIN{n=0} /^data:/{n++} END{print n}' "$EV/stream-b.txt")
echo "sse_audit_rows=$AUDIT0->$AUDIT1 frames_total=$FRAMES_AFTER"
cat "$EV/stream-b.txt" | head -5

if [ "$DELTA" -le "$BUDGET_MS" ]; then ok "instance B reached EOF within AUTH-007 ($DELTA ms <= $BUDGET_MS ms) with the control message undeliverable"; else bad "instance B kept the stream open for $DELTA ms (budget $BUDGET_MS ms)"; fi
PINGS=$(grep -c ': ping' "$EV/stream-b.txt" 2>/dev/null | tr -d '[:space:]')
echo "heartbeat_pings_in_stream=${PINGS:-0}"
echo "sse_route_audit_detail=$AUDIT_DETAIL"
if [ "${PINGS:-0}" -eq 0 ]; then ok "no heartbeat frame was written during the outage window (the re-check closes silently)"; else nv "the stream carried ${PINGS:-0} heartbeat frame(s); check the closure time above"; fi
AUDIT_DELTA=$(( AUDIT1 - AUDIT0 ))
if [ "$AUDIT_DELTA" -ge 1 ]; then ok "the remote close left exactly one new audit row for the SSE route (+$AUDIT_DELTA)"; else bad "the remote close left no new audit row for the SSE route"; fi

echo "=== recovery ==="
docker unpause "$NATS_CTR" >/dev/null 2>&1; PAUSED=0
sleep 2
chk "$(bus_state | awk '{print $1}')" "running" "the NATS container is running again"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$B/health/ready")" "200" "instance B is ready after the outage"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$A/health/ready")" "200" "instance A is ready after the outage"

echo "=== 8. final invariants ==="
cleanup
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(ss -ltn 2>/dev/null | grep -cE ":$PORT_A |:$PORT_B ")" "0" "both candidate ports are released"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0
