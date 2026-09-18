#!/usr/bin/env bash
# c1-scope-commit-gate-v7.sh -- P1-A residual: admin:session:revoke is NOT a
# self-only surface, so excluding the whole permission from the commit gate
# reopened the guard-passed/not-committed window for a cross-session force logout.
#
# backend/internal/api/adminv1.go registers
#   POST /admin/v1/sessions/{id}/commands/revoke -> s.require("admin:session:revoke", ...)
# and the handler calls iam.Sessions().RevokeByID(path id): it revokes the session
# NAMED IN THE PATH, which is normally somebody else's session. The v6 gate
# excluded the whole permission because a SELF-targeted call would queue behind
# the row lock the gate itself holds. That exclusion removed the gate for every
# target, not just for the caller's own row.
#
# This gate drives exactly that window with the calibration seam (a bounded delay
# between the permission decision and the handler):
#   EXPECT=defect  the frozen candidate implementation must PROVE that the target
#                  session is still revoked after the caller's authority was
#                  withdrawn inside the window (stale cross-session revocation).
#   EXPECT=fixed   the candidate must PROVE 409 AUTHZ_WORKSPACE_SCOPE_STALE with
#                  zero effect on the target session, plus exactly one deny audit
#                  row, and that the self-targeted call still succeeds.
#
# Everything runs inside a dedicated c1revoke fixture organisation. The real
# administrator account, its password, its memberships and every pre-existing
# session row are untouched. No cookie, CSRF token, password or DSN is printed.
# ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:?set LABEL}"
EXPECT="${EXPECT:?set EXPECT to defect|fixed}"
PHASE="${PHASE:-pre}"
DELAY_MS="${DELAY_MS:-6000}"
PORT="${PORT:-18131}"
API_EXPECT_SHA="${API_EXPECT_SHA:-bfcea45b88b7b253799834643a435a2d9c415f5cc68fd0d7bfbdba7b3dd45b3e}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-scope-commit-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
note(){ echo "NOTE: $1"; }

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
sess_ws(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['workspace_id']"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL expect=$EXPECT phase=$PHASE delay_ms=$DELAY_MS port=$PORT"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

calib_pid=""
CLEANUP_DONE=0
cleanup(){
  if [ -n "$calib_pid" ] && kill -0 "$calib_pid" 2>/dev/null; then
    kill -TERM "$calib_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$calib_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$calib_pid" 2>/dev/null || true
    sleep 1
  fi
  calib_pid=""
}
trap cleanup EXIT

start_calib(){
  local route="$1"
  cleanup
  chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
  chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no PENDING/RUNNING export exists before starting the second API process"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$PORT"
    export SCOPE_CALIB_ENABLE=1
    export SCOPE_CALIB_ROUTE="$route"
    export SCOPE_CALIB_DELAY_MS="$DELAY_MS"
    export SCOPE_CALIB_PHASE="$PHASE"
    nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
    echo $! > "$EV/calib.pid" )
  sleep 1
  calib_pid=$(cat "$EV/calib.pid")
  local ready=0 rc=""
  for _ in $(seq 1 40); do
    rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "calibration instance ready on $PORT (http $rc) for route $route"
  chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process is the binary under test"
}

echo "=== 0. runtime identity: production API must stay exactly as it is ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "prod_api_pid=$AP prod_api_exe_sha256=$API_SHA"
chk "$API_SHA" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"

echo "=== 1. fixtures (dedicated c1revoke organisation, one workspace pair) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1revoke-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1revoke -org-name "C1 admin revoke commit-gate acceptance" \
    -workspaces c1revoke-a,c1revoke-b \
    -identities security_admin:c1revoke-sec@fixture.local:c1revoke-a+c1revoke-b,investigator:c1revoke-inv@fixture.local:c1revoke-a \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
ORG=$(read_cred "$CRED" C1MEM_ORG_ID)
WSA=$(read_cred "$CRED" C1MEM_WS_C1REVOKE_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1REVOKE_B)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1REVOKE_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1REVOKE_SEC_PW)
SECID=$(read_cred "$CRED" C1MEM_C1REVOKE_SEC_IDENTITY_ID)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1REVOKE_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1REVOKE_INV_PW)
INVID=$(read_cred "$CRED" C1MEM_C1REVOKE_INV_IDENTITY_ID)
echo "org=$ORG ws_a=$WSA ws_b=$WSB sec_identity=$SECID inv_identity=$INVID"
chk "$(pgq <<SQL
SELECT count(*) FROM member_role_bindings b JOIN roles r ON r.id=b.role_id
WHERE b.identity_id='$SECID'::uuid AND r.key='security_admin';
SQL
)" "1" "the caller fixture really holds the security_admin role in PostgreSQL"
chk "$(pgq <<SQL
SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id
WHERE r.key='security_admin' AND rp.permission_key='admin:session:revoke';
SQL
)" "1" "security_admin really carries admin:session:revoke"

SECPW1="Pw-${TS}-c1revsec-4d18"
INVPW1="Pw-${TS}-c1revinv-9a02"
clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
  chk "$c" "200" "$tag fixture logs in with its initial password"
  csrf=$(csrf_of "$jar" cs_user_session_csrf)
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  [ "$c" = "200" ] || [ "$c" = "204" ]; chk "$?" "0" "$tag initial-password flag cleared through the real endpoint (status=$c)"
}
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1"
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1"

user_login(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
admin_login(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
    -d "{\"identifier\":\"$SECEMAIL\",\"password\":\"$SECPW1\",\"audience\":\"admin\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
revoked_at(){ pgq <<SQL
SELECT to_char(coalesce(revoked_at, to_timestamp(0)),'YYYY-MM-DD HH24:MI:SS.US') FROM sessions WHERE id='$1'::uuid;
SQL
}
revoked_reason(){ pgq <<SQL
SELECT coalesce(revoked_reason,'') FROM sessions WHERE id='$1'::uuid;
SQL
}
audit_of(){ pgq <<SQL
SELECT coalesce(max(seq),0)||'|'||count(*) FROM audit_events_v2 WHERE request_id='$1';
SQL
}
deny_rows(){ pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE request_id='$1' AND action='authorization.denied';
SQL
}
deny_reason(){ pgq <<SQL
SELECT coalesce(detail->>'reason','') FROM audit_events_v2
WHERE request_id='$1' AND action='authorization.denied' ORDER BY seq DESC LIMIT 1;
SQL
}

AUDIT0=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)
echo "audit_head_before=$AUDIT0"

run_adminrevoke(){
  local tag="$1" action="$2"
  echo "=== scenario $tag: cross-session admin revoke inside the guard-passed window ($action) ==="
  local TJAR=$EV/jar-$tag-target.txt CJAR=$EV/jar-$tag-caller.txt SJAR=$EV/jar-$tag-second.txt
  chk "$(user_login "$TJAR" "$WSA")" "200" "$tag: target user session logs in"
  local TSID
  TSID=$(sess_id "$TJAR" user)
  local ACTIVE0
  ACTIVE0=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$TSID'::uuid AND revoked_at IS NULL;
SQL
)
  chk "$ACTIVE0" "1" "$tag: target session starts active"
  chk "$(admin_login "$CJAR" "$WSA")" "200" "$tag: caller admin-audience session logs in"
  local CSID CCSRF
  CSID=$(sess_id "$CJAR" admin)
  CCSRF=$(csrf_of "$CJAR" cs_admin_session_csrf)
  chk "$(sess_ws "$CJAR" admin)" "$WSA" "$tag: caller session is in workspace A"
  [ "$CSID" != "$TSID" ]; chk "$?" "0" "$tag: the path target is NOT the caller session"
  echo "$tag target_session_id=$TSID caller_session_id=$CSID action=$action"

  start_calib "/api/admin/v1/sessions/$TSID/commands/revoke"

  local S=$(date +%s)
  curl -s -b "$CJAR" -o "$EV/$tag-revoke.json" -D "$EV/$tag-revoke.h" -w '%{http_code}' \
    -X POST "$CALAPI/api/admin/v1/sessions/$TSID/commands/revoke" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CCSRF" \
    -d '{"confirm":true,"reason":"c1revoke-window"}' > "$EV/$tag-revoke-code.txt" 2> "$EV/$tag-revoke-err.txt" &
  local WPID=$!
  sleep 1
  local MUT=$EV/$tag-mutate-code.txt
  local MUT_START=$(date -u +%s)
  case "$action" in
    caller_logout)
      curl -s -b "$CJAR" -c "$CJAR" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate.h" -w '%{http_code}' \
        -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CCSRF" -d '{}' > "$MUT" ;;
    caller_force_revoked_by_admin)
      chk "$(admin_login "$SJAR" "$WSA")" "200" "$tag: second admin session logs in"
      local SCSRF
      SCSRF=$(csrf_of "$SJAR" cs_admin_session_csrf)
      curl -s -b "$SJAR" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate.h" -w '%{http_code}' \
        -X POST "$API/api/admin/v1/sessions/$CSID/commands/revoke" \
        -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $SCSRF" \
        -d '{"confirm":true,"reason":"c1revoke-force-logout"}' > "$MUT" ;;
    caller_workspace_switch)
      curl -s -b "$CJAR" -c "$CJAR" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate.h" -w '%{http_code}' \
        -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CCSRF" \
        -d "{\"workspace_id\":\"$WSB\"}" > "$MUT" ;;
  esac
  local MUT_ELAPSED=$(( $(date -u +%s) - MUT_START ))
  wait $WPID
  local ELAPSED=$(( $(date +%s) - S ))
  local RCODE MCODE
  RCODE=$(cat "$EV/$tag-revoke-code.txt")
  MCODE=$(cat "$MUT")
  local RIDR RIDM SEQR SEQM DENIES REASON T_REVOKED T_REASON
  RIDR=$(rid_from "$EV/$tag-revoke.h")
  RIDM=$(rid_from "$EV/$tag-mutate.h")
  SEQR=0; SEQM=0
  if [ -n "$RIDR" ]; then SEQR=$(audit_of "$RIDR" | cut -d'|' -f1); fi
  if [ -n "$RIDM" ]; then SEQM=$(audit_of "$RIDM" | cut -d'|' -f1); fi
  DENIES=0; REASON=""
  if [ -n "$RIDR" ]; then DENIES=$(deny_rows "$RIDR"); REASON=$(deny_reason "$RIDR"); fi
  T_REVOKED=$(revoked_at "$TSID")
  T_REASON=$(revoked_reason "$TSID")
  local MUT_EFFECT=CALLER_SCOPE_NOT_MOVED
  local C_REVOKED C_WS_NOW
  C_REVOKED=$(revoked_at "$CSID")
  C_WS_NOW=""
  case "$action" in
    caller_logout|caller_force_revoked_by_admin) MUT_EFFECT="$C_REVOKED" ;;
    caller_workspace_switch) C_WS_NOW=$(pgq <<SQL
SELECT coalesce(workspace_id::text,'') FROM sessions WHERE id='$CSID'::uuid;
SQL
)
      MUT_EFFECT="$C_WS_NOW" ;;
  esac
  local STALE=0
  if [ "$T_REVOKED" != "1970-01-01 00:00:00.000000" ] && [ "$MUT_EFFECT" != "CALLER_SCOPE_NOT_MOVED" ]; then
    if [ "$T_REVOKED" \> "$MUT_EFFECT" ]; then STALE=0; else STALE=1; fi
  fi
  {
    echo "scenario=$tag action=$action phase=$PHASE delay_ms=$DELAY_MS"
    echo "target_session_id=$TSID caller_session_id=$CSID"
    echo "revoke_http_status=$RCODE revoke_request_id_present=$(if [ -n "$RIDR" ]; then echo yes; else echo no; fi)"
    echo "revoke_elapsed_seconds=$ELAPSED"
    echo "mutation_http_status=$MCODE mutation_elapsed_seconds=$MUT_ELAPSED"
    echo "target_session_revoked_at=$T_REVOKED target_revoked_reason=$T_REASON"
    echo "caller_session_revoked_at=$C_REVOKED caller_workspace_now=$C_WS_NOW"
    echo "mutation_effect_at=$MUT_EFFECT"
    echo "audit_seq_revoke=$SEQR audit_seq_mutation=$SEQM deny_rows_for_revoke_request=$DENIES deny_reason=$REASON"
  } > "$EV/$tag-facts.txt"
  cat "$EV/$tag-facts.txt"
  case "$EXPECT" in
    defect)
      if [ "$RCODE" = "200" ] && [ "$T_REVOKED" != "1970-01-01 00:00:00.000000" ] && [ "$MUT_EFFECT" != "CALLER_SCOPE_NOT_MOVED" ]; then
        ok "$tag: DEFECT PROVEN - the cross-session force logout committed at $T_REVOKED although the caller authority had already moved at $MUT_EFFECT (target_reason=$T_REASON, mutation_status=$MCODE)"
      elif [ "$RCODE" = "200" ] && [ "$MUT_EFFECT" = "CALLER_SCOPE_NOT_MOVED" ]; then
        bad "$tag: the mutation itself did not take effect, the window was not exercised (mutation_status=$MCODE)"
      else
        bad "$tag: inconclusive (revoke_status=$RCODE target_revoked_at=$T_REVOKED mutation_effect=$MUT_EFFECT mutation_status=$MCODE)"
      fi
      ;;
    fixed)
      if [ "$RCODE" = "409" ] && [ "$T_REVOKED" = "1970-01-01 00:00:00.000000" ]; then
        ok "$tag: refused with 409 and the target session is untouched (mutation_status=$MCODE mutation_effect=$MUT_EFFECT)"
        grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE' "$EV/$tag-revoke.json"
        chk "$?" "0" "$tag: refusal carries the stable code AUTHZ_WORKSPACE_SCOPE_STALE"
        chk "$DENIES" "1" "$tag: exactly one deny audit row was written for this request id"
        case "$REASON" in
          session_missing|session_workspace_changed) ok "$tag: deny reason is specific ($REASON)";;
          *) bad "$tag: deny reason is unexpected ('$REASON')";;
        esac
      elif [ "$RCODE" = "200" ] && [ "$T_REVOKED" != "1970-01-01 00:00:00.000000" ] && [ "$MUT_EFFECT" != "CALLER_SCOPE_NOT_MOVED" ] && [ "$T_REVOKED" \< "$MUT_EFFECT" ]; then
        ok "$tag: linearized - the revoke committed at $T_REVOKED strictly BEFORE the caller scope moved at $MUT_EFFECT (gate held the row lock, the mutation queued behind it, mutation_elapsed=${MUT_ELAPSED}s)"
      elif [ "$RCODE" = "200" ] && [ "$DENIES" -ge 1 ]; then
        bad "$tag: refused after the effect already committed (status=$RCODE target_revoked_at=$T_REVOKED mutation_effect=$MUT_EFFECT)"
      else
        bad "$tag: neither a clean refusal nor a provable linearization (revoke_status=$RCODE target_revoked_at=$T_REVOKED mutation_effect=$MUT_EFFECT denies=$DENIES)"
      fi
      ;;
  esac
}

run_self_target(){
  echo "=== scenario s7-self-target: the caller revokes its OWN session (self-lock control) ==="
  local CJAR=$EV/jar-s7-caller.txt
  chk "$(admin_login "$CJAR" "$WSA")" "200" "s7: admin session logs in"
  local CSID CCSRF
  CSID=$(sess_id "$CJAR" admin)
  CCSRF=$(csrf_of "$CJAR" cs_admin_session_csrf)
  local S=$(date +%s)
  local CODE
  CODE=$(curl -s -m 20 -b "$CJAR" -o "$EV/s7-self-revoke.json" -D "$EV/s7-self-revoke.h" -w '%{http_code}' \
    -X POST "$API/api/admin/v1/sessions/$CSID/commands/revoke" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CCSRF" \
    -d '{"confirm":true,"reason":"c1revoke-self"}')
  local ELAPSED=$(( $(date +%s) - S ))
  local T_REVOKED
  T_REVOKED=$(revoked_at "$CSID")
  {
    echo "scenario=s7-self-target action=self_revoke"
    echo "self_revoke_http_status=$CODE self_revoke_elapsed_seconds=$ELAPSED"
    echo "self_session_revoked_at=$T_REVOKED"
  } > "$EV/s7-self-target-facts.txt"
  cat "$EV/s7-self-target-facts.txt"
  chk "$CODE" "200" "s7: the self-targeted admin revoke still succeeds (no self-deadlock)"
  [ "$ELAPSED" -le 10 ]; chk "$?" "0" "s7: the self-targeted revoke returned within 10s (elapsed=${ELAPSED}s)"
  if [ "$T_REVOKED" != "1970-01-01 00:00:00.000000" ]; then ok "s7: the caller own session row is really revoked ($T_REVOKED)"; else bad "s7: the caller own session was not revoked"; fi
}

run_adminrevoke a1-caller-logout caller_logout
run_adminrevoke a2-caller-force-revoked caller_force_revoked_by_admin
run_adminrevoke a3-caller-workspace-switch caller_workspace_switch
run_self_target

echo "=== 4. census and production isolation ==="
chk "$(systemctl --user show blockchain-api.service -p MainPID --value)" "$AP" "production API pid is unchanged"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND organization_id <> '$ORG'::uuid;
SQL
)" "1" "the real administrator still lives outside the fixture organisation"
echo "audit_head_after=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)"

echo "=== 5. cleanup of everything this gate started ==="
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT has no listener after cleanup"
if [ -n "$calib_pid" ] && kill -0 "$calib_pid" 2>/dev/null; then bad "calibration process $calib_pid is still alive"; else ok "calibration process is gone"; fi
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SCOPE_COMMIT_GATE_${LABEL}_PASS=1"; else echo "C1_SCOPE_COMMIT_GATE_${LABEL}_PASS=0"; fi
