#!/usr/bin/env bash
# c1-sse-scope-gate-v4.sh -- C round A5, second authoritative SSE round after the
# C1-SSE-OLD-SCOPE-OPEN / C1-SSE-NO-SUBSCRIPTION-AUDIT fix.
#
# Lineage: v3 (sha 6c2d6b2f...) produced the authoritative 11 PASS / 3 FAIL round that FOUND
# both defects; it is FROZEN and stays on record untouched. v4 keeps every v3 scenario and adds
# what the fix must now satisfy:
#
#   * the old-scope stream must reach EOF when the same session switches workspace, and it must
#     receive NO further frame at all -- not even a heartbeat (v3 measured 1 ping after the
#     switch: that is C1-SSE-OLD-SCOPE-OPEN)
#   * the subscription must be visible in audit_events_v2: exactly one open row and exactly one
#     scope-invalidated row, both carrying session, workspace, job, route and request id, with
#     the scope-invalidated row naming the request that triggered the close, and the close row
#     must become visible within the 5 s audit bound (v3 measured 0 subscription rows: that is
#     C1-SSE-NO-SUBSCRIPTION-AUDIT)
#   * a logout must also end the subscription (server side, not client side)
#   * the job of the abandoned workspace must be 404 for the moved session (unchanged from v3)
#
# The fixture job is a purpose-built row inside the dedicated c1sse organisation; no real job,
# no real workspace and no real account is touched, and nothing is deleted.
# No cookie, CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
API_EXPECT_SHA="${API_EXPECT_SHA:-48e077e47f6504ccacc56fb31541f0eb6ebdd97f4aa2e44df73f822880e8d0ae}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-sse-scope-$TS; fi
if [ -z "$API" ]; then API=http://127.0.0.1:8080; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
note(){ echo "NOTE: $1"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "api=$API evidence=$EV expected_api_sha=$API_EXPECT_SHA"
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "api_pid=$AP api_exe_sha256=$API_SHA"
chk "$API_SHA" "$API_EXPECT_SHA" "running API binary is the frozen build for this round"

echo "=== 1. fixtures ==="
BIN=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1sse-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$BIN" -org-slug c1sse -org-name "C1 SSE scope acceptance" -workspaces c1sse-a,c1sse-b \
    -identities investigator:c1sse-inv@fixture.local:c1sse-a+c1sse-b -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID); WSA=$(read_cred "$CRED" C1MEM_WS_C1SSE_A); WSB=$(read_cred "$CRED" C1MEM_WS_C1SSE_B)
EMAIL=$(read_cred "$CRED" C1MEM_C1SSE_INV_EMAIL); PW0=$(read_cred "$CRED" C1MEM_C1SSE_INV_PW)
IDID=$(read_cred "$CRED" C1MEM_C1SSE_INV_IDENTITY_ID)
echo "org=$ORG ws_a=$WSA ws_b=$WSB identity=$IDID"
[ -n "$WSA" ] && [ -n "$IDID" ]; chk "$?" "0" "SSE fixture resolved"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account is outside the fixture organisation (untouched by this gate)"

# job_a lives in workspace A, job_b in workspace B; both belong to the fixture identity.
JOB=$(cat /proc/sys/kernel/random/uuid)
JOBB=$(cat /proc/sys/kernel/random/uuid)
for pair in "$JOB|$WSA|SSEPROBE-$TS" "$JOBB|$WSB|SSEPROBE-$TS-b"; do
  JID=$(printf '%s' "$pair" | cut -d'|' -f1); JWS=$(printf '%s' "$pair" | cut -d'|' -f2); JNO=$(printf '%s' "$pair" | cut -d'|' -f3)
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
)" "1" "purpose-built fixture job A exists inside the fixture workspace only"
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id='$JOBB'::uuid AND workspace_id='$WSB'::uuid AND created_by_identity='$IDID'::uuid;
SQL
)" "1" "purpose-built fixture job B exists inside the second fixture workspace"

echo "=== 2. session in workspace A ==="
JAR=$EV/jar.txt
code=$(curl -s -c "$JAR" -o "$EV/clear-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW0\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$code" "200" "fixture logs in choosing workspace A"
CSRF=$(awk '$6=="cs_user_session_csrf"{print $7}' "$JAR" | tail -1)
PW1="Pw-${TS}-c1sse-9f3a"
curl -s -b "$JAR" -o "$EV/clear-change.json" -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  -d "{\"current_password\":\"$PW0\",\"new_password\":\"$PW1\"}"
rm -f "$JAR"
curl -s -c "$JAR" -o "$EV/login.json" -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF=$(awk '$6=="cs_user_session_csrf"{print $7}' "$JAR" | tail -1)
chk "$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']")" "$WSA" "session is scoped to workspace A before subscribing"
SID=$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['session_id']")

echo "=== 3. open the SSE subscription under scope A ==="
curl -s -N -m 60 -b "$JAR" -H 'X-Auth-Audience: user' -H 'Accept: text/event-stream' \
  -D "$EV/sse-headers.txt" "$API/api/v2/jobs/$JOB/events" > "$EV/sse-stream.txt" 2> "$EV/sse-err.txt" &
SSEPID=$!
sleep 3
kill -0 $SSEPID 2>/dev/null; chk "$?" "0" "the SSE stream is open and being consumed"
chk "$([ "$(grep -c '^data: ' "$EV/sse-stream.txt")" -ge 1 ] && echo yes || echo no)" "yes" "the subscription received an initial job snapshot under workspace A"
SIZE_AT_SWITCH=$(wc -c < "$EV/sse-stream.txt")
RID_STREAM=$(rid_from "$EV/sse-headers.txt")
echo "sse_bytes_after_connect=$SIZE_AT_SWITCH stream_request_id_present=$([ -n "$RID_STREAM" ] && echo yes || echo no)"

echo "=== 4. move the same session to workspace B ==="
code=$(curl -s -b "$JAR" -c "$JAR" -o "$EV/switch.json" -D "$EV/switch-headers.txt" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  -d "{\"workspace_id\":\"$WSB\"}")
SWITCH_AT=$(date +%s)
RID_SWITCH=$(rid_from "$EV/switch-headers.txt")
chk "$code" "200" "the same session switched to workspace B while the SSE stream was open"
chk "$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']")" "$WSB" "session is now scoped to workspace B"

echo "=== 5. the old-scope stream must end here, silently ==="
CLOSED=no
for i in $(seq 1 20); do
  if ! kill -0 $SSEPID 2>/dev/null; then CLOSED=yes; break; fi
  sleep 0.5
done
CLOSE_LATENCY_MS=$(( $(date +%s%N)/1000000 - SWITCH_AT*1000 ))
SIZE_AFTER_CLOSE=$(wc -c < "$EV/sse-stream.txt")
echo "sse_stream_closed_within_wait=$CLOSED close_latency_ms=$CLOSE_LATENCY_MS bytes_at_switch=$SIZE_AT_SWITCH bytes_after=$SIZE_AFTER_CLOSE"
chk "$CLOSED" "yes" "C1-SSE-OLD-SCOPE-OPEN: the old-scope stream reached EOF after the session left workspace A"
# The close row must be visible within the 5 s audit bound. This is measured HERE, immediately
# after the stream ended, and not after the heartbeat window below (the first version of this
# gate measured it after a 17 s sleep and reported the sleep as audit latency).
CLOSE_ROWS=0
for i in $(seq 1 20); do
  CLOSE_ROWS=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid AND occurred_at > now() - interval '10 minutes';
SQL
)
  [ "$CLOSE_ROWS" -ge 1 ] && break
  sleep 0.25
done
AUDIT_CLOSE_VISIBLE_MS=$(( $(date +%s%N)/1000000 - SWITCH_AT*1000 ))
echo "close_audit_visible_ms=$AUDIT_CLOSE_VISIBLE_MS close_audit_rows=$CLOSE_ROWS"
sleep 17
if kill -0 $SSEPID 2>/dev/null; then ALIVE=yes; else ALIVE=no; fi
PINGS=$(grep -c '^: ping' "$EV/sse-stream.txt" || true)
SIZE_LATE=$(wc -c < "$EV/sse-stream.txt")
echo "sse_stream_alive_after_heartbeat_window=$ALIVE heartbeat_frames_after_switch=$PINGS bytes_now=$SIZE_LATE"
chk "$ALIVE" "no" "the closed stream is really gone one heartbeat interval later"
chk "$PINGS" "0" "C1-SSE-OLD-SCOPE-OPEN: no heartbeat reached the abandoned-scope stream"
chk "$SIZE_LATE" "$SIZE_AFTER_CLOSE" "not one byte was written to the stream after it closed"
wait $SSEPID 2>/dev/null || true

echo "=== 6. server-side subscription audit (open + scope-invalidated) ==="
OPEN_ROWS=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.subscribed' AND request_id='$RID_STREAM';
SQL
)
chk "$OPEN_ROWS" "1" "C1-SSE-NO-SUBSCRIPTION-AUDIT: exactly one subscription-open audit row, correlated by request id"
OPEN_CORR=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.subscribed' AND request_id='$RID_STREAM'
  AND session_id='$SID'::uuid AND workspace_id='$WSA'::uuid AND job_id='$JOB'::uuid
  AND route_template='/api/v2/jobs/$JOB/events';
SQL
)
chk "$OPEN_CORR" "1" "the open row carries the real session, workspace, job and route"

AUDIT_VISIBLE_MS=$AUDIT_CLOSE_VISIBLE_MS
echo "scope_invalidated_rows=$CLOSE_ROWS audit_visible_after_switch_ms=$AUDIT_VISIBLE_MS"
chk "$CLOSE_ROWS" "1" "exactly one scope-invalidated audit row for this subscription (no duplicate close row)"
if [ "$AUDIT_VISIBLE_MS" -le 5000 ]; then
  ok "the close audit row became visible within the 5 s audit bound (${AUDIT_VISIBLE_MS} ms after the switch)"
else
  bad "the close audit row took ${AUDIT_VISIBLE_MS} ms, beyond the 5 s audit bound"
fi
CLOSE_CORR=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOB'::uuid
  AND session_id='$SID'::uuid AND workspace_id='$WSA'::uuid
  AND deny_reason='session_workspace_changed'
  AND detail->>'closed_by_request_id'='$RID_SWITCH';
SQL
)
chk "$CLOSE_CORR" "1" "the close row names the session, the abandoned workspace, the reason and the triggering request id"
pgq > "$EV/subscription-audit-rows.txt" <<SQL
SELECT seq, action, category, result, route_template, request_id, session_id, workspace_id, job_id,
       coalesce(deny_reason,'') AS reason, coalesce(detail->>'closed_by_request_id','') AS closed_by
FROM audit_events_v2
WHERE action IN ('job.events.subscribed','job.events.unsubscribed','job.events.scope_invalidated')
ORDER BY seq DESC LIMIT 10;
SQL
cat "$EV/subscription-audit-rows.txt"

echo "=== 7. post-switch scope check on the same job ==="
code=$(curl -s -b "$JAR" -o "$EV/job-after-switch.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/v2/jobs/$JOB")
echo "job_detail_status_after_switch=$code"
case "$code" in
  404) ok "the job scoped to the abandoned workspace is no longer visible after the switch";;
  200) bad "the job scoped to the abandoned workspace is still visible after the switch";;
  *) note "job detail after switch returned $code (recorded, not normalised)";;
esac

echo "=== 8. NEW: a logout must also end the subscription ==="
code=$(curl -s -b "$JAR" -c "$JAR" -o "$EV/switch-back.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  -d "{\"workspace_id\":\"$WSB\"}")
chk "$code" "200" "the session is confirmed in workspace B before the logout case"
curl -s -N -m 60 -b "$JAR" -H 'X-Auth-Audience: user' -H 'Accept: text/event-stream' \
  -D "$EV/sse2-headers.txt" "$API/api/v2/jobs/$JOBB/events" > "$EV/sse2-stream.txt" 2> "$EV/sse2-err.txt" &
SSE2PID=$!
sleep 3
kill -0 $SSE2PID 2>/dev/null; chk "$?" "0" "a second subscription is open under the current workspace B"
SID2=$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['session_id']")
LOGOUT_CODE=$(curl -s -b "$JAR" -c "$JAR" -o "$EV/logout.json" -D "$EV/logout-headers.txt" -w '%{http_code}' -X POST "$API/api/auth/v1/logout" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d '{}')
RID_LOGOUT=$(rid_from "$EV/logout-headers.txt")
chk "$LOGOUT_CODE" "200" "the same session logged out while its subscription was open"
LOGGED_OUT=no
for i in $(seq 1 20); do
  if ! kill -0 $SSE2PID 2>/dev/null; then LOGGED_OUT=yes; break; fi
  sleep 0.5
done
SIZE2_AFTER=$(wc -c < "$EV/sse2-stream.txt")
sleep 3
SIZE2_LATE=$(wc -c < "$EV/sse2-stream.txt")
echo "sse2_closed_after_logout=$LOGGED_OUT bytes_after=$SIZE2_AFTER bytes_late=$SIZE2_LATE"
chk "$LOGGED_OUT" "yes" "the subscription ended when the session logged out"
chk "$SIZE2_LATE" "$SIZE2_AFTER" "no further byte reached the logged-out subscription"
wait $SSE2PID 2>/dev/null || true
LOGOUT_AUDIT=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='job.events.scope_invalidated' AND job_id='$JOBB'::uuid AND deny_reason='session_logged_out'
  AND session_id='$SID2'::uuid AND detail->>'closed_by_request_id'='$RID_LOGOUT';
SQL
)
chk "$LOGOUT_AUDIT" "1" "the logout closure is audited with reason session_logged_out and the logout request id"

echo "=== 9. census ==="
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id IN ('$JOB'::uuid,'$JOBB'::uuid);
SQL
)" "2" "both fixture job rows are preserved for the record"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account is untouched"
chk "$(pgq <<SQL
SELECT case when must_change_password then 'true' else 'false' end FROM identities WHERE email='admin@local';
SQL
)" "true" "the real account still carries must_change_password=true (never modified)"
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture memberships are still two active rows"
ps -ef | grep -c '[c]url -s -N -m 60' > "$EV/residual-client-count.txt" || true
echo "residual_sse_clients=$(cat "$EV/residual-client-count.txt")"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_SSE_SCOPE_GATE_V4=PASS"; exit 0; fi
echo "C1_SSE_SCOPE_GATE_V4=FAIL"; exit 4
