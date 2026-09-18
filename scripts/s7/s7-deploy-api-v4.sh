#!/usr/bin/env bash
# s7-deploy-api-v4.sh -- controlled API deployment for the C round (v4).
#
# Lineage: v1 = r10-deploy-api-strict.sh (FROZEN, never modified; its 14 PASS / 3 FAIL
# round stays on record untouched).  v2 = r10-deploy-api-v2.sh (bounded readiness wait).
# v4 keeps EVERY v1/v2 gate and adds the ones the root required after the v1 3 FAIL round.
#
# v1 defect that produced the 3 FAIL (root-confirmed): after restart it waited only for
# "is-active" and then curl'ed immediately, i.e. before the listener existed, so the two
# readiness curls produced no file.  v4 therefore NEVER accepts "service is active" as
# readiness: it polls the real API readiness endpoint with a bounded budget, then validates
# the FULL JSON contract (status=ready AND all four dependencies ok) and rejects a bare
# HTTP 200 (the nginx 8448 entry serves Vite HTML for /health/*, a false ready).
#
# Every gate is calibration-able: the negative calibration
# (s7-deploy-api-v4-negcal.sh) runs THIS EXACT FILE against an isolated temp tree with a
# stand-in service and proves each fail-closed gate really exits non-zero.
set -uo pipefail

ROOT="${DEPLOY_ROOT:-/home/euripides/projects/blockchain-platform}"
SERVICE="${DEPLOY_SERVICE:-blockchain-api.service}"
WORKER_SERVICE="${DEPLOY_WORKER_SERVICE:-blockchain-worker.service}"
SYSTEMCTL="${DEPLOY_SYSTEMCTL:-systemctl --user}"
READY_URL="${READY_URL:-http://127.0.0.1:8080/health/ready}"
LIVE_URL="${LIVE_URL:-http://127.0.0.1:8080/health/live}"
READY_TIMEOUT="${READY_TIMEOUT:-60}"
EXPECT_EXE="${DEPLOY_EXPECT_EXE:-$ROOT/backend/bin/api}"
EXPECTED_WORKER_SHA="${EXPECTED_WORKER_SHA:-513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48}"
TMPDIR_RUN="${DEPLOY_TMP:-/tmp}"

EXPECT_SHA_FILE="$1"
NEW_BIN="$2"

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
gate(){ if [ "$1" -eq 0 ]; then ok "$2"; else bad "$2"; fi; }

# ---- indirection points (real commands in production, stubs in calibration) ----
svc_pid(){ $SYSTEMCTL show -p MainPID --value "$SERVICE" 2>/dev/null | tr -d '[:space:]'; }
svc_restart(){ $SYSTEMCTL restart "$SERVICE"; }
exe_path(){ if [ -n "${DEPLOY_EXE_DIR:-}" ]; then printf '%s/exe' "$DEPLOY_EXE_DIR"; else printf '/proc/%s/exe' "$1"; fi; }
exe_sha(){ sha256sum "$(exe_path "$1")" 2>/dev/null | cut -d' ' -f1; }
exe_real(){ readlink -f "$(exe_path "$1")" 2>/dev/null; }
pgq(){
  if [ -n "${DEPLOY_PGQ_STUB:-}" ]; then cat >/dev/null; printf '%s\n' "$DEPLOY_PGQ_STUB"; return 0; fi
  docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1
}
journal_count(){
  if [ -n "${DEPLOY_JOURNAL_STUB:-}" ]; then printf '%s\n' "$DEPLOY_JOURNAL_STUB"; return 0; fi
  journalctl --user -u "$SERVICE" --since "-120s" --no-pager 2>/dev/null | grep -Eic "panic|fatal" || true
}

EXPECT=$(tr -d '[:space:]' < "$EXPECT_SHA_FILE")
NEWSHA=$(sha256sum "$NEW_BIN" | cut -d' ' -f1)
PID0=$(svc_pid)
SHA0=$(exe_sha "$PID0")
EXE0=$(exe_real "$PID0")
echo "root=$ROOT service=$SERVICE pid0=$PID0 exe0=$EXE0 sha0=$SHA0 new=$NEWSHA expect=$EXPECT"

echo "=== P0 pre-state ==="
[ "$NEWSHA" = "$EXPECT" ]; gate $? "new binary matches expected frozen sha"
if [ "$SHA0" = "$NEWSHA" ]; then bad "new binary identical to running binary (no-op deploy?)"; else ok "new binary differs from running binary"; fi
[ -n "$PID0" ] && [ "$PID0" != "0" ]; gate $? "service reports a live MainPID before deploy"
PRE_JOBS=$(printf "SELECT count(*) FROM download_jobs;\n" | pgq)
PRE_SHARDS=$(printf "SELECT count(*) FROM download_shards;\n" | pgq)
PRE_CKPT=$(printf "SELECT count(*) FROM checkpoints;\n" | pgq)
PRE_OUT=$(printf "SELECT count(*) FROM outbox_events;\n" | pgq)
echo "pre jobs=$PRE_JOBS shards=$PRE_SHARDS checkpoints=$PRE_CKPT outbox=$PRE_OUT"

echo "=== P1 backup + install ==="
BAK="$ROOT/backend/bin/api.bak-s7v4-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$ROOT/backend/bin/api" "$BAK"; gate $? "backup old binary -> $BAK"
[ "$(sha256sum "$BAK" | cut -d' ' -f1)" = "$SHA0" ]; gate $? "backup sha equals pre-deploy running sha"
install -m 0755 "$NEW_BIN" "$ROOT/backend/bin/api"; gate $? "install new binary"
[ "$(sha256sum "$ROOT/backend/bin/api" | cut -d' ' -f1)" = "$EXPECT" ]; gate $? "installed file sha equals expected"

echo "=== P2 restart + bounded readiness wait (identity must not drift during the wait) ==="
svc_restart; gate $? "restart $SERVICE"
READY_OK=0; DRIFT=0; ATTEMPTS=0; CT=""; CODE=""
for i in $(seq 1 "$READY_TIMEOUT"); do
  ATTEMPTS=$i
  P=$(svc_pid)
  if [ -n "$P" ] && [ "$P" != "0" ]; then
    S=$(exe_sha "$P")
    E=$(exe_real "$P")
    if [ "$S" != "$EXPECT" ] || [ "$E" != "$EXPECT_EXE" ]; then DRIFT=$((DRIFT+1)); fi
    CT=$(curl -s -o "$TMPDIR_RUN/s7v4-ready.json" -w "%{content_type}" "$READY_URL" 2>/dev/null)
    CODE=$(curl -s -o "$TMPDIR_RUN/s7v4-ready.json" -w "%{http_code}" "$READY_URL" 2>/dev/null)
    if [ "$CODE" = "200" ]; then READY_OK=1; break; fi
  fi
  sleep 1
done
echo "ready_attempts=$ATTEMPTS ready_ok=$READY_OK drift_observations=$DRIFT content_type=$CT code=$CODE"
[ "$READY_OK" -eq 1 ]; gate $? "bounded readiness wait reached HTTP 200 on $READY_URL within ${READY_TIMEOUT}s"
[ "$DRIFT" -eq 0 ]; gate $? "no PID/exe-sha drift observed during readiness wait (drift=$DRIFT)"
PID1=$(svc_pid)
EXE1=$(exe_real "$PID1")
SHA1=$(exe_sha "$PID1")
echo "pid1=$PID1 exe1=$EXE1 sha1=$SHA1"
[ "$SHA1" = "$EXPECT" ]; gate $? "running exe sha equals expected (real running identity)"
[ "$EXE1" = "$EXPECT_EXE" ]; gate $? "running exe path is the deployed file ($EXPECT_EXE)"
[ "$PID1" != "$PID0" ]; gate $? "pid changed (real restart)"

echo "=== P3 readiness semantics (JSON contract, never a bare 200) ==="
echo "ready_body=$(cat "$TMPDIR_RUN/s7v4-ready.json" 2>/dev/null)"
case "$CT" in application/json*) ok "content-type is application/json ($CT)";; *) bad "content-type not JSON: $CT";; esac
python3 - "$TMPDIR_RUN/s7v4-ready.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as exc:
    print("PARSE_FAIL", exc); sys.exit(1)
checks = d.get("checks") or {}
need = ["postgres", "clickhouse", "nats", "minio"]
missing = [k for k in need if checks.get(k) != "ok"]
print("status=%s checks=%s missing_or_bad=%s" % (d.get("status"), checks, missing))
sys.exit(0 if d.get("status") == "ready" and not missing else 1)
PY
RC_READY=$?
[ "$RC_READY" -eq 0 ]; gate $? "readiness JSON: status=ready and postgres/clickhouse/nats/minio all ok"

echo "=== P4 live + auth + business + journal ==="
LIVE=$(curl -s -o /dev/null -w "%{http_code}" "$LIVE_URL" 2>/dev/null)
[ "$LIVE" = "200" ]; gate $? "health/live 200"
ANON=$(curl -s -o "$TMPDIR_RUN/s7v4-anon.json" -w "%{http_code}" http://127.0.0.1:8080/api/v1/download-jobs 2>/dev/null)
[ "$ANON" = "401" ]; gate $? "anonymous job list 401 (not 404)"
grep -q '"code":"unauthenticated"' "$TMPDIR_RUN/s7v4-anon.json" 2>/dev/null; gate $? "401 body carries unauthenticated code + envelope"
POST_JOBS=$(printf "SELECT count(*) FROM download_jobs;\n" | pgq)
POST_SHARDS=$(printf "SELECT count(*) FROM download_shards;\n" | pgq)
POST_CKPT=$(printf "SELECT count(*) FROM checkpoints;\n" | pgq)
POST_OUT=$(printf "SELECT count(*) FROM outbox_events;\n" | pgq)
echo "post jobs=$POST_JOBS shards=$POST_SHARDS checkpoints=$POST_CKPT outbox=$POST_OUT"
[ "$POST_JOBS" -ge "$PRE_JOBS" ]; gate $? "jobs not decreased"
[ "$POST_SHARDS" -ge "$PRE_SHARDS" ]; gate $? "shards not decreased"
[ "$POST_CKPT" -ge "$PRE_CKPT" ]; gate $? "checkpoints not decreased"
[ "$POST_OUT" -ge "$PRE_OUT" ]; gate $? "outbox not decreased"
PANIC=$(journal_count)
[ "$PANIC" -eq 0 ]; gate $? "no panic/fatal in service journal (count=$PANIC)"
WPID=$($SYSTEMCTL show -p MainPID --value "$WORKER_SERVICE" 2>/dev/null | tr -d '[:space:]')
WSHA=$(exe_sha "$WPID")
echo "worker_pid=$WPID worker_sha=$WSHA"
if [ -n "${DEPLOY_SKIP_WORKER_GATE:-}" ]; then ok "worker identity gate skipped by explicit DEPLOY_SKIP_WORKER_GATE"; else
  [ "$WSHA" = "$EXPECTED_WORKER_SHA" ]; gate $? "worker binary unchanged (worker must not be rebuilt this round)"
fi

echo "=== RESULT PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -eq 0 ] || exit 1
exit 0
