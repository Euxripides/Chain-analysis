#!/usr/bin/env bash
# c1-sse-scope-gate-v3.sh -- C round A5: SSE scope convergence.
#
# Property under test: an SSE subscription that was opened while the session was scoped to
# workspace A must NOT keep streaming after the same session moves to workspace B, and the
# server must record a subscription audit trail. Today the subscription is bound to a job id
# at connect time only (backend/internal/api/sse.go SSEHub.Subscribe takes no session), so the
# honest expectation is that this gate FAILS and produces the defect evidence.
#
# The fixture job is a purpose-built row inside the dedicated c1sse organisation; no real job,
# no real workspace and no real account is touched, and nothing is deleted.
# No cookie, CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
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
echo "api=$API evidence=$EV"
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "api_pid=$AP api_exe_sha256=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"

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

JOB=$(cat /proc/sys/kernel/random/uuid)
pgq > "$EV/job-insert.txt" <<SQL
INSERT INTO download_jobs (id, job_no, status, chain, range_type, datasets,
                           organization_id, workspace_id, created_by_identity, source)
VALUES ('$JOB'::uuid, 'SSEPROBE-$TS', 'RUNNING', 'tron', 'block_range', ARRAY['transactions'],
        '$ORG'::uuid, '$WSA'::uuid, '$IDID'::uuid, 'acceptance_probe');
SQL
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id='$JOB'::uuid AND workspace_id='$WSA'::uuid AND created_by_identity='$IDID'::uuid;
SQL
)" "1" "purpose-built fixture job exists inside the fixture workspace only"

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

echo "=== 3. open the SSE subscription under scope A ==="
AUDIT_BEFORE=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE occurred_at > now() - interval '1 hour';
SQL
)
curl -s -N -m 60 -b "$JAR" -H 'X-Auth-Audience: user' -H 'Accept: text/event-stream' \
  "$API/api/v2/jobs/$JOB/events" > "$EV/sse-stream.txt" 2> "$EV/sse-err.txt" &
SSEPID=$!
sleep 3
kill -0 $SSEPID 2>/dev/null; chk "$?" "0" "the SSE stream is open and being consumed"
chk "$([ "$(grep -c '^data: ' "$EV/sse-stream.txt")" -ge 1 ] && echo yes || echo no)" "yes" "the subscription received an initial job snapshot under workspace A"
echo "sse_bytes_after_connect=$(wc -c < "$EV/sse-stream.txt")"

echo "=== 4. move the same session to workspace B ==="
code=$(curl -s -b "$JAR" -o "$EV/switch.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  -d "{\"workspace_id\":\"$WSB\"}")
chk "$code" "200" "the same session switched to workspace B while the SSE stream was open"
chk "$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']")" "$WSB" "session is now scoped to workspace B"

echo "=== 5. did the old-scope stream survive the switch? ==="
sleep 18
PINGS=$(grep -c '^: ping' "$EV/sse-stream.txt" || true)
if kill -0 $SSEPID 2>/dev/null; then ALIVE=yes; else ALIVE=no; fi
echo "sse_stream_alive_after_switch=$ALIVE heartbeat_frames_after_switch=$PINGS sse_bytes=$(wc -c < "$EV/sse-stream.txt")"
if [ "$ALIVE" = "no" ]; then
  ok "the old-scope SSE stream was closed by the server when the session left workspace A"
else
  bad "C1-SSE-OLD-SCOPE-OPEN: the SSE stream opened under workspace A is still open after the session moved to workspace B"
fi
if [ "$PINGS" -ge 1 ]; then
  bad "C1-SSE-OLD-SCOPE-OPEN: the abandoned-scope stream is still receiving server frames (heartbeat count=$PINGS)"
else
  ok "no server frame reached the abandoned-scope stream"
fi
kill $SSEPID 2>/dev/null || true
wait $SSEPID 2>/dev/null || true

echo "=== 6. server-side subscription audit ==="
AUDIT_AFTER=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE occurred_at > now() - interval '1 hour';
SQL
)
SSE_AUDIT=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE occurred_at > now() - interval '1 hour'
  AND (action ILIKE '%sse%' OR action ILIKE '%stream%' OR action ILIKE '%subscribe%' OR route_template ILIKE '%/jobs/%/events%');
SQL
)
echo "audit_rows_1h before=$AUDIT_BEFORE after=$AUDIT_AFTER subscription_scoped=$SSE_AUDIT"
# v2 harness defect: the v2 predicate matched every admin audit-events route, so four unrelated rows
# produced a false PASS. The v3 predicate only matches the SSE route template or an explicit SSE
# action name; unrelated admin rows can no longer satisfy it.
pgq > "$EV/subscription-audit-rows.txt" <<SQL
SELECT seq, action, category, route_template, result FROM audit_events_v2
WHERE occurred_at > now() - interval '1 hour'
  AND (action ILIKE '%sse%' OR action ILIKE '%stream%' OR action ILIKE '%subscribe%'
       OR route_template ILIKE '%/jobs/%/events%')
ORDER BY seq DESC LIMIT 10;
SQL
cat "$EV/subscription-audit-rows.txt"
if [ -n "$SSE_AUDIT" ] && [ "$SSE_AUDIT" -ge 1 ]; then
  ok "the server recorded a subscription-scoped audit row"
else
  bad "C1-SSE-NO-SUBSCRIPTION-AUDIT: opening and closing an SSE subscription produces no audit row"
fi

echo "=== 7. post-switch scope check on the same job ==="
code=$(curl -s -b "$JAR" -o "$EV/job-after-switch.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/v2/jobs/$JOB")
echo "job_detail_status_after_switch=$code"
case "$code" in
  404) ok "the job scoped to the abandoned workspace is no longer visible after the switch";;
  200) bad "the job scoped to the abandoned workspace is still visible after the switch";;
  *) note "job detail after switch returned $code (recorded, not normalised)";;
esac

echo "=== 8. census ==="
chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs WHERE id='$JOB'::uuid;
SQL
)" "1" "the fixture job row is preserved for the record"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account is untouched"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_SSE_SCOPE_GATE=PASS"; exit 0; fi
echo "C1_SSE_SCOPE_GATE=FAIL"; exit 4
