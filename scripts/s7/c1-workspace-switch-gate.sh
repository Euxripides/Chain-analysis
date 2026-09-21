#!/usr/bin/env bash
# c1-workspace-switch-gate.sh -- post-login workspace switch: real API + PG + audit gate.
#
# Covers: session rebinding, CSRF end-to-end negatives, membership fail-closed,
# validation negatives, cross-organization refusal, audit/login-event binding by
# request_id, idempotency, and fixture restore.
# ASCII only. Never prints credentials. Appends only audit/login events.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
# Optional overrides are read before nounset is re-armed (attempt 1 failed here:
# "EV: unbound variable" under set -u). ASCII-only, no parameter-default syntax.
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-workspace-switch-gate-$TS; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }

set +u
if [ -z "$API" ]; then API=http://127.0.0.1:8080; fi
set -u
JAR=$EV/cookies.txt
MULTI=/home/euripides/.p1gate-multi.env
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "api=$API evidence=$EV"

# Fixture keys are S7JS_INVESTIGATOR_EMAIL / _PW: the prefix contains a digit, so a
# ^[A-Z_]*_EMAIL= pattern does not match (attempt 2 failed with FIXTURE_MISSING).
email=$(grep -E '_EMAIL=' "$MULTI" | head -1 | cut -d= -f2-)
pw=$(grep -E '_PW=' "$MULTI" | head -1 | cut -d= -f2-)
if [ -z "$email" ] || [ -z "$pw" ]; then echo "FIXTURE_MISSING"; exit 9; fi
echo "fixture_email_masked=$(echo "$email" | sed 's/^\(.\).*@/\1***@/')"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
# jlen evaluates a path under d and prints its length (attempt 3 passed a full
# len(...) expression to jget, which prefixes "d" and therefore failed).
jlen(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(len(eval('d'+sys.argv[1])))" "$1" 2>/dev/null || echo "__ERR__"; }

echo "=== 1. login selector contract via API ==="
CODE=$(curl -s -o "$EV/1-login-no-ws.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\"}")
chk "$CODE" "409" "multi-member login without workspace_id is 409"
chk "$(jget "['error']['code']" < "$EV/1-login-no-ws.json")" "AUTHN_WORKSPACE_SELECTION_REQUIRED" "409 carries workspace selection code"
NC=$(jlen "['error']['details']['workspaces']" < "$EV/1-login-no-ws.json")
chk "$NC" "2" "selection list has exactly 2 member workspaces"
WS_A=$(jget "['error']['details']['workspaces'][0]['id']" < "$EV/1-login-no-ws.json")
WS_B=$(jget "['error']['details']['workspaces'][1]['id']" < "$EV/1-login-no-ws.json")
echo "ws_a=$WS_A ws_b=$WS_B"

echo "=== 2. explicit login binds workspace A ==="
CODE=$(curl -s -c "$JAR" -o "$EV/2-login-a.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$WS_A\"}")
chk "$CODE" "200" "explicit workspace login is 200"
CSRF=$(awk '$6=="cs_user_session_csrf"{print $7}' "$JAR" | tail -1)
if [ -n "$CSRF" ]; then ok "csrf cookie present (value not printed)"; else bad "csrf cookie missing"; fi

sess_ws(){ curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']"; }
chk "$(sess_ws)" "$WS_A" "session bound to explicitly chosen workspace A"

echo "=== 3. options endpoint ==="
curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session/workspaces" -o "$EV/3-options.json"
chk "$(jget "['data']['current_workspace_id']" < "$EV/3-options.json")" "$WS_A" "options endpoint reports current workspace"
chk "$(jlen "['data']['workspaces']" < "$EV/3-options.json")" "2" "options endpoint lists the 2 memberships"

echo "=== 4. CSRF end-to-end negatives ==="
post_switch(){
  local body="$1"; local out="$2"; shift 2
  curl -s -b "$JAR" -o "$EV/$out" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' "$@" -d "$body"
}
CODE=$(post_switch "{\"workspace_id\":\"$WS_B\"}" "4a-csrf-missing.json")
chk "$CODE" "403" "switch without CSRF header is 403"
chk "$(jget "['error']['code']" < "$EV/4a-csrf-missing.json")" "AUTHZ_CSRF_FAILED" "missing CSRF returns AUTHZ_CSRF_FAILED"
CODE=$(post_switch "{\"workspace_id\":\"$WS_B\"}" "4b-csrf-wrong.json" -H "X-CSRF-Token: deadbeefdeadbeef")
chk "$CODE" "403" "switch with wrong CSRF token is 403"
CODE=$(curl -s -o "$EV/4c-csrf-no-cookie.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d "{\"workspace_id\":\"$WS_B\"}")
chk "$CODE" "401" "switch with CSRF header but no session cookie is 401"
chk "$(sess_ws)" "$WS_A" "session unchanged after all CSRF negatives"

echo "=== 5. validation + membership negatives ==="
CODE=$(post_switch "{}" "5a-missing-field.json" -H "X-CSRF-Token: $CSRF")
chk "$CODE" "400" "switch without workspace_id is 400"
CODE=$(post_switch "{\"workspace_id\":\"   \"}" "5b-blank-field.json" -H "X-CSRF-Token: $CSRF")
chk "$CODE" "400" "switch with blank workspace_id is 400"
CODE=$(post_switch "{\"workspace_id\":\"not-a-uuid\"}" "5c-bad-uuid.json" -H "X-CSRF-Token: $CSRF")
echo "non_uuid_workspace_status=$CODE"

NONMEMBER=$(pgq <<SQL
SELECT w.id FROM workspaces w
 WHERE w.organization_id = (SELECT organization_id FROM identities WHERE email='$email')
   AND w.id::text NOT IN ('$WS_A','$WS_B')
 ORDER BY w.slug LIMIT 1;
SQL
)
RID_DENY=""
if [ -n "$NONMEMBER" ]; then
  CODE=$(post_switch "{\"workspace_id\":\"$NONMEMBER\"}" "5d-nonmember.json" -H "X-CSRF-Token: $CSRF")
  chk "$CODE" "403" "switch to same-org non-member workspace is 403"
  chk "$(jget "['error']['code']" < "$EV/5d-nonmember.json")" "AUTHZ_WORKSPACE_NOT_MEMBER" "non-member returns AUTHZ_WORKSPACE_NOT_MEMBER"
  chk "$(sess_ws)" "$WS_A" "session unchanged after non-member refusal"
  RID_DENY=$(jget "['meta']['request_id']" < "$EV/5d-nonmember.json")
else
  echo "SKIP no same-org non-member workspace exists"
fi

CROSSORG=$(pgq <<SQL
SELECT w.id FROM workspaces w
 WHERE w.organization_id <> (SELECT organization_id FROM identities WHERE email='$email')
 ORDER BY w.organization_id, w.slug LIMIT 1;
SQL
)
if [ -n "$CROSSORG" ]; then
  CODE=$(post_switch "{\"workspace_id\":\"$CROSSORG\"}" "5e-crossorg.json" -H "X-CSRF-Token: $CSRF")
  chk "$CODE" "403" "switch to another organization's workspace is 403"
  chk "$(sess_ws)" "$WS_A" "session unchanged after cross-org refusal"
else
  echo "SKIP no cross-org workspace exists"
fi

echo "=== 6. positive switch + PG + audit binding ==="
CODE=$(post_switch "{\"workspace_id\":\"$WS_B\"}" "6a-switch-b.json" -H "X-CSRF-Token: $CSRF")
chk "$CODE" "200" "valid switch to member workspace B is 200"
chk "$(jget "['data']['state']" < "$EV/6a-switch-b.json")" "switched" "response state=switched"
chk "$(jget "['data']['previous_workspace_id']" < "$EV/6a-switch-b.json")" "$WS_A" "response reports previous workspace A"
RID_OK=$(jget "['meta']['request_id']" < "$EV/6a-switch-b.json")
echo "switch_request_id=$RID_OK"
chk "$(sess_ws)" "$WS_B" "session now bound to workspace B"

PGWS=$(pgq <<SQL
SELECT s.workspace_id::text FROM sessions s
 WHERE s.id = (SELECT session_id FROM login_events
                WHERE request_id='$RID_OK' AND event_type='auth.workspace.switch' LIMIT 1);
SQL
)
chk "$PGWS" "$WS_B" "PG sessions.workspace_id matches switched workspace"

LE=$(pgq <<SQL
SELECT count(*) FROM login_events
 WHERE request_id='$RID_OK' AND event_type='auth.workspace.switch' AND result='success'
   AND workspace_id='$WS_B'::uuid AND detail->>'previous_workspace_id'='$WS_A';
SQL
)
chk "$LE" "1" "login_events has exactly one success switch event bound to request_id"
AE=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
 WHERE request_id='$RID_OK' AND action='authorization.workspace_switched' AND result='success';
SQL
)
chk "$AE" "1" "audit_events_v2 has exactly one success switch event bound to same request_id"

if [ -n "$RID_DENY" ]; then
  AED=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
 WHERE request_id='$RID_DENY' AND action='authorization.workspace_switched' AND result='deny'
   AND detail->>'reason'='workspace_not_member';
SQL
)
  chk "$AED" "1" "audit_events_v2 records the non-member refusal as deny"
fi

echo "=== 7. idempotency + fixture restore ==="
CODE=$(post_switch "{\"workspace_id\":\"$WS_B\"}" "7a-idempotent.json" -H "X-CSRF-Token: $CSRF")
chk "$CODE" "200" "re-selecting the current workspace is 200"
chk "$(jget "['data']['state']" < "$EV/7a-idempotent.json")" "unchanged" "re-selecting current workspace reports unchanged"

CODE=$(post_switch "{\"workspace_id\":\"$WS_A\"}" "7b-restore-a.json" -H "X-CSRF-Token: $CSRF")
chk "$CODE" "200" "restore switch back to workspace A is 200"
chk "$(sess_ws)" "$WS_A" "fixture restored: session back on workspace A"

curl -s -b "$JAR" -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -o "$EV/8-logout.json"
echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
[ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ] && echo "C1_SWITCH_GATE=PASS" || echo "C1_SWITCH_GATE=FAIL"
exit $([ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ] && echo 0 || echo 4)
