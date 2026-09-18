#!/usr/bin/env bash
# c1-role-switch-matrix.sh -- C-round role x permission x workspace-switch matrix.
#
# Requirement (root, 2026-09-13): a controlled fixture matrix for
# viewer / analyst / reviewer / auditor, with permissions derived from the database
# (never hard-coded here), a real permission x switch audit assertion per role, and a
# revoke-then-restore proof that the workspace membership is what actually gates access.
#
# Rules honoured:
#   - additive fixtures only; no real user row is touched; no password is printed;
#   - every expectation is computed from PG (role_permissions / workspace_memberships),
#     so the gate cannot drift into asserting a stale hand-written table;
#   - revoke/restore uses a dedicated fixture and restores the exact previous status.
# ASCII only.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-role-switch-matrix-$TS; fi
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

echo "=== 0. fixtures (additive, idempotent) ==="
BIN=$R/backend/bin/fixture-identity
echo "fixture_tool_sha256=$(sha256sum "$BIN" | cut -d' ' -f1)"
mk(){
  local role="$1" email="$2" ws="$3" out="$4" tag="$5"
  if [ -s "$out" ]; then echo "fixture_exists $tag"; return 0; fi
  # The tool needs the deployment DSN. Attempt 1 failed with
  # 'role "euripides" does not exist' because DATABASE_URL was not exported here;
  # the fixture script sources deploy/.env in a subshell for exactly this reason.
  ( set -a; . "$R/deploy/.env"; set +a
    "$BIN" -org-slug s7jobscope -org-name "S7 JobScope 回归夹具" -workspaces "$ws" \
      -identities "$role:$email:$ws" -out "$out" ) > "$EV/fixture-$tag.log" 2>&1
  if [ ! -s "$out" ]; then
    echo "FIXTURE_CREATE_FAILED $tag (see $EV/fixture-$tag.log)"; tail -3 "$EV/fixture-$tag.log"; return 1
  fi
  chmod 600 "$out"; echo "fixture_created $tag role=$role ws=$ws"
}
mk viewer       c1role-viewer@fixture.local   s7js-a /home/euripides/.c1role-viewer.env   c1role-viewer
mk analyst      c1role-analyst@fixture.local  s7js-a /home/euripides/.c1role-analyst.env  c1role-analyst
mk reviewer     c1role-reviewer@fixture.local s7js-a /home/euripides/.c1role-reviewer.env c1role-reviewer
mk auditor      c1role-auditor@fixture.local  s7js-a /home/euripides/.c1role-auditor.env  c1role-auditor

read_cred(){ grep -E "$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }

NONMEMBER=$(pgq <<'SQL'
SELECT w.id::text FROM workspaces w WHERE w.slug='s7js-b' LIMIT 1;
SQL
)
echo "non_member_workspace=$NONMEMBER"

role_matrix(){
  local role="$1" creds="$2"
  echo "=== role=$role ==="
  local email pw jar csrf
  email=$(read_cred "$creds" "_EMAIL"); pw=$(read_cred "$creds" "_PW")
  if [ -z "$email" ] || [ -z "$pw" ]; then bad "$role fixture credentials unreadable"; return; fi
  echo "email_masked=$(echo "$email" | sed 's/^\(.\).*@/\1***@/')"
  jar="$EV/jar-$role.txt"
  local code
  code=$(curl -s -c "$jar" -o "$EV/$role-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\"}")
  chk "$code" "200" "$role single-membership login is 200"
  csrf=$(awk '$6=="cs_user_session_csrf"{print $7}' "$jar" | tail -1)
  if [ -n "$csrf" ]; then ok "$role csrf cookie present (value not printed)"; else bad "$role csrf cookie missing"; fi

  curl -s -b "$jar" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" -o "$EV/$role-session.json"
  python3 - "$EV/$role-session.json" "$EV/$role-session-perms.txt" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
perms = sorted((d.get("data") or {}).get("permissions") or [])
open(sys.argv[2], "w", encoding="utf-8").write("\n".join(perms) + ("\n" if perms else ""))
print("session_permission_count=%d" % len(perms))
PY
  pgq > "$EV/$role-db-perms.txt" <<SQL
SELECT rp.permission_key FROM role_permissions rp JOIN roles r ON r.id=rp.role_id
 WHERE r.key='$role' ORDER BY rp.permission_key;
SQL
  if diff -q <(grep -v '^$' "$EV/$role-session-perms.txt" | sort) <(grep -v '^$' "$EV/$role-db-perms.txt" | sort) >/dev/null; then
    ok "$role session permission set equals the database role_permissions set ($(grep -c . "$EV/$role-db-perms.txt") keys)"
  else
    bad "$role session permission set differs from database role_permissions (see $EV/$role-*-perms.txt)"
  fi

  local has_create has_list
  has_create=$(grep -c '^case:create$' "$EV/$role-db-perms.txt" || true)
  has_list=$(grep -c '^case:list$' "$EV/$role-db-perms.txt" || true)

  code=$(curl -s -b "$jar" -o "$EV/$role-list-cases.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/v2/cases")
  if [ "$has_list" -ge 1 ]; then chk "$code" "200" "$role holds case:list and can list cases"; else chk "$code" "403" "$role lacks case:list and is refused"; fi

  code=$(curl -s -b "$jar" -o "$EV/$role-create-case.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -d '{}')
  if [ "$has_create" -ge 1 ]; then
    chkne "$code" "403" "$role holds case:create: the request passes authorization (got $code, not 403)"
  else
    chk "$code" "403" "$role lacks case:create and the API itself refuses (button visibility is not the gate)"
  fi

  if [ -n "$NONMEMBER" ]; then
    code=$(curl -s -b "$jar" -o "$EV/$role-switch-nonmember.json" -w '%{http_code}' -X POST "$API/api/auth/v1/session/workspace" \
      -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -d "{\"workspace_id\":\"$NONMEMBER\"}")
    chk "$code" "403" "$role switch to a same-org non-member workspace is 403"
    local rid
    rid=$(jget "['meta']['request_id']" < "$EV/$role-switch-nonmember.json")
    local deny
    deny=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
 WHERE request_id='$rid' AND action='authorization.workspace_switched' AND result='deny'
   AND workspace_id='$NONMEMBER'::uuid;
SQL
)
    chk "$deny" "1" "$role non-member switch is recorded as exactly one audit deny bound to its request_id"
    local le
    le=$(pgq <<SQL
SELECT count(*) FROM login_events
 WHERE request_id='$rid' AND event_type='auth.workspace.switch' AND result='failure';
SQL
)
    chk "$le" "1" "$role non-member switch writes exactly one login_event failure"
  fi

  curl -s -b "$jar" -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" -o "$EV/$role-logout.json"
}

role_matrix viewer  /home/euripides/.c1role-viewer.env
role_matrix analyst /home/euripides/.c1role-analyst.env
role_matrix reviewer /home/euripides/.c1role-reviewer.env
role_matrix auditor /home/euripides/.c1role-auditor.env

echo "=== revoke / restore on the analyst fixture ==="
AJAR=$EV/jar-analyst.txt
AREV=/home/euripides/.c1role-analyst.env
aemail=$(read_cred "$AREV" "_EMAIL"); apw=$(read_cred "$AREV" "_PW")
curl -s -c "$AJAR" -o "$EV/revoke-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$aemail\",\"password\":\"$apw\",\"audience\":\"user\"}" > "$EV/revoke-login-code.txt"
chk "$(cat "$EV/revoke-login-code.txt")" "200" "revoke-test login before revocation is 200"
PGQ_STATUS_BEFORE=$(pgq <<SQL
SELECT wm.status FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
 WHERE i.email='$aemail';
SQL
)
chk "$PGQ_STATUS_BEFORE" "active" "revoke-test fixture membership starts active"
pgq > "$EV/revoke-membership.txt" <<SQL
UPDATE workspace_memberships wm SET status='removed', updated_at=now()
 FROM identities i WHERE wm.identity_id=i.id AND i.email='$aemail' AND wm.status='active';
SQL
echo "revoked_rows=$(grep -c UPDATE "$EV/revoke-membership.txt" || true)"
FAILCLOSED=0
for i in $(seq 1 10); do
  C=$(curl -s -b "$AJAR" -o "$EV/revoke-session-$i.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/auth/v1/session")
  if [ "$C" = "401" ] || [ "$C" = "403" ]; then FAILCLOSED=1; echo "fail_closed_after_attempts=$i status=$C"; break; fi
  sleep 1
done
chk "$FAILCLOSED" "1" "revoked workspace membership makes the live session fail closed within the bounded window"
C2=$(curl -s -b "$AJAR" -o "$EV/revoke-cases.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/v2/cases")
if [ "$C2" = "401" ] || [ "$C2" = "403" ]; then ok "business endpoint also fails closed while membership is removed (status=$C2)"; else bad "business endpoint still served while membership is removed (status=$C2)"; fi
pgq > "$EV/restore-membership.txt" <<SQL
UPDATE workspace_memberships wm SET status='active', updated_at=now()
 FROM identities i WHERE wm.identity_id=i.id AND i.email='$aemail' AND wm.status='removed';
SQL
PGQ_STATUS_AFTER=$(pgq <<SQL
SELECT wm.status FROM workspace_memberships wm JOIN identities i ON i.id=wm.identity_id
 WHERE i.email='$aemail';
SQL
)
chk "$PGQ_STATUS_AFTER" "active" "revoke-test fixture membership restored to active"
CODE=$(curl -s -c "$AJAR" -o "$EV/restore-login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$aemail\",\"password\":\"$apw\",\"audience\":\"user\"}")
chk "$CODE" "200" "after restore the fixture can log in again (access restored, not merely re-enabled by hand)"

echo "=== fixture census (no deletions) ==="
pgq <<'SQL' | tee "$EV/fixture-census.txt"
SELECT i.email, i.status,
       coalesce(string_agg(w.slug || ':' || wm.status, ',' ORDER BY w.slug), '(none)') AS memberships
FROM identities i LEFT JOIN workspace_memberships wm ON wm.identity_id=i.id
LEFT JOIN workspaces w ON w.id=wm.workspace_id
WHERE i.email LIKE 'c1role-%@fixture.local' OR i.email LIKE 'p1gate-%@fixture.local'
GROUP BY i.email, i.status ORDER BY i.email;
SQL

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_ROLE_SWITCH_MATRIX=PASS"; exit 0; fi
echo "C1_ROLE_SWITCH_MATRIX=FAIL"; exit 4
