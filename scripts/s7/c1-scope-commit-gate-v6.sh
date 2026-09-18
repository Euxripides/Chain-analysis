session_id: e11fc079-7204-48a8-ad32-0e42b49aa2e3
truncated: false
size: 14546

session_id: e11fc079-7204-48a8-ad32-0e42b49aa2e3
truncated: false
size: 13457

#!/usr/bin/env bash
# c1-scope-commit-gate-v1.sh -- P1-A, the window scopeBodyMiddleware does NOT cover.
#
# The v4 race gate switches the session while the request BODY is still uploading, i.e.
# before the authorization decision runs. This gate exercises the OTHER window: the
# authorization decision has already been taken (guard passed), and the handler has not
# committed yet. A calibration build (//go:build scopecalib) inserts a bounded delay at
# exactly that point of /api/v2/cases, so the window is real, deterministic and
# reproducible instead of depending on luck. The production binary never contains the
# delay: scopecommit_calib_off.go replaces it with an empty function.
#
# EXPECT=defect  the frozen pre-fix implementation is under test: the gate must PROVE that
#                the write still commits after the scope moved (stale commit).
# EXPECT=fixed   the commit-gate implementation is under test: the gate must PROVE zero
#                stale commits (refusal with a stable code, or a provable linearization).
#
# Everything runs inside the dedicated c1conv fixture organisation. The real admin account,
# its password, its memberships and every pre-existing case row are untouched. No cookie,
# CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
CAL=/home/euripides/s7calib
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:?set LABEL}"
EXPECT="${EXPECT:?set EXPECT to defect|fixed}"
PHASE="${PHASE:-pre}"
DELAY_MS="${DELAY_MS:-6000}"
PORT="${PORT:-18130}"
API_EXPECT_SHA="${API_EXPECT_SHA:-48e077e47f6504ccacc56fb31541f0eb6ebdd97f4aa2e44df73f822880e8d0ae}"
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
yes_no(){ if [ "$1" = "1" ]; then echo yes; else echo no; fi; }

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
rid_from(){ awk 'tolower($1)=="x-request-id:"{print $2}' "$1" 2>/dev/null | tr -d '\r' | tail -1; }
csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" | tail -1; }
session_ws(){ curl -s -b "$1" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['workspace_id']"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL expect=$EXPECT phase=$PHASE delay_ms=$DELAY_MS port=$PORT bin=$BIN"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

CLEANUP_DONE=0
calib_pid=""
cleanup(){
  if [ "$CLEANUP_DONE" = "0" ] && [ -n "$calib_pid" ]; then
    CLEANUP_DONE=1
    kill -TERM "$calib_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$calib_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$calib_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "=== 0. runtime identity: production API must stay exactly as it is ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)
echo "prod_api_pid=$AP prod_api_exe_sha256=$API_SHA"
chk "$API_SHA" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no PENDING/RUNNING export exists (the second API process reconciles those on boot)"

echo "=== 1. fixtures (dedicated c1conv organisation, two member workspaces) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1conv-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1conv -org-name "C1 session scope convergence acceptance" \
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
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IDID'::uuid AND status='active';
SQL
)" "2" "fixture identity holds two active memberships"
AUDIT0=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)
echo "audit_head_before=$AUDIT0"

JAR=$EV/jar.txt
rm -f "$JAR"
code=$(curl -s -c "$JAR" -o "$EV/login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW0\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$code" "200" "fixture identity logs in with its initial password"
CSRF0=$(csrf_of "$JAR")
[ -n "$CSRF0" ]; chk "$?" "0" "CSRF cookie present (value never printed)"
PW1="Pw-${TS}-c1commit-7b21"
code=$(curl -s -b "$JAR" -o "$EV/pwchange.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF0" \
  -d "{\"current_password\":\"$PW0\",\"new_password\":\"$PW1\"}")
[ "$code" = "200" ] || [ "$code" = "204" ]; chk "$?" "0" "initial-password flag cleared through the real endpoint (status=$code)"

login_ws(){ local jar="$1" ws="$2" c
  rm -f "$jar"
  c=$(curl -s -c "$jar" -o "$jar.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}")
  printf '%s' "$c"; }
case_count(){ pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$1'::uuid AND name LIKE '$2%';
SQL
}
audit_seq_of(){ pgq <<SQL
SELECT coalesce(max(seq),0) FROM audit_events_v2 WHERE request_id='$1';
SQL
}

echo "=== 2. start the isolated calibration instance on port $PORT ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export SCOPE_CALIB_ENABLE=1
  export SCOPE_CALIB_ROUTE=/api/v2/cases
  export SCOPE_CALIB_DELAY_MS="$DELAY_MS"
  export SCOPE_CALIB_PHASE="$PHASE"
  nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
  echo $! > "$EV/calib.pid" )
sleep 1
calib_pid=$(cat "$EV/calib.pid")
echo "calib_pid=$calib_pid"
READY=0
for _ in $(seq 1 40); do
  RC=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
  if [ "$RC" = "200" ]; then READY=1; break; fi
  sleep 0.5
done
chk "$READY" "1" "calibration instance is ready on port $PORT (http $RC)"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "1" "calibration instance really listens on $PORT only"
chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process is the binary under test"

run_scenario(){
  local tag="$1" action="$2" bodytag="$3"
  echo "=== scenario $tag: $action during the guard-passed/not-committed window ==="
  local JAR=$EV/jar-$tag.txt
  chk "$(login_ws "$JAR" "$WSA")" "200" "$tag: fresh session logs in to workspace A"
  local CSRF=$(csrf_of "$JAR")
  chk "$(session_ws "$JAR")" "$WSA" "$tag: session is in workspace A"
  local SID=$(curl -s -b "$JAR" -H 'X-Auth-Audience: user' "$API/api/auth/v1/session" | jget "['data']['session_id']")
  echo "$tag write_session_id=$SID"
  local NAME="CMTRACE-$TS-$bodytag"
  local A_BEFORE=$(case_count "$WSA" "CMTRACE-$TS")
  local B_BEFORE=$(case_count "$WSB" "CMTRACE-$TS")
  local S=$(date +%s)
  curl -s -b "$JAR" -o "$EV/$tag-write.json" -D "$EV/$tag-write.h" -w '%{http_code}' \
    -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF" \
    -d "{\"case_no\":\"$NAME\",\"name\":\"$NAME\"}" > "$EV/$tag-write-code.txt" 2> "$EV/$tag-write-err.txt" &
  local WPID=$!
  sleep 1
  MUT_START=$(date -u +%s)
  local MUT=$EV/$tag-mutate-code.txt
  if [ "$action" = "workspace_switch" ]; then
    curl -s -b "$JAR" -c "$JAR" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate.h" -w '%{http_code}' \
      -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
      -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
      -d "{\"workspace_id\":\"$WSB\"}" > "$MUT"
  elif [ "$action" = "logout" ]; then
    curl -s -b "$JAR" -c "$JAR" -o "$EV/$tag-mutate.json" -D "$EV/$tag-mutate.h" -w '%{http_code}' \
      -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
      -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d '{}' > "$MUT"
  else
    pgq > "$EV/$tag-mutate.json" <<SQL
UPDATE workspace_memberships SET status='removed', updated_at=now()
WHERE identity_id='$IDID'::uuid AND workspace_id='$WSA'::uuid;
SQL
    echo "sql" > "$MUT"
  fi
  MUT_END=$(date -u +%s)
  local MUT_ELAPSED=$(( MUT_END - MUT_START ))
  local BOUND=$(( DELAY_MS / 1000 - 1 ))
  wait $WPID
  local ELAPSED=$(( $(date +%s) - S ))
  local WCODE=$(cat "$EV/$tag-write-code.txt")
  local MCODE=$(cat "$MUT")
  local A_AFTER=$(case_count "$WSA" "CMTRACE-$TS")
  local B_AFTER=$(case_count "$WSB" "CMTRACE-$TS")
  local RIDW=$(rid_from "$EV/$tag-write.h")
  local RIDM=$(rid_from "$EV/$tag-mutate.h")
  local SEQW=0; local SEQM=0
  if [ -n "$RIDW" ]; then SEQW=$(audit_seq_of "$RIDW"); fi
  if [ -n "$RIDM" ]; then SEQM=$(audit_seq_of "$RIDM"); fi
  local NEWA=$((A_AFTER-A_BEFORE)); local NEWB=$((B_AFTER-B_BEFORE))
  local STALE=0
  local CASE_TS=$(pgq <<SQL
SELECT to_char(coalesce(max(created_at), to_timestamp(0)),'YYYY-MM-DD HH24:MI:SS.US') FROM cases
WHERE workspace_id='$WSA'::uuid AND name LIKE 'CMTRACE-$TS-$bodytag';
SQL
)
  local REVOKED_TS=$(pgq <<SQL
SELECT to_char(coalesce(max(revoked_at), to_timestamp(0)),'YYYY-MM-DD HH24:MI:SS.US') FROM sessions WHERE id='$SID'::uuid;
SQL
)
  local MUT_TS=$(pgq <<SQL
SELECT to_char(coalesce(max(occurred_at), to_timestamp(0)),'YYYY-MM-DD HH24:MI:SS.US') FROM audit_events_v2 WHERE request_id='$RIDM';
SQL
)
  local MUT_EFFECT_TS="$MUT_TS"
  if [ "$REVOKED_TS" \> "$MUT_EFFECT_TS" ]; then MUT_EFFECT_TS="$REVOKED_TS"; fi
  if [ "$NEWA" -gt 0 ] && [ "$CASE_TS" \> "$MUT_EFFECT_TS" ]; then STALE=1; fi
  {
    echo "scenario=$tag action=$action phase=$PHASE delay_ms=$DELAY_MS"
    echo "write_http_status=$WCODE write_request_id_present=$(if [ -n "$RIDW" ]; then echo yes; else echo no; fi)"
    echo "write_elapsed_seconds=$ELAPSED"
    echo "mutation_http_status=$MCODE mutation_request_id_present=$(if [ -n "$RIDM"; then echo yes; else echo no; fi)"
    echo "mutation_elapsed_seconds=$MUT_ELAPSED"
    echo "cases_named_CMTRACE_$TS in_A=$A_BEFORE->$A_AFTER in_B=$B_BEFORE->$B_AFTER"
    echo "audit_seq_write=$SEQW audit_seq_mutation=$SEQM"
    echo "stale_commit_proven=$STALE"
    echo "session_workspace_after=$(session_ws "$JAR")"
  } > "$EV/$tag-facts.txt"
  cat "$EV/$tag-facts.txt"
  chk "$NEWB" "0" "$tag: no object was ever created in the workspace the session moved to"
  case "$EXPECT" in
    defect)
      if [ "$NEWA" -gt 0 ] && [ "$STALE" = "1" ]; then
        ok "$tag: DEFECT PROVEN - the write committed in workspace A AFTER the scope mutation took effect (case_created_at=$CASE_TS > mutation_effect_at=$MUT_EFFECT_TS; audit seq $SEQM < $SEQW)"
      elif [ "$NEWA" = "0" ] && [ "$WCODE" != "201" ]; then
        ok "$tag: this window is ALREADY fail-closed by another mechanism (status=$WCODE, zero objects committed) - recorded, not the P1-A defect"
      else
        bad "$tag: inconclusive (status=$WCODE new_in_A=$NEWA case_ts=$CASE_TS mutation_effect_ts=$MUT_EFFECT_TS)"
      fi
      ;;
    fixed)
      if [ "$WCODE" = "409" ] && [ "$NEWA" = "0" ]; then
        ok "$tag: refused with 409 and zero objects committed anywhere ($ELAPSED s in flight)"
        grep -q 'AUTHZ_WORKSPACE_SCOPE_STALE' "$EV/$tag-write.json"
        chk "$?" "0" "$tag: refusal carries the stable code AUTHZ_WORKSPACE_SCOPE_STALE"
      elif [ "$NEWA" = "0" ] && [ "$WCODE" != "201" ]; then
        ok "$tag: refused by an existing guard with $WCODE and zero objects committed anywhere"
      elif [ "$NEWA" -gt 0 ] && [ "$MUT_ELAPSED" -ge "$BOUND" ]; then
        ok "$tag: commit linearized - the scope mutation was BLOCKED by the commit gate for ${MUT_ELAPSED}s (bound ${BOUND}s) and is ordered strictly after the committed write (write seq $SEQW, mutation seq $SEQM)"
      else
        bad "$tag: neither refusal nor a provable linearization (status=$WCODE new_in_A=$NEWA write_seq=$SEQW mutation_seq=$SEQM)"
      fi
      ;;
  esac
  if [ "$action" = "membership_revoke" ]; then
    pgq > "$EV/$tag-restore.json" <<SQL
UPDATE workspace_memberships SET status='active', updated_at=now()
WHERE identity_id='$IDID'::uuid AND workspace_id='$WSA'::uuid;
SQL
  fi
}

run_scenario s1-workspace-switch workspace_switch a-sw
run_scenario s2-logout logout a-lg
run_scenario s3-membership-revoke membership_revoke a-mr

echo "=== 4. census and production isolation ==="
chk "$(systemctl --user show blockchain-api.service -p MainPID --value)" "$AP" "production API pid is unchanged"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"
echo "audit_head_after=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)"

echo "=== 5. cleanup of everything this gate started ==="
cleanup
sleep 1
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT has no listener after cleanup"
if kill -0 "$calib_pid" 2>/dev/null; then bad "calibration process $calib_pid is still alive"; else ok "calibration process $calib_pid is gone"; fi
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SCOPE_COMMIT_GATE_${LABEL}_PASS=1"; else echo "C1_SCOPE_COMMIT_GATE_${LABEL}_PASS=0"; fi

