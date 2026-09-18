#!/usr/bin/env bash
# c1-scope-commit-latency-gate-v1.sh -- P1-A residual: the commit gate holds a row
# lock on the caller session row from its re-check until the handler has committed.
# A scope mutation that overlaps that window has to queue behind the lock, so the
# mutation latency is bounded below by the handler duration. AUTH-007 requires a
# forced logout to take effect within 5 seconds, so the design is only acceptable
# while every gated handler is far shorter than that.
#
# The gate does not guess: it runs a calibration build whose delay sits INSIDE the
# critical section (PHASE=post, immediately before the handler) on a real
# production route, then measures four real scope mutations against the running
# production instance:
#
#   workspace switch   POST /api/auth/v1/session/workspace
#   logout             POST /api/auth/v1/logout
#   admin force logout POST /api/admin/v1/sessions/{id}/commands/revoke
#   membership revoke  direct UPDATE of workspace_memberships (recorded, and
#                      reported separately: it is NOT a sessions row UPDATE)
#
# Each mutation is measured twice: once with no gated request in flight (baseline)
# and once with a bounded slow gated request holding the lock. EXPECT=current means
# "the design under test must keep every forced logout within AUTH-007"; a
# contended measurement above the threshold is reported as a VIOLATION, not hidden.
# ASCII only. Fixtures live in a dedicated c1lat organisation; the real
# administrator account is untouched and no secret is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-latency-current}"
HANDLER_MS="${HANDLER_MS:-12000}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
PORT="${PORT:-18132}"
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

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
now_ms(){ date +%s%3N; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL handler_ms=$HANDLER_MS threshold_ms=$THRESHOLD_MS port=$PORT"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

calib_pid=""

# port_holders: 仍占用校准端口的进程（正常情况下为空）。
# 为什么需要它：API 启动要等 provider 能力探测完成才真正 bind（实测冷启动约 44 s），
# 若上一个用例的实例还没退干净就启动下一个，新实例会以
# "listen tcp :18132: bind: address already in use" 退出，
# 于是 readiness 循环一路 http 000 —— 这是**校准夹具自身的竞态**，
# 2026-09-16 实测把 AUTH-007 判据（contended_max_ms <= 5000）误记成 FAIL 两次。
port_holders(){ ss -ltnp 2>/dev/null | awk -v p=":$PORT" '$4 ~ p {print}' | grep -o "pid=[0-9]*" | cut -d= -f2 | sort -u; }

wait_port_free(){
  local i
  for i in $(seq 1 60); do
    [ -z "$(port_holders)" ] && return 0
    for hp in $(port_holders); do kill -TERM "$hp" 2>/dev/null || true; done
    sleep 0.5
  done
  for hp in $(port_holders); do kill -KILL "$hp" 2>/dev/null || true; done
  sleep 1
  [ -z "$(port_holders)" ]
}

cleanup(){
  if [ -n "$calib_pid" ] && kill -0 "$calib_pid" 2>/dev/null; then
    kill -TERM "$calib_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$calib_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$calib_pid" 2>/dev/null || true
  fi
  # 进程可能已被 setsid 重排到别的进程组，端口是唯一可靠的"退干净了"判据。
  wait_port_free || true
  calib_pid=""
}
trap cleanup EXIT

start_calib(){
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
    export SCOPE_CALIB_ROUTE=/api/v2/cases
    export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
    export SCOPE_CALIB_PHASE=post
    nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
    echo $! > "$EV/calib.pid" )
  sleep 1
  calib_pid=$(cat "$EV/calib.pid")
  # 启动前若端口已被占用（上一个实例没退干净），先等它退：
  # 否则新实例会在 bind 处直接退出，readiness 永远是 http 000。
  wait_port_free || true
  # 启动等待：API 在上线时会做一次 provider 能力探测（rpc capability probed，实测可达 30-100 s），
  # 40x0.5s=20 s 会把它误判成"校准实例不可用"（2026-09-16 实测 FAIL[1]，产品侧无异常：
  # 同一进程在第二次启动就已 ready）。这里把等待放宽到 60 s —— 放宽的是**等待**，
  # 不是任何产品判据。
  local ready=0 rc=""
  for _ in $(seq 1 120); do
    rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "calibration instance ready on $PORT (http $rc)"
  chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process is the binary under test"
}

echo "=== 0. runtime identity: the production API must not be disturbed ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "prod_api_pid=$AP"

echo "=== 1. fixtures (dedicated c1lat organisation) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 commit gate latency acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
INVID=$(read_cred "$CRED" C1MEM_C1LAT_INV_IDENTITY_ID)
echo "ws_a=$WSA ws_b=$WSB inv_identity=$INVID"
INVPW1="Pw-${TS}-c1latinv-71c4"
SECPW1="Pw-${TS}-c1latsec-2f83"
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
  [ "$c" = "200" ] || [ "$c" = "204" ]; chk "$?" "0" "$tag initial-password flag cleared (status=$c)"
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1"
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1"

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

CONTENDED_MAX=0
record(){
  local tag="$1" kind="$2" ms="$3" code="$4"
  echo "$tag kind=$kind elapsed_ms=$ms http=$code"
  echo "$tag|$kind|$ms|$code" >> "$EV/latency.tsv"
  if [ "$kind" = "contended" ]; then
    if [ "$ms" -gt "$CONTENDED_MAX" ]; then CONTENDED_MAX=$ms; fi
  fi
}

# run_case <tag> <action> <with_inflight>
run_case(){
  local tag="$1" action="$2" inflight="$3"
  echo "=== case $tag action=$action inflight=$inflight ($HANDLER_MS ms handler inside the critical section) ==="
  local W=$EV/jar-$tag-w.txt
  chk "$(user_login "$W" "$WSA")" "200" "$tag: writer session logs in to workspace A"
  local SID CSRF
  SID=$(sess_id "$W" user)
  CSRF=$(csrf_of "$W" cs_user_session_csrf)
  local WPID="" T0 T1 MS CODE KIND
  if [ "$inflight" = "yes" ]; then
    start_calib
    local NAME="LATRACE-$TS-$tag"
    curl -s -b "$W" -o "$EV/$tag-write.json" -D "$EV/$tag-write.h" -w '%{http_code}' \
      -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
      -H "X-CSRF-Token: $CSRF" \
      -d "{\"case_no\":\"$NAME\",\"name\":\"$NAME\"}" > "$EV/$tag-write-code.txt" 2>/dev/null &
    WPID=$!
    sleep 1
    KIND=contended
  else
    KIND=baseline
  fi
  T0=$(now_ms)
  case "$action" in
    workspace_switch)
      CODE=$(curl -s -m 120 -b "$W" -c "$W" -o "$EV/$tag-mutate.json" -w '%{http_code}' \
        -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d "{\"workspace_id\":\"$WSB\"}") ;;
    logout)
      CODE=$(curl -s -m 120 -b "$W" -c "$W" -o "$EV/$tag-mutate.json" -w '%{http_code}' \
        -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
        -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" -d '{}') ;;
    admin_revoke)
      local A=$EV/jar-$tag-admin.txt ACSRF
      admin_login "$A" "$WSA" > /dev/null
      ACSRF=$(csrf_of "$A" cs_admin_session_csrf)
      CODE=$(curl -s -m 120 -b "$A" -o "$EV/$tag-mutate.json" -w '%{http_code}' \
        -X POST "$API/api/admin/v1/sessions/$SID/commands/revoke" \
        -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $ACSRF" \
        -d '{"confirm":true,"reason":"c1lat-latency"}') ;;
    membership_revoke)
      local DJAR=$EV/jar-$tag-drop.txt
      user_login "$DJAR" "$WSA" > /dev/null
      CODE=$(pgq > "$EV/$tag-mutate.json" <<SQL
UPDATE workspace_memberships SET status='removed', updated_at=now()
WHERE identity_id='$INVID'::uuid AND workspace_id='$WSA'::uuid;
SELECT 1;
SQL
)
      CODE="rc=$?" ;;
  esac
  T1=$(now_ms)
  MS=$(( T1 - T0 ))
  if [ "$action" = "membership_revoke" ]; then
    # The measurement above is only meaningful if the next case starts from the same
    # state, so the fixture membership is put back exactly as it was found.
    pgq > /dev/null <<SQL
UPDATE workspace_memberships SET status='active', updated_at=now()
WHERE identity_id='$INVID'::uuid AND workspace_id='$WSA'::uuid;
SQL
  fi
  if [ -n "$WPID" ]; then wait $WPID; fi
  local WCODE
  WCODE=$(cat "$EV/$tag-write-code.txt" 2>/dev/null || echo n/a)
  record "$tag" "$KIND" "$MS" "$CODE"
  {
    echo "case=$tag action=$action kind=$KIND inflight=$inflight handler_ms=$HANDLER_MS"
    echo "mutation_elapsed_ms=$MS mutation_http=$CODE"
    echo "gated_write_http=$WCODE gated_write_route=/api/v2/cases"
    echo "writer_session_id=$SID"
  } >> "$EV/latency-facts.txt"
  if [ "$inflight" = "yes" ]; then cleanup; fi
}

: > "$EV/latency.tsv"; : > "$EV/latency-facts.txt"

run_case b1-workspace-switch workspace_switch no
run_case b2-logout logout no
run_case b3-admin-revoke admin_revoke no
run_case b4-membership-revoke membership_revoke no

run_case c1-workspace-switch workspace_switch yes
run_case c2-logout logout yes
run_case c3-admin-revoke admin_revoke yes
run_case c4-membership-revoke membership_revoke yes

echo "=== 3. AUTH-007 verdict ==="
echo "contended_max_ms=$CONTENDED_MAX threshold_ms=$THRESHOLD_MS"
awk -F'|' '$2=="baseline"{if($3+0>m)m=$3+0}END{print "baseline_max_ms="m+0}' "$EV/latency.tsv"
awk -F'|' '$2=="contended"{printf "contended %s=%s ms\n", $1, $3}' "$EV/latency.tsv"
if [ "$CONTENDED_MAX" -le "$THRESHOLD_MS" ]; then
  ok "every contended scope mutation stayed within AUTH-007 ($CONTENDED_MAX ms <= $THRESHOLD_MS ms)"
  echo "AUTH007_VIOLATION=0"
else
  bad "AUTH-007 VIOLATION: a contended scope mutation took $CONTENDED_MAX ms (> $THRESHOLD_MS ms) while a ${HANDLER_MS} ms handler held the gate"
  echo "AUTH007_VIOLATION=1"
fi
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready after the measurement"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary unchanged after the measurement"
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"

echo "=== 4. cleanup ==="
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT has no listener after cleanup"
chk "$(ss -ltn 2>/dev/null | grep -cE ':(18080|18090) ')" "0" "no listener on the forbidden ports 18080/18090"
echo "audit_head_after=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SCOPE_COMMIT_LATENCY_${LABEL}_PASS=1"; else echo "C1_SCOPE_COMMIT_LATENCY_${LABEL}_PASS=0"; fi
