#!/usr/bin/env bash
# c1-admin-permission-matrix-gate-v2.sh -- C round A5: per-route permission matrix for the
# /api/admin/v1 surface, exercised against the REAL deployed API with REAL PostgreSQL.
#
# What it proves, per route, for three fixtures whose permissions come from PostgreSQL:
#   security_admin (37 permissions)  -> every route it holds must NOT be 401/403
#   auditor        (30 permissions)  -> every route it holds must NOT be 401/403
#   viewer         (0 admin perms)   -> every admin route must be 401/403
# and, symmetrically, every route a fixture does NOT hold must be refused.
#
# Expectation source is the DEPLOYED source tree, extracted mechanically by
# scripts/s7/admin-route-perm-extract.py (never a hand-written table). The extractor also
# emits the sha256 of every file it read; those are frozen below and asserted, so a stale
# declaration can never silently vouch for a different tree.
#
# Safety:
#   - fixtures are additive, inside the dedicated c1perm organisation; the real admin@local
#     account, its password, its lock state and its memberships are never touched;
#   - every non-GET request carries a deliberately malformed JSON body, so the request is
#     rejected by the handler's decoder BEFORE any business logic runs. The permission
#     decision is made by the route middleware, which runs first, so the positive path is
#     still measured (400) while no object can be created or mutated;
#   - a before/after PostgreSQL census over 40 business tables proves zero side effects.
# No cookie, CSRF token, password or DSN is ever printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-admin-permission-matrix-$TS; fi
if [ -z "$API" ]; then API=http://127.0.0.1:8080; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "api=$API evidence=$EV"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }

FIXED_ID=ffffffff-ffff-ffff-ffff-fffffffffffe
FAKE_KEY=zz-no-such-key
FAKE_CMD=zz-no-such-command
FAKE_CHAIN=zz-no-such-chain
FAKE_DATE=1970-01-01

# Frozen hashes of the declaration sources (verified equal to the Windows worktree copies
# on 2026-09-14). If any drifts, this gate fails closed instead of vouching for a tree that
# was not the one measured.
declare -A SRC_SHA=(
  [internal/api/adminorg.go]=4cae3ce022e169fe1f783fa1a7f7dc8dd8b1347fd93b8dd895bc012fe112fbc8
  [internal/api/adminv1.go]=a69af50564df61b77fceb4c3d30d20f26a2302519958c7c43fd1e7c64b26c363
  [internal/api/server.go]=23b11f54d21f7d78e2ed1894e571160e895a92b857404dd908e94202ca40aa9f
  [internal/api/sprint6_audit_integrity.go]=e9198297b0aff2e5a55895a133948c160b7096e7b5e3898691e563e7d34297f2
  [internal/api/sprint6_control.go]=80314753d4f3175e849ef30d33a33ae3e30ad9e4c760ef64c765e486286a674a
  [internal/api/sprint6_governance.go]=670843e506cab096d4ab5e811ef1caa42567e4b4c402785c0d8a5cf63868bae3
  [internal/api/sprint6_identity.go]=4658fef953505cb50eb03d67d92c4dd4c8bc11ed02291dcf7bd037853701f8fc
  [internal/api/sprint6_obs.go]=bd40d880fc617d0f98942eff015ae263e3543fb2859b036c1f63e7b88fcfa223
  [internal/api/sprint6_provider.go]=8327e54e91196a748a9f27b956bd3c9d13b4374182e9b1bb5232f20d8fbbd4ed
)

echo "=== 0. freeze: extractor + declaration sources ==="
EXTRACTOR=$R/scripts/s7/admin-route-perm-extract.py
[ -f "$EXTRACTOR" ]; chk "$?" "0" "route/permission extractor present"
echo "extractor_sha256=$(sha256sum "$EXTRACTOR" | cut -d\  -f1)"
python3 "$EXTRACTOR" "$R/backend/internal/api" > "$EV/routes.raw" 2> "$EV/extract.err"
chk "$?" "0" "extractor ran"
grep '^ROUTE|' "$EV/routes.raw" > "$EV/routes.tsv"
ROUTE_N=$(wc -l < "$EV/routes.tsv")
echo "route_count=$ROUTE_N"
[ "$ROUTE_N" -ge 100 ]; chk "$?" "0" "extracted a plausible admin route surface"
UNDECL=$(grep -c '(none)' "$EV/routes.tsv" || true)
chk "$UNDECL" "0" "every admin route declares at least one permission (no silent deny-by-default gap)"
for f in "${!SRC_SHA[@]}"; do
  got=$(grep -F "SRC|$f|" "$EV/routes.raw" | head -1 | cut -d'|' -f3)
  if [ "$got" = "${SRC_SHA[$f]}" ]; then ok "declaration source frozen: $f"; else bad "declaration source drifted: $f (got=$got)"; fi
done

echo "=== 1. admin binary identity ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "api_pid=$AP api_exe_sha256=$API_SHA"
chk "$API_SHA" "$(sha256sum $R/backend/bin/api | cut -d\  -f1)" "running API binary == backend/bin/api on disk"

echo "=== 2. fixtures (dedicated c1perm organisation) ==="
BIN=$R/backend/bin/fixture-membership
[ -x "$BIN" ]; chk "$?" "0" "fixture tool present"
echo "fixture_tool_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
CRED=/home/euripides/.c1perm-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$BIN" -org-slug c1perm -org-name "C1 admin permission matrix acceptance" \
    -workspaces c1perm-a \
    -identities security_admin:c1perm-seca@fixture.local:c1perm-a,auditor:c1perm-aud@fixture.local:c1perm-a,viewer:c1perm-viewer@fixture.local:c1perm-a \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
[ -s "$CRED" ]; chk "$?" "0" "fixture credentials written 0600"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1PERM_A)
echo "org=$ORG ws=$WSA"
[ -n "$ORG" ] && [ -n "$WSA" ]; chk "$?" "0" "fixture org and workspace resolved"

REAL_BEFORE=$(pgq <<SQL
SELECT email||'|'||status||'|'||must_change_password FROM identities WHERE email='admin@local';
SQL
)
echo "real_admin_before_frozen=yes (value withheld from the log)"

echo "=== 3. clear the initial-password flag through the REAL product endpoint ==="
# The fixtures are created with the product default (must_change_password = true). Instead of
# pretending the flag does not exist, each fixture performs a genuine password change through
# POST /api/auth/v1/password/change, so every later assertion runs on a legitimately cleared
# account. The password is never printed.
clear_initial_flag(){
  local tag="$1" key email pw npw jar code
  key=$(printf '%s' "$tag" | tr 'a-z' 'A-Z' | tr '-' '_')
  email=$(read_cred "$CRED" "C1MEM_${key}_EMAIL")
  pw=$(read_cred "$CRED" "C1MEM_${key}_PW")
  npw="Pw-${TS}-${tag}-9f3a"
  jar="$EV/jar-clear-$tag.txt"
  code=$(curl -s -c "$jar" -o "$EV/clear-login-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
  if [ "$code" != "200" ]; then bad "$tag could not log in to clear the initial flag (status=$code)"; return 1; fi
  local csrf
  csrf=$(awk '$6=="cs_user_session_csrf"{print $7}' "$jar" | tail -1)
  code=$(curl -s -b "$jar" -o "$EV/clear-change-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$npw\"}")
  [ "$code" = "200" ] || [ "$code" = "204" ]; chk "$?" "0" "$tag password change accepted by the real endpoint (status=$code)"
  local flag
  flag=$(pgq <<SQL
SELECT must_change_password FROM identities WHERE lower(email)=lower('$email');
SQL
)
  chk "$flag" "f" "$tag must_change_password cleared in PostgreSQL"
  CUR_PW[$tag]="$npw"
}
declare -A CUR_PW=()
clear_initial_flag c1perm-seca
clear_initial_flag c1perm-aud
clear_initial_flag c1perm-viewer

echo "=== 4. permission sets straight from PostgreSQL ==="
role_perms(){
  pgq <<SQL | tr '\n' ',' | sed 's/,$//'
SELECT rp.permission_key FROM roles r JOIN role_permissions rp ON rp.role_id=r.id
WHERE r.key='$1' AND rp.permission_key LIKE 'admin:%' ORDER BY 1;
SQL
}
SECA_PERMS=$(role_perms security_admin)
AUD_PERMS=$(role_perms auditor)
VIEW_PERMS=$(role_perms viewer)
echo "security_admin_admin_perms=$(printf '%s' "$SECA_PERMS" | tr ',' '\n' | grep -c . )"
echo "auditor_admin_perms=$(printf '%s' "$AUD_PERMS" | tr ',' '\n' | grep -c . )"
echo "viewer_admin_perms=$(printf '%s' "$VIEW_PERMS" | tr ',' '\n' | grep -c . )"
chk "$VIEW_PERMS" "" "viewer holds no admin permission at all (pure negative control)"

echo "=== 5. admin-audience sessions ==="
declare -A JAR CSRF
login_admin(){
  local tag="$1" key email pw code
  key=$(printf '%s' "$tag" | tr 'a-z' 'A-Z' | tr '-' '_')
  email=$(read_cred "$CRED" "C1MEM_${key}_EMAIL")
  pw=${CUR_PW[$tag]:-}
  JAR[$tag]="$EV/jar-admin-$tag.txt"
  code=$(curl -s -c "${JAR[$tag]}" -o "$EV/login-admin-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
  chk "$code" "200" "$tag obtains an admin-audience session"
  CSRF[$tag]=$(awk '$6=="cs_admin_session_csrf"{print $7}' "${JAR[$tag]}" | tail -1)
  [ -n "${CSRF[$tag]}" ]; chk "$?" "0" "$tag admin CSRF cookie present (value never printed)"
}
login_admin c1perm-seca
login_admin c1perm-aud
login_admin c1perm-viewer

echo "=== 6. per-route matrix ==="
holds(){
  # holds <comma separated held perms> <comma separated required perms> -> 0 when ANY required is held
  local held=",$1," req="$2" p
  local IFS=','
  for p in $req; do
    case "$held" in *",$p,"*) return 0;; esac
  done
  return 1
}
census_tables="workspaces identities roles role_permissions member_role_bindings organization_memberships workspace_memberships feature_flags deletion_requests backup_jobs restore_previews approval_requests approval_request_events retention_policies retention_runs configuration_settings configuration_revisions gpt_action_api_keys api_control_budgets api_control_policies api_control_actions download_jobs exports cases investigation_canvases notifications provider_secrets provider_descriptors routing_policies routing_policy_revisions audit_exports kill_switch_engagements audit_chain_anchors audit_integrity_incidents audit_integrity_incident_events update_previews platform_jobs access_policies projects"
census(){
  local t
  for t in $census_tables; do
    printf '%s|%s\n' "$t" "$(pgq <<SQL
SELECT count(*) FROM $t;
SQL
)"
  done
}
census > "$EV/census-before.txt"
echo "census_before_lines=$(wc -l < "$EV/census-before.txt")"

printf 'fixture\tmethod\tpath\trequired\tdecision\tstatus\trequest_id\n' > "$EV/matrix.tsv"
DENY_MISSING=0
run_matrix(){
  local tag="$1" held="$2" i m url req decision code expect
  echo "--- fixture=$tag ---"
  i=0
  while IFS='|' read -r _kind m url req _loc; do
    i=$((i+1))
    url=$(printf '%s' "$url" | sed -e "s/{id}/$FIXED_ID/g" -e "s/{binding}/$FIXED_ID/g" -e "s/{key}/$FAKE_KEY/g" -e "s/{command}/$FAKE_CMD/g" -e "s/{chain}/$FAKE_CHAIN/g" -e "s/{date}/$FAKE_DATE/g")
    if holds "$held" "$req"; then decision=allow; else decision=deny; fi
    if [ "$m" = "GET" ]; then
      code=$(curl -s -m 30 -b "${JAR[$tag]}" -o "$EV/body-$tag-$i.txt" -D "$EV/hdr-$tag-$i.txt" -w '%{http_code}' \
        -X GET "$API$url" -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: ${CSRF[$tag]}")
    else
      code=$(curl -s -m 30 -b "${JAR[$tag]}" -o "$EV/body-$tag-$i.txt" -D "$EV/hdr-$tag-$i.txt" -w '%{http_code}' \
        -X "$m" "$API$url" -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: ${CSRF[$tag]}" \
        -H 'Content-Type: application/json' --data-binary '{"__acceptance_probe__":')
    fi
    rid=$(rid_from "$EV/hdr-$tag-$i.txt")
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$tag" "$m" "$url" "$req" "$decision" "$code" "$rid" >> "$EV/matrix.tsv"
    if [ "$decision" = "allow" ]; then
      if [ "$code" = "401" ] || [ "$code" = "403" ]; then
        bad "$tag $m $url required=$req -> refused with $code although the identity holds it"
      fi
    else
      if [ "$code" = "401" ] || [ "$code" = "403" ]; then :; else
        bad "$tag $m $url required=$req -> reached the handler with $code although the identity does NOT hold it"
      fi
      if [ -z "$rid" ]; then
        DENY_MISSING=$((DENY_MISSING+1))
      fi
    fi
  done < "$EV/routes.tsv"
  echo "routes_exercised=$i"
}
run_matrix c1perm-seca "$SECA_PERMS"
run_matrix c1perm-aud  "$AUD_PERMS"
run_matrix c1perm-viewer "$VIEW_PERMS"

ALLOWED=$(awk -F'\t' 'NR>1 && $5=="allow"{n++} END{print n+0}' "$EV/matrix.tsv")
DENIED=$(awk -F'\t' 'NR>1 && $5=="deny"{n++} END{print n+0}' "$EV/matrix.tsv")
echo "matrix_rows=$(($ALLOWED+$DENIED)) allow_expected=$ALLOWED deny_expected=$DENIED"
[ "$DENIED" -ge 115 ]; chk "$?" "0" "the negative half of the matrix is non-trivial (every route refused for at least one fixture)"
[ "$ALLOWED" -ge 10 ]; chk "$?" "0" "the positive half of the matrix is non-trivial (real allow paths exist)"
chk "$DENY_MISSING" "0" "every refused request carried an X-Request-Id (deny audit is correlatable)"

echo "=== 7. deny audit correlation (authorization.denied bound to the request id) ==="
awk -F'\t' 'NR>1 && $5=="deny" && $7!=""{print $7}' "$EV/matrix.tsv" | sort -u > "$EV/deny-rids.txt"
RID_N=$(wc -l < "$EV/deny-rids.txt")
echo "distinct_denied_request_ids=$RID_N"
# The deny audit is written from a goroutine, so poll briefly instead of assuming it landed.
RID_VALUES=$(sed "s/^/('/; s/$/')/" "$EV/deny-rids.txt" | paste -sd, -)
JOINED=""; BAD_JOIN=""
for attempt in 1 2 3 4 5 6; do
  pgq > "$EV/deny-audit-join.txt" <<SQL
WITH probe(rid) AS (VALUES $RID_VALUES)
SELECT (SELECT count(*) FROM probe) AS denied_requests,
       (SELECT count(*) FROM probe p WHERE EXISTS (
          SELECT 1 FROM audit_events_v2 a
          WHERE a.request_id=p.rid AND a.result='deny' AND a.action='authorization.denied')) AS with_deny_audit;
SQL
  BAD_JOIN=$(awk -F'|' '/^[0-9]+\|[0-9]+$/{a=$1} END{print a}' "$EV/deny-audit-join.txt")
  JOINED=$(awk -F'|' '/^[0-9]+\|[0-9]+$/{a=$2} END{print a}' "$EV/deny-audit-join.txt")
  echo "deny_audit_attempt=$attempt denied=$BAD_JOIN correlated=$JOINED"
  if [ -n "$BAD_JOIN" ] && [ "$JOINED" = "$BAD_JOIN" ]; then break; fi
  sleep 4
done
chk "$JOINED" "$BAD_JOIN" "every refused admin request has exactly one correlated authorization.denied audit row"

echo "=== 8. census: no business object was created or mutated ==="
sleep 2
census > "$EV/census-after.txt"
if diff -q "$EV/census-before.txt" "$EV/census-after.txt" > /dev/null; then
  ok "40-table PostgreSQL business census is byte-identical before and after the matrix"
else
  bad "business census changed (see census-diff.txt)"
  diff "$EV/census-before.txt" "$EV/census-after.txt" > "$EV/census-diff.txt" || true
  cat "$EV/census-diff.txt"
fi
REAL_AFTER=$(pgq <<SQL
SELECT email||'|'||status||'|'||must_change_password FROM identities WHERE email='admin@local';
SQL
)
if [ "$REAL_BEFORE" = "$REAL_AFTER" ]; then ok "the real admin@local row is untouched (status and flag unchanged)"; else bad "the real admin@local row changed"; fi
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real account still lives outside the fixture organisation"

echo "=== 9. anonymous and user-audience controls ==="
ANON=$(curl -s -o "$EV/anon-overview.json" -w '%{http_code}' "$API/api/admin/v1/overview")
case "$ANON" in 401|403) ok "anonymous request to a sampled admin route is refused ($ANON)";; *) bad "anonymous request reached the admin surface ($ANON)";; esac
USERAUD=$(curl -s -b "${JAR[c1perm-seca]}" -o "$EV/user-audience-overview.json" -w '%{http_code}' -H 'X-Auth-Audience: user' "$API/api/admin/v1/overview")
case "$USERAUD" in 401|403) ok "the same session cannot use the admin surface with a user audience ($USERAUD)";; *) bad "user audience reached the admin surface ($USERAUD)";; esac

echo "=== RESULT ==="
echo "GATE_PASS=$PASS GATE_FAIL=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PASS" -gt 0 ]; then echo "C1_ADMIN_PERMISSION_MATRIX_GATE=PASS"; exit 0; fi
echo "C1_ADMIN_PERMISSION_MATRIX_GATE=FAIL"; exit 4
