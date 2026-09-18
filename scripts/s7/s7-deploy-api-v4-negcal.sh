#!/usr/bin/env bash
# s7-deploy-api-v4-negcal.sh -- negative calibration for s7-deploy-api-v4.sh.
#
# Requirement (root, 2026-09-13): the deployment script must not be "trusted because it
# passed once".  Each fail-closed gate has to be shown to actually fail, and the bounded
# readiness wait has to be shown to actually wait.  Calibration is therefore run against an
# ISOLATED temp tree with a stand-in HTTP service, a stub systemctl, a stub psql and a stub
# journalctl.  It never restarts, installs or probes the real user service.
#
# Cases:
#   C1 delayed-start readiness (binds only after 5s) -> rc 0 AND ready_attempts >= 4
#   C2 permanently not ready                          -> rc != 0
#   C3 wrong expected SHA                             -> rc != 0
#   C4 dependency not ready (clickhouse down)         -> rc != 0
#   C5 new binary identical to running binary         -> rc != 0
#   C6 restart does not change identity (pid/exe)     -> rc != 0
#   C7 HTTP 200 text/html "false ready"               -> rc != 0
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
DEPLOY="$HERE/s7-deploy-api-v4.sh"
STANDIN="$HERE/s7-deploy-api-v4-negcal-standin.py"
WORK="${NEGCAL_WORK:-$(mktemp -d /tmp/s7v4-negcal-XXXXXX)}"
CASE_FAILS=0
case_result(){ # name expected_rc actual_rc extra
  local name="$1" want="$2" got="$3" extra="$4"
  if [ "$want" = "zero" ] && [ "$got" -eq 0 ]; then echo "CASE_PASS $name rc=$got $extra"; return 0; fi
  if [ "$want" = "nonzero" ] && [ "$got" -ne 0 ]; then echo "CASE_PASS $name rc=$got $extra"; return 0; fi
  echo "CASE_FAIL $name rc=$got want=$want $extra"; CASE_FAILS=$((CASE_FAILS+1)); return 1
}

mkdir -p "$WORK"; chmod 700 "$WORK"
echo "work=$WORK deploy_sha256=$(sha256sum "$DEPLOY" | cut -d' ' -f1)"
echo "standin_sha256=$(sha256sum "$STANDIN" | cut -d' ' -f1)"

write_stubs(){ # case_dir -> creates bin/systemctl, bin/start.sh
  local C="$1"
  mkdir -p "$C/bin" "$C/state" "$C/exe" "$C/root/backend/bin"
  cat > "$C/bin/systemctl" <<'STUB'
#!/usr/bin/env bash
# Stub systemctl for the isolated deploy calibration. Only the verbs the deploy script uses.
C="$NEGCAL_CASE"
SVC="${!#}"
case "$1" in
  show)
    if [ -f "$C/state/pid.$SVC" ]; then cat "$C/state/pid.$SVC"; else echo 0; fi
    ;;
  is-active)
    if [ -f "$C/state/pid.$SVC" ] && kill -0 "$(cat "$C/state/pid.$SVC")" 2>/dev/null; then exit 0; fi
    exit 3
    ;;
  restart)
    if [ "${NEGCAL_RESTART_NOOP:-0}" = "1" ]; then
      # deliberately do nothing: identity must not change (C6)
      exit 0
    fi
    if [ -f "$C/state/pid.$SVC" ]; then
      OP=$(cat "$C/state/pid.$SVC")
      kill "$OP" 2>/dev/null; sleep 0.3; kill -9 "$OP" 2>/dev/null; sleep 0.3
    fi
    "$C/bin/start.sh" "$SVC"
    ;;
  *)
    exit 1
    ;;
esac
STUB
  cat > "$C/bin/start.sh" <<'START'
#!/usr/bin/env bash
# Start the isolated stand-in as the "service" and publish its pid + executable identity.
C="$NEGCAL_CASE"
SVC="$1"
# The service picks up whatever is installed in root/backend/bin/api: mirror it as the
# running executable so the exe-sha and exe-path gates keep their production semantics.
cp -f "$C/root/backend/bin/api" "$C/exe/exe"
NEGCAL_MODE="${NEGCAL_MODE:-ready}" NEGCAL_DELAY="${NEGCAL_DELAY:-0}" NEGCAL_PORT="${NEGCAL_PORT:-18099}" \
  python3 "$NEGCAL_STANDIN" >> "$C/standin.log" 2>&1 </dev/null &
echo $! > "$C/state/pid.$SVC"
for _ in $(seq 1 40); do
  kill -0 "$(cat "$C/state/pid.$SVC")" 2>/dev/null && break
  sleep 0.1
done
exit 0
START
  chmod +x "$C/bin/systemctl" "$C/bin/start.sh"
}

run_case(){ # name old_content new_content expected_sha_source mode delay restart_noop want extra_gate port
  local name="$1" oldc="$2" newc="$3" shasrc="$4" mode="$5" delay="$6" noop="$7" want="$8" extra="$9" port="${10}"
  local C="$WORK/$name"
  rm -rf "$C"; write_stubs "$C"
  printf '%s\n' "$oldc" > "$C/old.bin"
  printf '%s\n' "$newc" > "$C/new.bin"
  install -m 0755 "$C/old.bin" "$C/root/backend/bin/api"
  cp -f "$C/root/backend/bin/api" "$C/exe/exe"
  if [ "$shasrc" = "mismatch" ]; then printf 'deadbeef\n' > "$C/expected.sha"; else sha256sum "$C/new.bin" | cut -d' ' -f1 > "$C/expected.sha"; fi
  export NEGCAL_CASE="$C" NEGCAL_STANDIN="$STANDIN"
  export NEGCAL_MODE="$mode" NEGCAL_DELAY="$delay" NEGCAL_PORT="$port" NEGCAL_RESTART_NOOP="$noop"
  "$C/bin/start.sh" blockchain-api.service >/dev/null 2>&1
  sleep 0.4
  local OUT="$C/deploy.out"
  DEPLOY_ROOT="$C/root" DEPLOY_SERVICE="blockchain-api.service" DEPLOY_WORKER_SERVICE="blockchain-worker.service" \
  DEPLOY_SYSTEMCTL="$C/bin/systemctl" DEPLOY_EXE_DIR="$C/exe" DEPLOY_EXPECT_EXE="$C/exe/exe" \
  DEPLOY_PGQ_STUB=1 DEPLOY_JOURNAL_STUB=0 DEPLOY_SKIP_WORKER_GATE=1 DEPLOY_TMP="$C" \
  READY_URL="http://127.0.0.1:$port/health/ready" LIVE_URL="http://127.0.0.1:$port/health/live" \
  READY_TIMEOUT="${NEGCAL_READY_TIMEOUT:-12}" \
    bash "$DEPLOY" "$C/expected.sha" "$C/new.bin" > "$OUT" 2>&1
  local RC=$?
  local RESULT; RESULT=$(grep -m1 '^=== RESULT' "$OUT" || echo "RESULT_MISSING")
  local ATTEMPTS; ATTEMPTS=$(sed -n 's/.*ready_attempts=\([0-9]*\).*/\1/p' "$OUT" | head -1)
  local FAILS; FAILS=$(sed -n 's/.*RESULT PASS=[0-9]* FAIL=\([0-9]*\).*/\1/p' "$OUT" | head -1)
  case_result "$name" "$want" "$RC" "$RESULT attempts=${ATTEMPTS:-NA} fails=${FAILS:-NA} $extra"
  if [ "$name" = "C1_delayed_start" ]; then
    if [ "${ATTEMPTS:-0}" -ge 4 ]; then echo "CASE_PASS C1_bounded_wait_actually_waited attempts=$ATTEMPTS"; else echo "CASE_FAIL C1_bounded_wait_actually_waited attempts=${ATTEMPTS:-NA}"; CASE_FAILS=$((CASE_FAILS+1)); fi
  else
    if [ "${FAILS:-0}" -ge 1 ]; then echo "CASE_PASS ${name}_reports_nonzero_fail_gates fails=$FAILS"; else echo "CASE_FAIL ${name}_reports_nonzero_fail_gates fails=${FAILS:-NA}"; CASE_FAILS=$((CASE_FAILS+1)); fi
  fi
  if [ -f "$C/state/pid.blockchain-api.service" ]; then
    CP=$(cat "$C/state/pid.blockchain-api.service")
    kill "$CP" 2>/dev/null; sleep 0.2; kill -9 "$CP" 2>/dev/null
  fi
  sleep 0.3
}

run_case C1_delayed_start "old-binary-v1" "new-binary-v2" "match" ready 5 0 zero "positive_control" 18091
run_case C2_never_ready "old-binary-v1" "new-binary-v2" "match" never 0 0 nonzero "fail_closed" 18092
run_case C3_wrong_sha "old-binary-v1" "new-binary-v2" "mismatch" ready 0 0 nonzero "fail_closed" 18093
run_case C4_dependency_not_ready "old-binary-v1" "new-binary-v2" "match" dep_bad 0 0 nonzero "fail_closed" 18094
run_case C5_identical_binary "same-binary" "same-binary" "match" ready 0 0 nonzero "fail_closed" 18095
run_case C6_restart_no_identity_change "old-binary-v1" "new-binary-v2" "match" ready 0 1 nonzero "fail_closed" 18096
run_case C7_html_false_ready "old-binary-v1" "new-binary-v2" "match" html 0 0 nonzero "fail_closed" 18097

echo "=== NEGCAL RESULT case_fails=$CASE_FAILS work=$WORK ==="
[ "$CASE_FAILS" -eq 0 ] || exit 1
echo "NEGCAL_PASS=1"
exit 0
