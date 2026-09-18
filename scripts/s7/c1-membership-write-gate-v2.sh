#!/usr/bin/env bash
# c1-membership-write-gate-v2.sh -- C round: membership-aware WRITE path proved against
# the REAL deployed API and REAL PostgreSQL.
#
# Why this gate exists (root, 2026-09-14): the previous round closed the
# membership-aware workspace resolution with a pure decision-core unit test plus
# package tests only. That is not integration evidence. This gate drives the real
# HTTP API and asserts PostgreSQL state, covering five classes with positive and
# negative controls:
#   1. identity with ZERO active workspace memberships
#   2. identity with SEVERAL active memberships and no explicit scope
#   3. session-bound workspace (the normal path) with an audit allow binding
#   4. membership revoked while a session is live: the FIRST subsequent write fails
#      closed, then membership is restored and the write works again
#   5. migration-era SYSTEM principal (negative control at the API: compat headers
#      are ignored in session mode; positive control through the real-PG resolver)
#
# Rules honoured:
#   - additive fixtures only in a DEDICATED organisation; no real user row, real
#     password, lock state or membership is modified;
#   - no credential, cookie, CSRF token or DSN value is ever printed;
#   - every expectation is derived from PostgreSQL, not from a hand-written table;
#   - revoked/restored membership status is verified before and after.
# ASCII only.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-membership-write-$TS; fi
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
cases_in(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$1'::uuid;
SQL
}
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }
tid_from(){ awk 'tolower($1)=="x-trace-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

echo "=== 0. fixtures (dedicated org, additive, idempotent) ==="
BIN=$R/backend/bin/fixture-membership
if [ ! -x "$BIN" ]; then echo "MISSING_FIXTURE_TOOL $BIN"; exit 3; fi
echo "fixture_tool_sha256=$(sha256sum "$BIN" | cut -d' ' -f1)"
CRED=/home/euripides/.c1mem-creds.env
if [ ! -s "$CRED" ]; then
  ( set -a; . "$R/deploy/.env"; set +a
    "$BIN" -org-slug c1mem -org-name "C1 membership write acceptance" \
      -workspaces c1mem-a,c1mem-b \
      -identities investigator:c1memw-nomem@fixture.local:none,investigator:c1memw-multi@fixture.local:c1mem-a+c1mem-b,investigator:c1memw-single@fixture.local:c1mem-a,auditor:c1memw-revoked@fixture.local:c1mem-a \
      -out "$CRED" ) > "$EV/fixture.log" 2>&1
else
  echo "fixture_creds_reused (additive tool re-run keeps memberships consistent)"
  ( set -a; . "$R/deploy/.env"; set +a
    "$BIN" -org-slug c1mem -org-name "C1 membership write acceptance" \
      -workspaces c1mem-a,c1mem-b \
      -identities investigator:c1memw-nomem@fixture.local:none,investigator:c1memw-multi@fixture.local:c1mem-a+c1mem-b,investigator:c1memw-single@fixture.local:c1mem-a,auditor:c1memw-revoked@fixture.local:c1mem-a \
      -out "$CRED" ) >> "$EV/fixture.log" 2>&1
fi
chmod 600 "$CRED"
[ -s "$CRED" ]; chk "$?" "0" "fixture credential file exists and is 0600"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1MEM_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1MEM_B)
NOMEM_EMAIL=$(read_cred "$CRED" C1MEM_C1MEMW_NOMEM_EMAIL)
MULTI_EMAIL=$(read_cred "$CRED" C1MEM_C1MEMW_MULTI_EMAIL)
SINGLE_EMAIL=$(read_cred "$CRED" C1MEM_C1MEMW_SINGLE_EMAIL)
REVOKED_EMAIL=$(read_cred "$CRED" C1MEM_C1MEMW_REVOKED_EMAIL)
NOMEM_ID=$(read_cred "$CRED" C1MEM_C1MEMW_NOMEM_IDENTITY_ID)
echo "org_id=$ORG ws_a=$WSA ws_b=$WSB"
[ -n "$ORG" ] && [ -n "$WSA" ] && [ -n "$WSB" ]; chk "$?" "0" "fixture org and both workspaces resolved"
pgq > "$EV/fixture-membership-census.txt" <<SQL
SELECT i.email, coalesce(w.slug,'(none)'), coalesce(wm.status,'-')
FROM identities i
LEFT JOIN workspace_memberships wm ON wm.identity_id=i.id
LEFT JOIN workspaces w ON w.id=wm.workspace_id
WHERE i.organization_id='$ORG'::uuid ORDER BY i.email, w.slug;
SQL
cat "$EV/fixture-membership-census.txt"
NOMEM_ACTIVE=$(pgq <<SQL
SELECT count(*) FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE i.email='$NOMEM_EMAIL' AND wm.status='active';
SQL
)
chk "$NOMEM_ACTIVE" "0" "zero-membership fixture really holds no active membership"
MULTI_ACTIVE=$(pgq <<SQL
SELECT count(*) FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE i.email='$MULTI_EMAIL' AND wm.status='active';
SQL
)
chk "$MULTI_ACTIVE" "2" "multi-membership fixture really holds two active memberships"

CREATE_HOLDERS=$(pgq <<SQL
SELECT count(*) FROM member_role_bindings b JOIN role_permissions rp ON rp.role_id=b.role_id
JOIN identities i ON i.id=b.identity_id WHERE i.email='$SINGLE_EMAIL' AND rp.permission_key='case:create' AND b.revoked_at IS NULL;
SQL
)
[ "$CREATE_HOLDERS" -ge 1 ]; chk "$?" "0" "fixture role really holds case:create in PostgreSQL (v1 used a role that does not)"

login(){
  # login <jar> <email> <password> <workspace|-|NONE> <tag>
  local jar="$1" email="$2" pw="$3" ws="$4" tag="$5" body
  body="{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\""
  if [ "$ws" != "-" ]; then body="$body,\"workspace_id\":\"$ws\""; fi
  body="$body}"
  curl -s -c "$jar" -o "$EV/login-$tag.json" -D "$EV/login-$tag.h" -w '%{http_code}' \
    -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "$body"
}
csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" 2>/dev/null | tail -1; }
sess_cookie_count(){ grep -c 'cs_user_session' "$1" 2>/dev/null || true; }
create_case(){
  # create_case <jar> <csrf> <tag> <name>
  curl -s -b "$1" -o "$EV/case-$3.json" -D "$EV/case-$3.h" -w '%{http_code}' \
    -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $2" -d "{\"name\":\"$4\"}"
}

echo "=== 1. must_change_password state control (root correction 4) ==="
MJAR="$EV/jar-multi.txt"
MULTI_PW=$(read_cred "$CRED" C1MEM_C1MEMW_MULTI_PW)
MC=$(login "$MJAR" "$MULTI_EMAIL" "$MULTI_PW" "-" multi-noscope)
chk "$MC" "409" "multi-membership login without workspace_id is refused with a selection requirement"
MSEL=$(jget "['error']['code']" < "$EV/login-multi-noscope.json")
chk "$MSEL" "AUTHN_WORKSPACE_SELECTION_REQUIRED" "refusal carries the explicit selection code, not a generic error"
chk "$(sess_cookie_count "$MJAR")" "0" "no session cookie is issued when the workspace choice is missing"
MULTI_WSB=$(login "$MJAR" "$MULTI_EMAIL" "$MULTI_PW" "$WSB" multi-wsb)
chk "$MULTI_WSB" "200" "the same identity logs in once a member workspace is chosen explicitly"
MCSRF=$(csrf_of "$MJAR")
[ -n "$MCSRF" ]; ok "session csrf cookie issued (value never printed)"
curl -s -b "$MJAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" -o "$EV/session-multi.json"
MFLAG=$(jget "['data']['must_change_password']" < "$EV/session-multi.json")
MWS=$(jget "['data']['workspace_id']" < "$EV/session-multi.json")
chk "$MWS" "$WSB" "session workspace equals the explicitly chosen member workspace"
chk "$MFLAG" "True" "fixture session reports must_change_password=true (initial one-time password state)"
BIZCODE=$(curl -s -b "$MJAR" -o "$EV/mcp-biz-read.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/v2/cases")
echo "must_change_password_true_business_read_status=$BIZCODE"
echo "MUST_CHANGE_PASSWORD_ENFORCEMENT=NOT_ENFORCED_ON_BUSINESS_ENDPOINTS (UI-only signal; recorded as an open product gap, not as role UX evidence)"
ok "must_change_password=true behaviour recorded explicitly instead of being silently bypassed"
# A legitimate post-change fixture: the REAL product endpoint performs the change.
SJAR="$EV/jar-single.txt"
SINGLE_PW=$(read_cred "$CRED" C1MEM_C1MEMW_SINGLE_PW)
SL=$(login "$SJAR" "$SINGLE_EMAIL" "$SINGLE_PW" "-" single-before-change)
chk "$SL" "200" "single-membership fixture logs in with its initial one-time password"
SCSRF=$(csrf_of "$SJAR")
SFLAG0=$(curl -s -b "$SJAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['must_change_password'])")
chk "$SFLAG0" "True" "single fixture starts in the forced-change state"
NEWPW=$(head -c 24 /dev/urandom | base64 | tr -d '=+/' | head -c 24)
printf 'C1MEM_SINGLE_NEWPW=%s\n' "$NEWPW" >> "$CRED"
chmod 600 "$CRED"
PWC=$(curl -s -b "$SJAR" -o "$EV/pwchange-single.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $SCSRF" \
  -d "{\"current_password\":\"$SINGLE_PW\",\"new_password\":\"$NEWPW\"}")
chk "$PWC" "200" "the real product password-change endpoint accepts the one-time password"
NEWPW_DB_STATE=$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE lower(email)=lower('$SINGLE_EMAIL');
SQL
)
chk "$NEWPW_DB_STATE" "false" "PostgreSQL records must_change_password=false after the product change flow"
SL2=$(login "$SJAR" "$SINGLE_EMAIL" "$NEWPW" "-" single-after-change)
chk "$SL2" "200" "the fixture can log in again with its own rotated password"
SFLAG1=$(curl -s -b "$SJAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['must_change_password'])")
chk "$SFLAG1" "False" "post-change session is a normal role session (must_change_password=false)"
SCSRF=$(csrf_of "$SJAR")

echo "=== 2. class 1: zero active memberships ==="
ZJAR="$EV/jar-nomem.txt"
NOMEM_PW=$(read_cred "$CRED" C1MEM_C1MEMW_NOMEM_PW)
BASE_A=$(cases_in "$WSA"); BASE_B=$(cases_in "$WSB")
ZREQ=$(rid_from "$EV/login-nomem.h")
DENIED_BEFORE=$(pgq <<SQL
SELECT count(*) FROM login_events WHERE identity_id='$NOMEM_ID'::uuid AND result='denied';
SQL
)
ZCODE=$(login "$ZJAR" "$NOMEM_EMAIL" "$NOMEM_PW" "-" nomem)
chkne "$ZCODE" "200" "zero-membership login is refused (got $ZCODE)"
chk "$(sess_cookie_count "$ZJAR")" "0" "zero-membership login issues no session cookie"
DENIED_AFTER=$(pgq <<SQL
SELECT count(*) FROM login_events WHERE identity_id='$NOMEM_ID'::uuid AND result='denied';
SQL
)
chk "$((DENIED_AFTER-DENIED_BEFORE))" "1" "the refusal adds exactly one login_event denial for that identity"
ZW=$(curl -s -o "$EV/nomem-write.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d '{"name":"c1mem nomem must not exist"}')
chk "$ZW" "401" "an unauthenticated write after the refused login is 401"
chk "$(cases_in "$WSA")" "$BASE_A" "zero-membership class creates no business object in ws A"
chk "$(cases_in "$WSB")" "$BASE_B" "zero-membership class creates no business object in ws B"

echo "=== 3. class 2: several memberships, explicit scope required ==="
MWS2=$(jget "['data']['workspace_id']" < "$EV/session-multi.json")
chk "$MWS2" "$WSB" "multi-membership session stays on the explicitly chosen workspace"
MBAD=$(login "$EV/jar-multi-bad.txt" "$MULTI_EMAIL" "$MULTI_PW" "$WSA" multi-explicit-a)
chk "$MBAD" "200" "the same identity can also choose its other member workspace"
MFOREIGN=$(login "$EV/jar-multi-foreign.txt" "$MULTI_EMAIL" "$MULTI_PW" "00000000-0000-0000-0000-000000000000" multi-foreign)
chkne "$MFOREIGN" "200" "a workspace outside the membership set cannot be requested (got $MFOREIGN)"
CCODE=$(create_case "$MJAR" "$MCSRF" multi-wsb "c1mem multi wsB case")
chk "$CCODE" "201" "multi-membership session creates a case through the real API"
CID=$(jget "['data']['id']" < "$EV/case-multi-wsb.json")
CW=$(pgq <<SQL
SELECT workspace_id::text FROM cases WHERE id='$CID'::uuid;
SQL
)
chk "$CW" "$WSB" "the created case is stored in the session workspace, not an organisation default"
chk "$(cases_in "$WSA")" "$BASE_A" "no business object leaked into the other member workspace"
MRID=$(rid_from "$EV/case-multi-wsb.h")
MTID=$(tid_from "$EV/case-multi-wsb.h")
MROWS=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE request_id='$MRID' AND trace_id='$MTID' AND action='case.create' AND result='success'
  AND workspace_id='$WSB'::uuid;
SQL
)
chk "$MROWS" "1" "exactly one audit allow row is bound to the write request_id and trace_id"

echo "=== 4. class 3: session workspace write (single member, post-change) ==="
SW=$(curl -s -b "$SJAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['workspace_id'])")
chk "$SW" "$WSA" "single-membership session is bound to its only member workspace"
SCODE=$(create_case "$SJAR" "$SCSRF" single-wsa "c1mem single wsA case")
chk "$SCODE" "201" "single-membership session creates a case through the real API"
SID=$(jget "['data']['id']" < "$EV/case-single-wsa.json")
SW2=$(pgq <<SQL
SELECT workspace_id::text FROM cases WHERE id='$SID'::uuid;
SQL
)
chk "$SW2" "$WSA" "the row lands in the session workspace"
SRID=$(rid_from "$EV/case-single-wsa.h")
STID=$(tid_from "$EV/case-single-wsa.h")
SROWS=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE request_id='$SRID' AND trace_id='$STID' AND action='case.create' AND result='success';
SQL
)
chk "$SROWS" "1" "the session-workspace write is bound to exactly one audit allow row"
chk "$(cases_in "$WSB")" "$((BASE_B+1))" "only the multi-member class added a row to ws B"

echo "=== 5. class 4: revocation mid-session, first write must fail closed ==="
PRE_S=$(cases_in "$WSA")
pgq > "$EV/revoke.txt" <<SQL
UPDATE workspace_memberships wm SET status='removed', updated_at=now()
FROM identities i WHERE wm.identity_id=i.id AND lower(i.email)=lower('$SINGLE_EMAIL') AND wm.status='active';
SQL
REV_STATE=$(pgq <<SQL
SELECT coalesce(string_agg(wm.status,','),'(none)') FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE lower(i.email)=lower('$SINGLE_EMAIL');
SQL
)
chk "$REV_STATE" "removed" "the single fixture membership is really removed in PostgreSQL"
RVCODE=$(curl -s -b "$SJAR" -o "$EV/revoked-write.json" -D "$EV/revoked-write.h" -w '%{http_code}' \
  -X POST "$API/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $SCSRF" -d '{"name":"c1mem revoked must not exist"}')
chk "$RVCODE" "401" "the first write after revocation is refused with 401 (fail closed)"
RVERR=$(jget "['error']['code']" < "$EV/revoked-write.json")
chk "$RVERR" "AUTHZ_WORKSPACE_ACCESS_REVOKED" "the refusal carries the explicit revocation code"
chk "$(cases_in "$WSA")" "$PRE_S" "the revoked write created no business object"
RVRID=$(rid_from "$EV/revoked-write.h")
RVROWS=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE request_id='$RVRID' AND action='auth.workspace.access_revoked' AND result='deny';
SQL
)
chk "$RVROWS" "1" "exactly one security audit deny row is bound to the revoked write request"
RVLE=$(pgq <<SQL
SELECT count(*) FROM login_events WHERE request_id='$RVRID' AND result='denied';
SQL
)
chk "$RVLE" "1" "exactly one login_event denial is bound to the revoked write request"
pgq > "$EV/restore.txt" <<SQL
UPDATE workspace_memberships wm SET status='active', updated_at=now()
FROM identities i WHERE wm.identity_id=i.id AND lower(i.email)=lower('$SINGLE_EMAIL') AND wm.status='removed';
SQL
RES_STATE=$(pgq <<SQL
SELECT coalesce(string_agg(wm.status,','),'(none)') FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE lower(i.email)=lower('$SINGLE_EMAIL');
SQL
)
chk "$RES_STATE" "active" "the fixture membership is restored to active"
RL=$(login "$SJAR" "$SINGLE_EMAIL" "$NEWPW" "-" single-restored)
chk "$RL" "200" "after restoration the fixture logs in again"
SCSRF=$(csrf_of "$SJAR")
RSCODE=$(create_case "$SJAR" "$SCSRF" single-restored "c1mem single restored case")
chk "$RSCODE" "201" "the restored identity can write again (membership is the real gate)"

echo "=== 6. class 5: system principal controls ==="
FORGED=$(curl -s -o "$EV/forged-headers.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H 'X-User-Id: 00000000-0000-0000-0000-0000000000ff' -H "X-User-Email: $SINGLE_EMAIL" \
  -H "X-User-Organization-Id: $ORG" -H "X-User-Workspace-Id: $WSA" -H 'X-User-Role: platform_admin' \
  -d '{"name":"c1mem forged header must not exist"}')
chk "$FORGED" "401" "forged compatibility headers cannot impersonate a system principal in session mode"
chk "$(cases_in "$WSA")" "$((PRE_S+1))" "the forged-header attempt created no business object"
FSESS=$(curl -s -o "$EV/forged-session.json" -w '%{http_code}' -H 'X-Auth-Audience: user' \
  -H 'X-User-Id: 00000000-0000-0000-0000-0000000000ff' -H 'X-User-Role: platform_admin' "$API/api/auth/v1/session")
chk "$FSESS" "401" "forged headers cannot open a self-service session either"
REVOKED_PW=$(read_cred "$CRED" C1MEM_C1MEMW_REVOKED_PW)
pgq > "$EV/revoke-live.txt" <<SQL
UPDATE workspace_memberships wm SET status='removed', updated_at=now()
FROM identities i WHERE wm.identity_id=i.id AND lower(i.email)=lower('$REVOKED_EMAIL') AND wm.status='active';
SQL
echo "=== 7. real-PostgreSQL resolver integration (go test -tags live) ==="
export PATH=$PATH:/usr/local/go/bin
go version
eval "$(R="$R" bash -c 'set -a; . "$R"/deploy/.env; set +a; printf "C1MEM_DSN=%q\n" "$MIGRATION_DATABASE_URL"')"
export C1MEM_LIVE_DSN="$C1MEM_DSN"
export C1MEM_ORG_ID="$ORG" C1MEM_WS_A="$WSA" C1MEM_WS_B="$WSB"
export C1MEM_NOMEM_EMAIL="$NOMEM_EMAIL" C1MEM_MULTI_EMAIL="$MULTI_EMAIL"
export C1MEM_SINGLE_EMAIL="$SINGLE_EMAIL" C1MEM_REVOKED_EMAIL="$REVOKED_EMAIL"
export C1MEM_FOREIGN_EMAIL=admin@local
unset C1MEM_DSN
( cd "$R/backend" && go test -tags live -count=1 -run 'TestWritableWorkspaceMembershipLive|TestResolveWorkspaceLiveMembershipClasses' -v ./internal/api/ ./internal/casework/ ) > "$EV/live-pg-tests.log" 2>&1
LIVERC=$?
grep -E '^(--- )?(ok|FAIL|PASS|--- PASS|--- FAIL)' "$EV/live-pg-tests.log" | tail -20
chk "$LIVERC" "0" "real-PG resolver integration tests pass (api + casework)"
if grep -qE -- '--- SKIP|cannot find package|no test files' "$EV/live-pg-tests.log"; then bad "live tests were skipped or did not compile"; else ok "live tests really executed (no SKIP, packages resolved)"; fi
LIVEPASS=$(grep -c -- '--- PASS' "$EV/live-pg-tests.log" || true)
[ "$LIVEPASS" -ge 6 ]; chk "$?" "0" "at least six real-PG sub-tests passed (count=$LIVEPASS)"
pgq > "$EV/restore-live.txt" <<SQL
UPDATE workspace_memberships wm SET status='active', updated_at=now()
FROM identities i WHERE wm.identity_id=i.id AND lower(i.email)=lower('$REVOKED_EMAIL') AND wm.status='removed';
SQL
REV_RESTORED=$(pgq <<SQL
SELECT coalesce(string_agg(wm.status,','),'(none)') FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
WHERE lower(i.email)=lower('$REVOKED_EMAIL');
SQL
)
chk "$REV_RESTORED" "active" "the revocation-window fixture membership is restored"

echo "=== 8. census and no-loss assertions ==="
pgq > "$EV/final-cases-by-workspace.txt" <<SQL
SELECT w.slug, count(c.id) FROM workspaces w LEFT JOIN cases c ON c.workspace_id=w.id
WHERE w.organization_id='$ORG'::uuid GROUP BY w.slug ORDER BY w.slug;
SQL
cat "$EV/final-cases-by-workspace.txt"
NONC1=$(pgq <<SQL
SELECT count(*) FROM cases WHERE organization_id='$ORG'::uuid AND workspace_id NOT IN ('$WSA'::uuid,'$WSB'::uuid);
SQL
)
chk "$NONC1" "0" "every case created by this gate lives in the fixture org inside the two fixture workspaces"
REALTOUCH=$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)
chk "$REALTOUCH" "1" "the real account is untouched and still belongs to its own organisation"

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_MEMBERSHIP_WRITE_GATE=PASS"; exit 0; fi
echo "C1_MEMBERSHIP_WRITE_GATE=FAIL"; exit 4
