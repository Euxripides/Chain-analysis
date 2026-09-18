#!/usr/bin/env bash
# c1-session-scope-convergence-gate-v1.sh -- C round A5: real write-side scope convergence.
#
# Covers four properties the previous rounds did not:
#   1. an in-flight WRITE authorised under the OLD workspace scope must not create an object
#      after that scope stops being valid (membership revoked mid-request);
#   2. the same race when the user SWITCHES workspace mid-request;
#   3. a request that still carries the old workspace scope after a switch;
#   4. multi-tab and multi-device convergence: independent sessions must not contaminate each
#      other, and a switch on one session must not move another.
#
# Every object is created inside a dedicated c1conv organisation; the real admin@local account,
# its password, its memberships and every pre-existing case row are untouched, and the census is
# asserted before/after. Nothing is deleted: the fixture objects stay as evidence.
# No cookie, CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-session-scope-convergence-$TS; fi
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
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

echo "=== 0. runtime identity ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "api_pid=$AP api_exe_sha256=$API_SHA"
chk "$API_SHA" "39e0f74772237fb1bc20c9cc7fcfbfec73463779d20de0dff743062351e267f7" "running API binary is the frozen build for this round"

echo "=== 1. fixtures (dedicated c1conv organisation, two member workspaces) ==="
BIN=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1conv-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$BIN" -org-slug c1conv -org-name "C1 session scope convergence acceptance" \
    -workspaces c1conv-a,c1conv-b \
    -identities investigator:c1conv-inv@fixture.local:c1conv-a+c1conv-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1CONV_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1CONV_B)
EMAIL=$(read_cred "$CRED" C1MEM_C1CONV_INV_EMAIL)
PW0=$(read_cred "$CRED" C1MEM_C1CONV_INV_PW)
IDID=$(read_cred "$CRED" C1MEM_C1CONV_INV_IDENTITY_ID)
echo "org=$ORG ws_a=$WSA ws_b=$WSB identity=$IDID"
[ -n "$ORG" ] && [ -n "$WSA" ] && [ -n "$WSB" ]; chk "$?" "0" "convergence fixture resolved"
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture identity really holds two active memberships"
REAL0=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id NOT IN ('$WSA'::uuid,'$WSB'::uuid);
SQL
)
echo "real_cases_outside_fixture_workspaces=$REAL0"

echo "=== 2. clear the initial-password flag through the REAL product endpoint ==="
JAR1=$EV/jar-tab1.txt
code=$(curl -s -c "$JAR1" -o "$EV/clear-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW0\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$code" "200" "tab1 logs in choosing workspace A"
CSRF1=$(awk '$6=="cs_user_session_csrf"{print $7}' "$JAR1" | tail -1)
[ -n "$CSRF1" ]; chk "$?" "0" "tab1 CSRF cookie present (value never printed)"
PW1="Pw-${TS}-c1conv-9f3a"
code=$(curl -s -b "$JAR1" -o "$EV/clear-change.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF1" \
  -d "{\"current_password\":\"$PW0\",\"new_password\":\"$PW1\"}")
[ "$code" = "200" ] || [ "$code" = "204" ]; chk "$?" "0" "initial-password flag cleared through the real endpoint (status=$code)"

csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" | tail -1; }
session_ws(){ curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['workspace_id']"; }
session_id_of(){ curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['session_id']"; }
login_tab(){ local jar="$1" ws="$2" ua="$3" c
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -A "$ua" \
    -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
case_count(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$1'::uuid AND name LIKE 'RACE-${TS}%';
SQL
}
make_body(){ { printf '{"case_no":"RACE-%s-%s","name":"RACE-%s-%s","description":"' "$TS" "$2" "$TS" "$2"
    head -c "$3" /dev/zero | tr '\0' 'x'; printf '"}'; } > "$1"; }

echo "=== 3. RACE A: membership revoked while a write is already in flight ==="
A_BEFORE=$(case_count "$WSA")
make_body "$EV/race-a-body.json" arev 160000
S=$(date +%s)
curl -s -b "$JAR1" -o "$EV/race-a-resp.json" -D "$EV/race-a-resp.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" --data-binary "@$EV/race-a-body.json" --limit-rate 40k \
  > "$EV/race-a-code.txt" 2> "$EV/race-a-err.txt" &
CURLPID=$!
sleep 1
pgq > "$EV/race-a-revoke.txt" <<SQL
UPDATE workspace_memberships SET status='removed', updated_at=now()
WHERE identity_id='$IDID'::uuid AND workspace_id='$WSA'::uuid;
SQL
wait $CURLPID
ELAPSED=$(( $(date +%s) - S ))
RACE_A_CODE=$(cat "$EV/race-a-code.txt")
echo "race_a_elapsed_seconds=$ELAPSED http_status=$RACE_A_CODE"
if [ "$ELAPSED" -lt 2 ]; then
  bad "the throttled request finished in ${ELAPSED}s: the race window was not real, result not usable"
else
  ok "the write was genuinely in flight for ${ELAPSED}s while the membership was revoked"
fi
A_AFTER=$(case_count "$WSA")
echo "cases_named_RACE_in_A before=$A_BEFORE after=$A_AFTER"
if [ "$A_AFTER" = "$A_BEFORE" ]; then
  ok "RACE A: no case was created in workspace A after the membership was revoked mid-request"
else
  bad "RACE A: $((A_AFTER-A_BEFORE)) case(s) were created in workspace A although the membership was revoked mid-request"
fi
RID_A=$(rid_from "$EV/race-a-resp.h")
if [ -n "$RID_A" ]; then
  DENY_A=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$RID_A' AND result='deny';
SQL
)
  echo "race_a_deny_audit_rows=$DENY_A"
  if [ "$DENY_A" -ge 1 ]; then ok "RACE A: the in-flight write produced an explicit deny audit row"; else bad "RACE A: no deny audit row for the in-flight write"; fi
else
  note "RACE A response carried no X-Request-Id, so deny-audit correlation could not be asserted"
fi
pgq > "$EV/race-a-restore.txt" <<SQL
UPDATE workspace_memberships SET status='active', updated_at=now()
WHERE identity_id='$IDID'::uuid AND workspace_id='$WSA'::uuid;
SQL
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture memberships restored to two active rows after RACE A"

echo "=== 4. RACE B: workspace switched while a write is already in flight ==="
rm -f "$JAR1"
curl -s -c "$JAR1" -o "$EV/relogin.json" -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF1=$(csrf_of "$JAR1")
chk "$(session_ws "$JAR1")" "$WSA" "tab1 session is back in workspace A for RACE B"
A_BEFORE=$(case_count "$WSA"); B_BEFORE=$(case_count "$WSB")
make_body "$EV/race-b-body.json" asw 160000
curl -s -b "$JAR1" -o "$EV/race-b-resp.json" -D "$EV/race-b-resp.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" --data-binary "@$EV/race-b-body.json" --limit-rate 40k \
  > "$EV/race-b-code.txt" 2> "$EV/race-b-err.txt" &
CURLPID=$!
sleep 1
curl -s -b "$JAR1" -o "$EV/race-b-switch.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF1" \
  -d "{\"workspace_id\":\"$WSB\"}" > "$EV/race-b-switch-code.txt"
wait $CURLPID
RACE_B_CODE=$(cat "$EV/race-b-code.txt")
A_AFTER=$(case_count "$WSA"); B_AFTER=$(case_count "$WSB")
echo "race_b_switch_status=$(cat "$EV/race-b-switch-code.txt") write_status=$RACE_B_CODE"
echo "cases_named_RACE in A: $A_BEFORE->$A_AFTER ; in B: $B_BEFORE->$B_AFTER"
if [ "$((A_AFTER-A_BEFORE))" -eq 0 ] && [ "$((B_AFTER-B_BEFORE))" -eq 0 ]; then
  ok "RACE B: the in-flight write created nothing in either workspace (fail closed)"
elif [ "$((A_AFTER-A_BEFORE))" -eq 0 ]; then
  note "RACE B: the in-flight write committed into the NEW workspace B ($((B_AFTER-B_BEFORE)) object(s)); nothing landed in the abandoned scope A"
  ok "RACE B: the in-flight write did not land in the abandoned workspace A"
else
  bad "RACE B: the in-flight write landed in the abandoned workspace A ($((A_AFTER-A_BEFORE)) object(s))"
fi
RID_B=$(rid_from "$EV/race-b-resp.h")
if [ -n "$RID_B" ]; then
  echo "race_b_audit_rows=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$RID_B';
SQL
)"
fi

echo "=== 5. stale scope after a switch ==="
chk "$(session_ws "$JAR1")" "$WSB" "tab1 session is in workspace B after the switch"
A_BEFORE=$(case_count "$WSA")
code=$(curl -s -b "$JAR1" -o "$EV/stale-scope.json" -D "$EV/stale-scope.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" -H "X-Workspace-Id: $WSA" \
  -d "{\"case_no\":\"RACE-${TS}-stale\",\"name\":\"RACE-${TS}-stale\"}")
A_AFTER=$(case_count "$WSA")
echo "stale_scope_http_status=$code cases_in_A $A_BEFORE->$A_AFTER"
if [ "$A_AFTER" = "$A_BEFORE" ]; then
  ok "a request still claiming the abandoned workspace created nothing in it"
else
  bad "a request still claiming the abandoned workspace created an object in it"
fi
if [ "$code" = "201" ]; then
  note "the stale X-Workspace-Id header was ignored and the object was placed in the session workspace B (server-derived scope wins)"
elif [ "$code" = "403" ] || [ "$code" = "409" ] || [ "$code" = "422" ]; then
  ok "the stale workspace claim was refused with $code"
else
  note "stale workspace claim produced status $code (recorded, not normalised)"
fi

echo "=== 6. multi-tab convergence ==="
JAR2=$EV/jar-tab2.txt
C2=$(login_tab "$JAR2" "$WSA" "c1conv-tab2/1.0")
chk "$C2" "200" "tab2 opens an independent session in workspace A"
CSRF2=$(csrf_of "$JAR2")
chk "$(session_ws "$JAR2")" "$WSA" "tab2 session starts in A"
SID1=$(session_id_of "$JAR1"); SID2=$(session_id_of "$JAR2")
if [ -n "$SID1" ] && [ -n "$SID2" ] && [ "$SID1" != "$SID2" ]; then ok "the two tabs hold distinct server-side sessions"; else bad "the two tabs do not hold distinct sessions"; fi
curl -s -b "$JAR1" -o "$EV/tab1-switch-A.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF1" \
  -d "{\"workspace_id\":\"$WSA\"}" > "$EV/tab1-switch-A-code.txt"
chk "$(cat "$EV/tab1-switch-A-code.txt")" "200" "tab1 switches back to workspace A"
chk "$(session_ws "$JAR1")" "$WSA" "tab1 session really moved to A"
chk "$(session_ws "$JAR2")" "$WSA" "tab2 session is unaffected by tab1 movement"
curl -s -b "$JAR2" -o "$EV/tab2-switch-B.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF2" \
  -d "{\"workspace_id\":\"$WSB\"}" > "$EV/tab2-switch-B-code.txt"
chk "$(cat "$EV/tab2-switch-B-code.txt")" "200" "tab2 switches independently to workspace B"
chk "$(session_ws "$JAR1")" "$WSA" "tab1 stays in A while tab2 is in B (no cross-tab contamination)"
chk "$(session_ws "$JAR2")" "$WSB" "tab2 really moved to B"

echo "=== 7. multi-device convergence ==="
JAR3=$EV/jar-device3.txt
C3=$(login_tab "$JAR3" "$WSB" "c1conv-device3/2.0 (different device)")
chk "$C3" "200" "a third session logs in from a different device in workspace B"
SID3=$(session_id_of "$JAR3")
DEV1=$(pgq <<SQL
SELECT coalesce(device_id::text,'') FROM sessions WHERE id='$SID1'::uuid;
SQL
)
DEV3=$(pgq <<SQL
SELECT coalesce(device_id::text,'') FROM sessions WHERE id='$SID3'::uuid;
SQL
)
if [ -n "$DEV1" ] && [ -n "$DEV3" ] && [ "$DEV1" != "$DEV3" ]; then ok "the two devices are recorded as distinct devices"; else bad "device identity was not distinguished between the two sessions"; fi
chk "$(pgq <<SQL
SELECT count(*) FROM sessions WHERE identity_id='$IDID'::uuid AND revoked_at IS NULL;
SQL
)" "3" "three independent live sessions exist for the fixture identity"
CSRF3=$(csrf_of "$JAR3")
curl -s -b "$JAR3" -o "$EV/device3-switch-A.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF3" \
  -d "{\"workspace_id\":\"$WSA\"}" > "$EV/device3-switch-A-code.txt"
chk "$(cat "$EV/device3-switch-A-code.txt")" "200" "device 3 switches to workspace A"
chk "$(session_ws "$JAR1")" "$WSA" "device 1 is unaffected by the device 3 switch"
chk "$(session_ws "$JAR2")" "$WSB" "device 2 is unaffected by the device 3 switch"

echo "=== 8. convergence census ==="
pgq > "$EV/census.txt" <<SQL
SELECT s.id::text, s.workspace_id::text, coalesce(w.slug,'?'), s.revoked_at IS NULL AS live
FROM sessions s LEFT JOIN workspaces w ON w.id=s.workspace_id
WHERE s.identity_id='$IDID'::uuid ORDER BY s.created_at;
SQL
cat "$EV/census.txt"
chk "$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id NOT IN ('$WSA'::uuid,'$WSB'::uuid);
SQL
)" "$REAL0" "no case outside the fixture workspaces was created or removed"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account is untouched"
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture memberships are restored to two active rows at the end"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_SESSION_SCOPE_CONVERGENCE_GATE=PASS"; exit 0; fi
echo "C1_SESSION_SCOPE_CONVERGENCE_GATE=FAIL"; exit 4
