#!/usr/bin/env bash
# Isolated, NON-WRITING calibration for the shared rc-accounted helpers (scripts/s7/b2-raw-helpers.sh).
#
# Why: the previous round counted helper failures in a subshell-local variable, so the parent kept
# reading 0 while queries had actually failed, and an empty value could be compared against an
# empty expectation and "pass". This calibration proves, with the REAL helper functions, that:
#   * a failed PG query (non-existent table, unreachable database) records a NON-ZERO rc in the
#     persisted rc file and writes a line to the error log;
#   * a timed-out curl records a NON-ZERO rc and a line in its error log;
#   * the aggregate gate used by the round therefore goes non-zero, so empty-vs-empty can never be
#     reported as PASS;
#   * a healthy call still records rc=0 (the gate is not a constant).
# It only reads: the failing SQL references a table that does not exist and the failing curl points
# at TEST-NET-1. No business row, object, bucket or task is created, modified or deleted.
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$P/acceptance-evidence/dual-system-v1/sprint7/b2raw-helper-error-calibration-$TS
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee -a "$EV/calibration.console.log") 2>&1
cd $P
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
sha256sum $P/scripts/s7/b2-raw-helpers.sh | tee -a "$EV/run-meta.txt"
. "$P/scripts/s7/b2-raw-helpers.sh"
PASS=0; FAIL=0
check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }

echo "=== C1 healthy query records rc=0 ==="
GOOD=$(pgq "SET ROLE blockchain_app; select 1")
check C1 "healthy query returns its value" "$GOOD" "1"

echo "=== C2 non-existent table fails and records a non-zero rc ==="
BAD=$(pgq "SET ROLE blockchain_app; select 1 from b2cal_no_such_table")
check C2 "failed query returns no value (not a fake 0)" "$([ -z "$BAD" ] && echo empty || echo "value=$BAD")" "empty"
check C2 "a non-zero rc is recorded in the persisted rc file" "$(grep -cv '^0$' "$EV/pg-rcs.txt" || true)" "1"
check C2 "the failure is written to the error log" "$(grep -c 'ERROR' "$EV/pg-errors.log" || true)" "1"

echo "=== C3 unreachable database fails through the SAME helper (bounded) ==="
# The negative control must exercise the real helper, not a hand-written psql call: B2RAW_PG_HOST
# routes pgq at an unroutable TEST-NET-1 address, and the call must return on its own (bounded)
# rather than hanging. An earlier round hung here and had to be interrupted manually; that
# interruption is recorded in the previous evidence directory and is NOT a natural-timeout PASS.
B2RAW_PG_HOST=192.0.2.1
B2RAW_PG_TIMEOUT=8
C3_START=$(date +%s)
BAD2=$(pgq "select 1")
C3_RC=$?
C3_ELAPSED=$(( $(date +%s) - C3_START ))
B2RAW_PG_HOST=""
B2RAW_PG_TIMEOUT="20"
echo "$C3_ELAPSED" > "$EV/c3-elapsed_s"
echo "$C3_RC" > "$EV/c3-pgq-rc"
check C3 "unreachable database returns a non-zero rc through the real pgq" "$([ "$C3_RC" -ne 0 ] && echo nonzero || echo zero)" "nonzero"
check C3 "no value is produced" "$([ -z "$BAD2" ] && echo empty || echo "value=$BAD2")" "empty"
check C3 "the call was bounded (elapsed <= 15s, no manual interruption needed)" "$([ "$C3_ELAPSED" -le 15 ] && echo bounded || echo "hung: $C3_ELAPSED s")" "bounded"
check C3 "the connection failure is written to the persisted error log" "$([ "$(grep -c 'timeout\|refused\|could not connect\|No route' "$EV/pg-errors.log" || true)" -ge 1 ] && echo yes || echo no)" "yes"

echo "=== C4 HTTP timeout fails and records a non-zero rc ==="
B2RAW_CURL_TIMEOUT=2
CODE=$(httpc timeout "$EV/timeout.body" http://192.0.2.1:8080/health/ready)
B2RAW_CURL_TIMEOUT="120"
C4_RC=$(tail -1 "$EV/curl-rcs.txt")
check C4 "timed-out curl reports http_code 000 (NOT an empty string)" "$CODE" "000"
check C4 "timed-out curl raw rc is 28 (operation timeout)" "$C4_RC" "28"
check C4 "exactly one non-zero rc recorded in the persisted curl rc file" "$(grep -cv '^0$' "$EV/curl-rcs.txt" || true)" "1"
check C4 "the failure is written to the curl error log" "$(grep -c 'curl' "$EV/curl-errors.log" || true)" "1"

echo "=== C5 healthy HTTP call records rc=0 (gate is not constant) ==="
CODE2=$(httpc healthy "$EV/healthy.body" --max-time 10 http://127.0.0.1:8080/health/ready)
check C5 "healthy call returns 200" "$CODE2" "200"
check C5 "healthy call records rc=0" "$(tail -1 "$EV/curl-rcs.txt")" "0"

echo "=== C6 aggregate gate semantics ==="
COUNTS=$(helper_counts)
PG_TOTAL=$(printf '%s' "$COUNTS" | awk '{print $1}')
PG_BAD=$(printf '%s' "$COUNTS" | awk '{print $2}')
CURL_TOTAL=$(printf '%s' "$COUNTS" | awk '{print $3}')
CURL_BAD=$(printf '%s' "$COUNTS" | awk '{print $4}')
LOGS=$(helper_log_lines)
PG_LOG=$(printf '%s' "$LOGS" | awk '{print $1}')
CURL_LOG=$(printf '%s' "$LOGS" | awk '{print $2}')
echo "pg_total=$PG_TOTAL pg_bad=$PG_BAD curl_total=$CURL_TOTAL curl_bad=$CURL_BAD pg_log=$PG_LOG curl_log=$CURL_LOG" | tee "$EV/aggregate.txt"
check C6 "aggregate sees the two injected PG failures" "$PG_BAD" "2"
check C6 "aggregate sees the injected curl failure" "$CURL_BAD" "1"
check C6 "aggregate counted every call (pg_total=3, curl_total=2)" "$PG_TOTAL:$CURL_TOTAL" "3:2"
check C6 "error logs are non-empty, so the round gate would fail closed" "$([ "$PG_LOG" -ge 2 ] && [ "$CURL_LOG" -ge 1 ] && echo yes || echo no)" "yes"
check C6 "the failed PG helper produced no value at all (cannot equal a non-empty expectation)" "$([ -z "$BAD" ] && echo yes || echo no)" "yes"
check C6 "the timed-out HTTP helper produced 000, which no expected status code accepts" "$(printf '%s' "$CODE" | grep -c '^000$')" "1"
check C6 "C3 rc was recorded as a non-zero entry like every other call" "$([ "$(cat "$EV/c3-pgq-rc")" -ne 0 ] && echo yes || echo no)" "yes"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL" | tee "$EV/summary.txt"
if [ "$FAIL" -eq 0 ]; then
  echo "HELPER_ERROR_CALIBRATION=PASS"
  exit 0
fi
echo "HELPER_ERROR_CALIBRATION=FAIL"
exit 6
