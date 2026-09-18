#!/usr/bin/env bash
# c1-role-switch-success-gate-v2.sh -- C round: the FOUR role families must be able to
# SUCCESSFULLY switch between two workspaces they are members of, and must be refused
# for a same-organisation workspace they are NOT a member of.
#
# Why this exists (root correction 5, 2026-09-14): the previous round only covered
# "4 roles x non-member refusal". A matrix that never shows the positive path cannot
# distinguish "the gate works" from "everything is refused".
#
# Every expectation is derived from PostgreSQL (workspace_memberships), never from a
# hand-written table. Fixtures are additive, inside the dedicated c1mem organisation;
# no real account, password, lock state or membership is touched.
# Values are never printed: no cookie, CSRF, password or DSN appears in the output.
# ASCII only.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-role-switch-success-$TS; fi
if [ -z "$API" ]; then API=http://127.0.0.1:8080; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
chkne(){ if [ "$1" != "$2" ]; then ok "$3"; else bad "$3 (got='$1' must differ from '$2')"; fi; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "api=$API evidence=$EV"
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }
tid_from(){ awk 'tolower($1)=="x-trace-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

echo "=== 0. switch fixtures (two member workspaces + one same-org non-member) ==="
BIN=$R/backend/bin/fixture-membership
[ -x "$BIN" ]; chk "$?" "0" "fixture tool is installed"
echo "fixture_tool_sha256=$(sha256sum "$BIN" | cut -d' ' -f1)"
CRED=/home/euripides/.c1sw-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$BIN" -org-slug c1mem -org-name "C1 membership write acceptance" \
    -workspaces c1mem-a,c1mem-b,c1mem-c \
    -identities viewer:c1sw-viewer@fixture.local:c1mem-a+c1mem-b,analyst:c1sw-analyst@fixture.local:c1mem-a+c1mem-b,reviewer:c1sw-reviewer@fixture.local:c1mem-a+c1mem-b,auditor:c1sw-auditor@fixture.local:c1mem-a+c1mem-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
[ -s "$CRED" ]; chk "$?" "0" "switch fixture credentials written 0600"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1MEM_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1MEM_B)
WSC=$(read_cred "$CRED" C1MEM_WS_C1MEM_C)
FOREIGN=$(pgq <<SQL
SELECT w.id::text FROM workspaces w JOIN organizations o ON o.id=w.organization_id
WHERE o.slug='s7jobscope' AND w.slug='s7js-a' LIMIT 1;
SQL
)
[ -n "$WSC" ] && [ -n "$FOREIGN" ]; chk "$?" "0" "same-org non-member workspace and a foreign-org workspace both resolved"
echo "org=$ORG ws_a=$WSA ws_b=$WSB ws_c=$WSC foreign=$FOREIGN"

switch_to(){
  # switch_to <jar> <csrf> <wsid> <tag>
  curl -s -b "$1" -o "$EV/switch-$4.json" -D "$EV/switch-$4.h" -w '%{http_code}' \
    -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $2" -d "{\"workspace_id\":\"$3\"}"
}
session_ws(){
  curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | \
    python3 -c "import sys,json;print(json.load(sys.stdin)['data']['workspace_id'])"
}

role_case(){
  local role="$1" tag="$2"
  local email pw jar csrf code members
  # v1 bug: tr only upper-cased, so "c1sw-viewer" became C1SW-VIEWER and the
  # key lookup failed. The tag is also dash-to-underscore normalised here.
  local tagkey
  tagkey=$(printf '%s' "$tag" | tr 'a-z' 'A-Z' | tr '-' '_')
  email=$(read_cred "$CRED" "C1MEM_${tagkey}_EMAIL")
  pw=$(read_cred "$CRED" "C1MEM_${tagkey}_PW")
  jar="$EV/jar-$tag.txt"; csrf=""
  echo "=== role=$role tag=$tag ==="
  if [ -z "$email" ] || [ -z "$pw" ]; then bad "$role fixture credentials unreadable"; return; fi
  members=$(pgq <<SQL
SELECT count(*) FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE i.email='$email' AND wm.status='active';
SQL
)
  chk "$members" "2" "$role fixture really holds exactly two active memberships"

  code=$(curl -s -c "$jar" -o "$EV/login-$tag-noscope.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\"}")
  chk "$code" "409" "$role login without an explicit workspace is refused (needs a choice)"
  chk "$(jget "['error']['code']" < "$EV/login-$tag-noscope.json")" "AUTHN_WORKSPACE_SELECTION_REQUIRED" "$role refusal code is the explicit selection code"

  code=$(curl -s -c "$jar" -o "$EV/login-$tag-a.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
  chk "$code" "200" "$role logs in choosing its first member workspace"
  csrf=$(awk '$6=="cs_user_session_csrf"{print $7}' "$jar" | tail -1)
  [ -n "$csrf" ]; chk "$?" "0" "$role csrf cookie present (value never printed)"
  chk "$(session_ws "$jar")" "$WSA" "$role session starts in the chosen workspace"

  code=$(switch_to "$jar" "$csrf" "$WSB" "$tag-b")
  chk "$code" "200" "$role switches to its SECOND member workspace successfully"
  chk "$(session_ws "$jar")" "$WSB" "$role session really moved to the second workspace"
  local rid tid
  rid=$(rid_from "$EV/switch-$tag-b.h"); tid=$(tid_from "$EV/switch-$tag-b.h")
  chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$rid' AND action='authorization.workspace_switched' AND result='allow';
SQL
)" "1" "$role successful switch writes exactly one audit allow bound to its request"
  chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$rid' AND trace_id='$tid' AND result='allow';
SQL
)" "1" "$role audit allow row also carries the same trace_id"
  chk "$(pgq <<SQL
SELECT count(*) FROM login_events WHERE request_id='$rid' AND event_type='auth.workspace.switch' AND result='success';
SQL
)" "1" "$role successful switch writes exactly one login_event success"

  code=$(switch_to "$jar" "$csrf" "$WSC" "$tag-c")
  chk "$code" "403" "$role switch to a SAME-ORG non-member workspace is refused"
  rid=$(rid_from "$EV/switch-$tag-c.h")
  chk "$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$rid' AND action='authorization.workspace_switched' AND result='deny';
SQL
)" "1" "$role refusal writes exactly one audit deny bound to its request"
  chk "$(pgq <<SQL
SELECT count(*) FROM login_events WHERE request_id='$rid' AND event_type='auth.workspace.switch' AND result='failure';
SQL
)" "1" "$role refusal writes exactly one login_event failure"
  chk "$(session_ws "$jar")" "$WSB" "$role session is UNCHANGED by the refused switch"

  code=$(switch_to "$jar" "$csrf" "$FOREIGN" "$tag-foreign")
  chk "$code" "403" "$role switch to a FOREIGN-org workspace is refused"

  code=$(curl -s -b "$jar" -o "$EV/$tag-admin.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/admin/v1/system/health")
  if [ "$code" = "401" ] || [ "$code" = "403" ] || [ "$code" = "404" ]; then
    ok "$role user-audience session cannot use the admin surface (status=$code)"
  else
    bad "$role user-audience session reached the admin surface (status=$code)"
  fi

  curl -s -b "$jar" -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -o "$EV/logout-$tag.json"
  chk "$(session_ws "$jar")" "__ERR__" "$role logout invalidates the session"
}

role_case viewer   c1sw-viewer
role_case analyst  c1sw-analyst
role_case reviewer c1sw-reviewer
role_case auditor  c1sw-auditor

echo "=== census ==="
pgq > "$EV/census.txt" <<SQL
SELECT i.email, i.status, coalesce(string_agg(w.slug || ':' || wm.status, ',' ORDER BY w.slug), '(none)')
FROM identities i LEFT JOIN workspace_memberships wm ON wm.identity_id=i.id
LEFT JOIN workspaces w ON w.id=wm.workspace_id
WHERE i.organization_id='$ORG'::uuid GROUP BY i.email, i.status ORDER BY i.email;
SQL
cat "$EV/census.txt"
REAL=$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)
chk "$REAL" "1" "the real account is untouched"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_ROLE_SWITCH_SUCCESS_GATE=PASS"; exit 0; fi
echo "C1_ROLE_SWITCH_SUCCESS_GATE=FAIL"; exit 4
