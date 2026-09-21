#!/usr/bin/env bash
# Runner for the B3 v2 isolated negative calibration.
# Freezes SHAs, runs the harness self-check (must exit nonzero) and the real round
# (must exit zero with B3V2_RESULT=PASS), copies every artifact into an immutable
# evidence directory and propagates a nonzero exit if any part is not proven.
# ASCII-only. Nothing is deleted; the v1 round stays untouched.
set -u
set -o pipefail

TS=$(date -u +%Y%m%dT%H%M%SZ)
P=/home/euripides/projects/blockchain-platform
EV=$P/acceptance-evidence/dual-system-v1/sprint7/b3-isolated-negcal-v2-$TS
CAL=$P/scripts/s7/b3-negcal-v2.sh
REP=$P/scripts/s7/b3-negcal-v2-rep.py
mkdir -p "$EV"

{
  echo "EV=$EV"
  echo "stamp_utc=$TS"
  echo "--- script/parser/binaries/config ---"
  sha256sum "$CAL" "$REP" 2>&1
  sha256sum "$P/backend/bin/api" "$P/backend/bin/worker" "$P/backend/bin/exportmigrate.next" "$P/backend/bin/objectprobe" "$P/deploy/.env" 2>&1
  echo "api_pid=$(systemctl --user show blockchain-api.service -p MainPID --value)"
  echo "api_state=$(systemctl --user show blockchain-api.service -p ActiveState --value)"
  echo "worker_pid=$(systemctl --user show blockchain-worker.service -p MainPID --value)"
  echo "worker_state=$(systemctl --user show blockchain-worker.service -p ActiveState --value)"
  echo "--- static checks ---"
} > "$EV/freeze.txt" 2>&1

bash -n "$CAL"
BN=$?
python3 -m py_compile "$REP"
PY=$?
echo "bash_n_rc=$BN" >> "$EV/freeze.txt"
echo "py_compile_rc=$PY" >> "$EV/freeze.txt"
if [ "$BN" -ne 0 ] || [ "$PY" -ne 0 ]; then
  echo "RUNNER_RESULT=FAIL_STATIC_CHECK"
  exit 5
fi

bash "$CAL" --selfcheck > "$EV/selfcheck.log" 2>&1
SC=$?
bash "$CAL" > "$EV/round.log" 2>&1
RR=$?

WC=$(grep -m1 '^B3V2_WC=' "$EV/round.log" | cut -d= -f2)
if [ -n "$WC" ] && [ -d "$WC" ]; then
  mkdir -p "$EV/workdir"
  cp -a "$WC"/. "$EV/workdir/"
fi

VERDICT=$(grep -m1 '^B3V2_RESULT=' "$EV/round.log" | cut -d= -f2)
COUNTS=$(grep -m1 '^B3V2_PASS=' "$EV/round.log" | cut -d= -f2-)
PREFIX=$(grep -m1 '^B3V2_OBJECT_PREFIX=' "$EV/round.log" | cut -d= -f2-)
ASSERT_N=$(grep -m1 '^B3V2_ASSERTION_IDS=' "$EV/round.log" | cut -d= -f2-)

SC_NONZERO=no
if [ "$SC" -ne 0 ]; then SC_NONZERO=yes; fi
SC_CONFIRMED=no
if grep -q 'SELFCHECK_RESULT=NONZERO_PROPAGATION_CONFIRMED' "$EV/selfcheck.log"; then SC_CONFIRMED=yes; fi
SC_TRUE_PASS=no
if grep -q 'SELFCHECK_TRUE_PASS ok' "$EV/selfcheck.log"; then SC_TRUE_PASS=yes; fi

{
  echo "runner_stamp_utc=$TS"
  echo "calibration_script_sha256=$(sha256sum "$CAL" | cut -d' ' -f1)"
  echo "parser_sha256=$(sha256sum "$REP" | cut -d' ' -f1)"
  echo "selfcheck_rc=$SC"
  echo "selfcheck_nonzero=$SC_NONZERO"
  echo "selfcheck_nonzero_propagation_confirmed=$SC_CONFIRMED"
  echo "selfcheck_true_assertion_passed=$SC_TRUE_PASS"
  echo "round_rc=$RR"
  echo "round_verdict=$VERDICT"
  echo "round_counts=$COUNTS"
  echo "round_object_prefix=$PREFIX"
  echo "round_assertion_ids=$ASSERT_N (expected 57 = 52 substantive + 5 gate)"
  echo "workdir_copied=$([ -d "$EV/workdir" ] && echo yes || echo no)"
} > "$EV/RUNNER.txt"

cat "$EV/RUNNER.txt"

if [ "$SC_NONZERO" = "yes" ] && [ "$SC_CONFIRMED" = "yes" ] && [ "$RR" -eq 0 ] && [ "$VERDICT" = "PASS" ] && [ "$ASSERT_N" = "57" ]; then
  echo "RUNNER_RESULT=PASS"
  exit 0
fi
echo "RUNNER_RESULT=FAIL"
exit 6
