#!/usr/bin/env bash
# c1-session-scope-convergence-gate-v4.sh -- C round A5, second authoritative round after the
# C1-SCOPE-RACE-STALE fix.
#
# Lineage: v3 (sha d6520eed...) produced the authoritative 31 PASS / 1 FAIL round that FOUND
# the defect; it is FROZEN and stays on record untouched. v4 keeps every v3 scenario and adds
# the ones the fix must now satisfy:
#
#   1. RACE A: membership revoked while a write is already in flight        -> fail closed (v3)
#   2. RACE B: workspace switched while a write is already in flight        -> NOW must be
#      refused with 409 AUTHZ_WORKSPACE_SCOPE_STALE, zero objects in BOTH workspaces, and
#      exactly one deny audit row carrying the old and the new scope (v3 measured 201 + 1 case
#      in the abandoned workspace: that is the defect this gate now pins shut)
#   3. stale X-Workspace-Id after a switch                                  -> unchanged (v3)
#   4. multi-tab and multi-device convergence                               -> unchanged (v3)
#   5. NEW session revoked (real logout) while a write is in flight         -> 409 session_missing
#   6. NEW anonymous state-changing request must stay 401, never 409        -> guard does not
#      hijack the unauthenticated decision
#   7. NEW client disconnect in the middle of the body                      -> zero objects
#
# Everything happens inside the dedicated c1conv fixture organisation. The real admin@local
# account, its password, its memberships and every pre-existing case row are untouched, and the
# census is asserted before and after. Nothing is deleted: fixture objects stay as evidence.
# No cookie, CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
API_EXPECT_SHA="${API_EXPECT_SHA:-48e077e47f6504ccacc56fb31541f0eb6ebdd97f4aa2e44df73f822880e8d0ae}"
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
echo "api=$API evidence=$EV expected_api_sha=$API_EXPECT_SHA"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

echo "=== 0. runtime identity ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "api_pid=$AP api_exe_sha256=$API_SHA"
chk "$API_SHA" "$API_EXPECT_SHA" "running API binary is the frozen build for this round"

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
session_ws(){ curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']"; }
session_id_of(){ curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
login_tab(){ local jar="$1" ws="$2" ua="$3" c
  c=$(curl -s -b "$jar" -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -A "$ua" \
    -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
case_count(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$1'::uuid AND name LIKE 'RACE-$TS%';
SQL
}
make_body(){ { printf '{"case_no":"RACE-%s-%s","name":"RACE-%s-%s","description":"' "$TS" "$2" "$TS" "$2"
    head -c "$3" /dev/zero | tr '\0' 'x'; printf '"}'; } > "$1"; }
# deny_audit <request_id> <expected_reason> <label>
deny_audit(){
  RID="$1"; WANT_REASON="$2"; LABEL="$3"
  if [ -z "$RID" ]; then bad "$LABEL: response carried no X-Request-Id, deny audit cannot be correlated"; return; fi
  ROW=$(pgq <<SQL
SELECT action||'|'||result||'|'||error_code||'|'||deny_reason||'|'||coalesce(detail->>'old_workspace_id','')||'|'||coalesce(detail->>'new_workspace_id','')||'|'||coalesce(detail->>'reason','')
FROM audit_events_v2 WHERE request_id='$RID' ORDER BY seq;
SQL
)
  echo "${LABEL}_audit_row=$ROW"
  N=$(printf '%s\n' "$ROW" | grep -c . )
  chk "$N" "1" "$LABEL: exactly one audit row for the refused in-flight write"
  printf '%s' "$ROW" | grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE'; chk "$?" "0" "$LABEL: audit row carries the stable refusal code"
  printf '%s' "$ROW" | grep -q "|deny|"; chk "$?" "0" "$LABEL: audit result is deny"
  printf '%s' "$ROW" | grep -q "|$WANT_REASON|"; chk "$?" "0" "$LABEL: audit deny_reason is $WANT_REASON"
}

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

echo "=== 4. RACE B (THE FIXED DEFECT): workspace switched while a write is already in flight ==="
rm -f "$JAR1"
curl -s -c "$JAR1" -o "$EV/relogin.json" -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF1=$(csrf_of "$JAR1")
chk "$(session_ws "$JAR1")" "$WSA" "tab1 session is back in workspace A for RACE B"
A_BEFORE=$(case_count "$WSA"); B_BEFORE=$(case_count "$WSB")
make_body "$EV/race-b-body.json" asw 160000
S=$(date +%s)
curl -s -b "$JAR1" -o "$EV/race-b-resp.json" -D "$EV/race-b-resp.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" --data-binary "@$EV/race-b-body.json" --limit-rate 40k \
  > "$EV/race-b-code.txt" 2> "$EV/race-b-err.txt" &
CURLPID=$!
sleep 1
curl -s -b "$JAR1" -c "$JAR1" -o "$EV/race-b-switch.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF1" \
  -d "{\"workspace_id\":\"$WSB\"}" > "$EV/race-b-switch-code.txt"
wait $CURLPID
ELAPSED=$(( $(date +%s) - S ))
RACE_B_CODE=$(cat "$EV/race-b-code.txt")
A_AFTER=$(case_count "$WSA"); B_AFTER=$(case_count "$WSB")
echo "race_b_elapsed_seconds=$ELAPSED race_b_switch_status=$(cat "$EV/race-b-switch-code.txt") write_status=$RACE_B_CODE"
echo "cases_named_RACE in A: $A_BEFORE->$A_AFTER ; in B: $B_BEFORE->$B_AFTER"
if [ "$ELAPSED" -lt 2 ]; then
  bad "RACE B: the throttled request finished in ${ELAPSED}s: the race window was not real"
else
  ok "RACE B: the write was genuinely in flight for ${ELAPSED}s while the session was switched"
fi
chk "$RACE_B_CODE" "409" "RACE B: the in-flight write is refused with 409"
grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE' "$EV/race-b-resp.json"; chk "$?" "0" "RACE B: refusal carries AUTHZ_WORKSPACE_SCOPE_STALE"
chk "$A_AFTER" "$A_BEFORE" "RACE B: zero new objects in the ABANDONED workspace A"
chk "$B_AFTER" "$B_BEFORE" "RACE B: zero new objects in the NEW workspace B (fail closed, not migrated)"
RID_B=$(rid_from "$EV/race-b-resp.h")
deny_audit "$RID_B" "session_workspace_changed" "RACE B"
OLD=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$RID_B' AND detail->>'old_workspace_id'='$WSA';
SQL
)
chk "$OLD" "1" "RACE B: the deny audit row records the OLD scope it was authorised under"
NEW=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$RID_B' AND detail->>'new_workspace_id'='$WSB';
SQL
)
chk "$NEW" "1" "RACE B: the deny audit row records the NEW workspace the session had moved to"
chk "$(session_ws "$JAR1")" "$WSB" "tab1 session really moved to workspace B"

echo "=== 5. stale scope after a switch ==="
A_BEFORE=$(case_count "$WSA")
code=$(curl -s -b "$JAR1" -o "$EV/stale-scope.json" -D "$EV/stale-scope.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" -H "X-Workspace-Id: $WSA" \
  -d "{\"case_no\":\"RACE-$TS-stale\",\"name\":\"RACE-$TS-stale\"}")
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
UA_DESKTOP="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
UA_MOBILE="Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
UA_TABLET="Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
echo "device_key_semantics=trusted_devices(identity_id,platform,browser) - recorded as observed design, not a defect"
C2=$(login_tab "$JAR2" "$WSA" "$UA_TABLET")
chk "$C2" "200" "tab2 opens an independent session in workspace A"
CSRF2=$(csrf_of "$JAR2")
chk "$(session_ws "$JAR2")" "$WSA" "tab2 session starts in A"
SID1=$(session_id_of "$JAR1"); SID2=$(session_id_of "$JAR2")
if [ -n "$SID1" ] && [ -n "$SID2" ] && [ "$SID1" != "$SID2" ]; then ok "the two tabs hold distinct server-side sessions"; else bad "the two tabs do not hold distinct sessions"; fi
curl -s -b "$JAR1" -c "$JAR1" -o "$EV/tab1-switch-A.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF1" \
  -d "{\"workspace_id\":\"$WSA\"}" > "$EV/tab1-switch-A-code.txt"
chk "$(cat "$EV/tab1-switch-A-code.txt")" "200" "tab1 switches back to workspace A"
chk "$(session_ws "$JAR1")" "$WSA" "tab1 session really moved to A"
chk "$(session_ws "$JAR2")" "$WSA" "tab2 session is unaffected by tab1 movement"
curl -s -b "$JAR2" -c "$JAR2" -o "$EV/tab2-switch-B.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF2" \
  -d "{\"workspace_id\":\"$WSB\"}" > "$EV/tab2-switch-B-code.txt"
chk "$(cat "$EV/tab2-switch-B-code.txt")" "200" "tab2 switches independently to workspace B"
chk "$(session_ws "$JAR1")" "$WSA" "tab1 stays in A while tab2 is in B (no cross-tab contamination)"
chk "$(session_ws "$JAR2")" "$WSB" "tab2 really moved to B"

echo "=== 7. multi-device convergence ==="
JAR3=$EV/jar-device3.txt
C3=$(login_tab "$JAR3" "$WSB" "$UA_DESKTOP")
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
curl -s -b "$JAR3" -c "$JAR3" -o "$EV/device3-switch-A.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF3" \
  -d "{\"workspace_id\":\"$WSA\"}" > "$EV/device3-switch-A-code.txt"
chk "$(cat "$EV/device3-switch-A-code.txt")" "200" "device 3 switches to workspace A"
chk "$(session_ws "$JAR1")" "$WSA" "device 1 is unaffected by the device 3 switch"
chk "$(session_ws "$JAR2")" "$WSB" "device 2 is unaffected by the device 3 switch"

echo "=== 8. NEW: session revoked (real logout) while a write is already in flight ==="
JAR4=$EV/jar-tab4.txt
rm -f "$JAR4"
curl -s -c "$JAR4" -o "$EV/tab4-login.json" -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF4=$(csrf_of "$JAR4")
chk "$(session_ws "$JAR4")" "$WSA" "tab4 session starts in workspace A"
A_BEFORE=$(case_count "$WSA"); B_BEFORE=$(case_count "$WSB")
make_body "$EV/race-c-body.json" alogout 160000
S=$(date +%s)
curl -s -b "$JAR4" -o "$EV/race-c-resp.json" -D "$EV/race-c-resp.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF4" --data-binary "@$EV/race-c-body.json" --limit-rate 40k \
  > "$EV/race-c-code.txt" 2> "$EV/race-c-err.txt" &
CURLPID=$!
sleep 1
curl -s -b "$JAR4" -c "$JAR4" -o "$EV/race-c-logout.json" -w '%{http_code}' -X POST "$API/api/auth/v1/logout" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF4" \
  -d '{}' > "$EV/race-c-logout-code.txt"
wait $CURLPID
ELAPSED=$(( $(date +%s) - S ))
RACE_C_CODE=$(cat "$EV/race-c-code.txt")
A_AFTER=$(case_count "$WSA"); B_AFTER=$(case_count "$WSB")
echo "race_c_elapsed_seconds=$ELAPSED logout_status=$(cat "$EV/race-c-logout-code.txt") write_status=$RACE_C_CODE"
echo "cases_named_RACE in A: $A_BEFORE->$A_AFTER ; in B: $B_BEFORE->$B_AFTER"
if [ "$ELAPSED" -lt 2 ]; then
  bad "RACE C: the throttled request finished in ${ELAPSED}s: the race window was not real"
else
  ok "RACE C: the write was genuinely in flight for ${ELAPSED}s while the session was logged out"
fi
chk "$RACE_C_CODE" "409" "RACE C: the in-flight write is refused with 409"
grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE' "$EV/race-c-resp.json"; chk "$?" "0" "RACE C: refusal carries AUTHZ_WORKSPACE_SCOPE_STALE"
chk "$A_AFTER" "$A_BEFORE" "RACE C: zero new objects in the revoked session workspace"
deny_audit "$(rid_from "$EV/race-c-resp.h")" "session_missing" "RACE C"

echo "=== 9. NEW: anonymous state-changing request stays 401 (the guard must not hijack it) ==="
A_BEFORE=$(case_count "$WSA")
code=$(curl -s -o "$EV/anon-post.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
  -H 'Content-Type: application/json' -d '{"case_no":"RACE-anon","name":"RACE-anon"}')
chk "$code" "401" "anonymous POST /api/v2/cases is 401, never 409"
if grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE' "$EV/anon-post.json"; then bad "anonymous request was refused with the session-scope code"; else ok "anonymous refusal does not use the session-scope code"; fi
chk "$(case_count "$WSA")" "$A_BEFORE" "the anonymous attempt created no case in A"

echo "=== 10. NEW: client disconnect in the middle of the body ==="
A_BEFORE=$(case_count "$WSA")
make_body "$EV/race-d-body.json" adisc 160000
# timeout -s KILL is used instead of signalling $! by hand: the first version of this gate
# signalled the background pid and curl simply finished the upload (the run produced a normal
# 201), so the "disconnect" case was never actually exercised. A SIGKILLed client is
# deterministic. The client must NOT observe a status code for this case.
timeout -s KILL 2 curl -s -b "$JAR1" -o "$EV/race-d-resp.json" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF1" --data-binary "@$EV/race-d-body.json" --limit-rate 40k \
  > "$EV/race-d-code.txt" 2> "$EV/race-d-err.txt"
RACE_D_CODE=$(cat "$EV/race-d-code.txt")
echo "race_d_client_status='$RACE_D_CODE' (empty means the client was killed before the server answered)"
if [ -z "$RACE_D_CODE" ]; then
  ok "the client really disconnected in the middle of the body (no status code was received)"
else
  bad "the client completed the request (status=$RACE_D_CODE): the disconnect case did not happen, the assertion below is not evidence"
fi
A_AFTER=$(case_count "$WSA")
echo "cases_named_RACE in A: $A_BEFORE->$A_AFTER after the client disconnected mid-body"
chk "$A_AFTER" "$A_BEFORE" "a client that disconnects mid-body creates no object"
curl -s -o /dev/null -w '%{http_code}' "$API/health/ready" > "$EV/ready-after-disconnect.txt"
chk "$(cat "$EV/ready-after-disconnect.txt")" "200" "the API is still ready after the aborted request"
chk "$(session_ws "$JAR1")" "$WSA" "the session of the disconnected client still works"

echo "=== 11. convergence census ==="
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
SELECT case when must_change_password then 'true' else 'false' end FROM identities WHERE email='admin@local';
SQL
)" "true" "the real account still carries must_change_password=true (never modified)"
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture memberships are restored to two active rows at the end"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_SESSION_SCOPE_CONVERGENCE_GATE_V4=PASS"; exit 0; fi
echo "C1_SESSION_SCOPE_CONVERGENCE_GATE_V4=FAIL"; exit 4
