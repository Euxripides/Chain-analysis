#!/usr/bin/env bash
# r10-deploy-api-v2.sh — R10 后端修复部署（第二轮：修 deploy v1 的 3 个 FAIL 成因）。
# v1（r10-deploy-api-strict.sh）的 14 PASS / 3 FAIL 原样保留为**失败轮**，不改称通过。
# v1 缺陷（根 11:00 复核确认）：restart 后只等 is-active 就立刻 curl，API 尚未 ready -> 3 FAIL。
# v2 更正：有界轮询真实 API readiness（http://127.0.0.1:8080/health/ready，直连 API 进程），
#          并在等待期间/之后重复确认 PID 与 /proc/<pid>/exe sha 未漂移；
#          校验 Content-Type=application/json + JSON 可解析 + status=ready + 四依赖全 ok，
#          **不接受 HTTP 200 本身**（8448 的 /health/* 落在 nginx location / -> Vite HTML，是假 ready）。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
EXPECT_SHA_FILE="$1"
NEW_BIN="$2"
READY_URL="${READY_URL:-http://127.0.0.1:8080/health/ready}"
READY_TIMEOUT="${READY_TIMEOUT:-60}"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
gate(){ if [ "$1" -eq 0 ]; then ok "$2"; else bad "$2"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }

EXPECT=$(tr -d '[:space:]' < "$EXPECT_SHA_FILE")
PID0=$(systemctl --user show -p MainPID --value blockchain-api.service)
SHA0=$(sha256sum /proc/$PID0/exe | cut -d' ' -f1)
EXE0=$(readlink -f /proc/$PID0/exe)
NEWSHA=$(sha256sum "$NEW_BIN" | cut -d' ' -f1)
echo "pid0=$PID0 exe0=$EXE0 sha0=$SHA0 new=$NEWSHA expect=$EXPECT"
[ "$NEWSHA" = "$EXPECT" ]; gate $? "new binary matches expected frozen sha"
PRE_JOBS=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)
PRE_SHARDS=$(printf "SELECT count(*) FROM download_shards;
" | pgq)
PRE_CKPT=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)
PRE_OUT=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)

echo "=== P1 backup + install ==="
BAK="$R/backend/bin/api.bak-r10v2-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$R/backend/bin/api" "$BAK"; gate $? "backup old binary -> $BAK"
[ "$(sha256sum "$BAK" | cut -d' ' -f1)" = "$SHA0" ]; gate $? "backup sha equals pre-deploy running sha"
install -m 0755 "$NEW_BIN" "$R/backend/bin/api"; gate $? "install new binary"
[ "$(sha256sum "$R/backend/bin/api" | cut -d' ' -f1)" = "$EXPECT" ]; gate $? "installed file sha equals expected"

echo "=== P2 restart + bounded readiness wait (during wait: PID/exe sha must not drift) ==="
systemctl --user restart blockchain-api.service; gate $? "systemctl restart blockchain-api"
READY_OK=0; DRIFT=0; ATTEMPTS=0; CT=""
for i in $(seq 1 "$READY_TIMEOUT"); do
  ATTEMPTS=$i
  P=$(systemctl --user show -p MainPID --value blockchain-api.service)
  if [ -n "$P" ] && [ "$P" != "0" ]; then
    S=$(sha256sum /proc/$P/exe 2>/dev/null | cut -d' ' -f1)
    E=$(readlink -f /proc/$P/exe 2>/dev/null)
    if [ "$S" != "$EXPECT" ] || [ "$E" != "$R/backend/bin/api" ]; then DRIFT=$((DRIFT+1)); fi
    CT=$(curl -s -o /tmp/r10v2-ready.json -w "%{content_type}" "$READY_URL" 2>/dev/null)
    CODE=$(curl -s -o /tmp/r10v2-ready.json -w "%{http_code}" "$READY_URL" 2>/dev/null)
    if [ "$CODE" = "200" ]; then READY_OK=1; break; fi
  fi
  sleep 1
done
echo "ready_attempts=$ATTEMPTS ready_ok=$READY_OK drift_observations=$DRIFT content_type=$CT"
[ "$READY_OK" -eq 1 ]; gate $? "bounded wait reached HTTP 200 on real API readiness ($READY_URL) within ${READY_TIMEOUT}s"
[ "$DRIFT" -eq 0 ]; gate $? "no PID/exe-sha drift observed during readiness wait (drift=$DRIFT)"
PID1=$(systemctl --user show -p MainPID --value blockchain-api.service)
EXE1=$(readlink -f /proc/$PID1/exe)
SHA1=$(sha256sum /proc/$PID1/exe | cut -d' ' -f1)
echo "pid1=$PID1 exe1=$EXE1 sha1=$SHA1"
[ "$SHA1" = "$EXPECT" ]; gate $? "/proc/<pid>/exe sha equals expected (真实运行身份)"
[ "$EXE1" = "$R/backend/bin/api" ]; gate $? "running exe path is backend/bin/api (not legacy/chainsight)"
[ "$PID1" != "$PID0" ]; gate $? "pid changed (真实重启)"

echo "=== P3 readiness semantics (必须 JSON + 字段，不接受裸 200) ==="
echo "ready_body=$(cat /tmp/r10v2-ready.json)"
case "$CT" in application/json*) ok "content-type is application/json ($CT)";; *) bad "content-type not JSON: $CT (8448 的 /health/* 会返回 Vite HTML)";; esac
python3 - <<'PY' > /tmp/r10v2-ready-verdict.txt 2>&1
import json, sys
try:
    d = json.load(open("/tmp/r10v2-ready.json", encoding="utf-8"))
except Exception as exc:
    print("PARSE_FAIL", exc); sys.exit(1)
checks = d.get("checks") or {}
need = ["postgres", "clickhouse", "nats", "minio"]
missing = [k for k in need if checks.get(k) != "ok"]
print("status=%s checks=%s missing_or_bad=%s" % (d.get("status"), checks, missing))
sys.exit(0 if d.get("status") == "ready" and not missing else 1)
PY
RC_READY=$?
cat /tmp/r10v2-ready-verdict.txt
[ "$RC_READY" -eq 0 ]; gate $? "readiness JSON: status=ready 且 postgres/clickhouse/nats/minio 全 ok"
LIVE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health/live)
[ "$LIVE" = "200" ]; gate $? "health/live 200 (direct API)"

echo "=== P4 auth + business + journal ==="
ANON=$(curl -s -o /tmp/r10v2-anon.json -w "%{http_code}" http://127.0.0.1:8080/api/v1/download-jobs)
[ "$ANON" = "401" ]; gate $? "anonymous job list 401 (not 404)"
grep -q '"code":"unauthenticated"' /tmp/r10v2-anon.json; gate $? "401 body carries unauthenticated code + envelope"
POST_JOBS=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)
POST_SHARDS=$(printf "SELECT count(*) FROM download_shards;
" | pgq)
POST_CKPT=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)
POST_OUT=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)
echo "pre jobs=$PRE_JOBS shards=$PRE_SHARDS ckpt=$PRE_CKPT outbox=$PRE_OUT / post jobs=$POST_JOBS shards=$POST_SHARDS ckpt=$POST_CKPT outbox=$POST_OUT"
[ "$POST_JOBS" -ge "$PRE_JOBS" ]; gate $? "jobs not decreased"
[ "$POST_SHARDS" -ge "$PRE_SHARDS" ]; gate $? "shards not decreased"
[ "$POST_CKPT" -ge "$PRE_CKPT" ]; gate $? "checkpoints not decreased"
[ "$POST_OUT" -ge "$PRE_OUT" ]; gate $? "outbox not decreased"
PANIC=$(journalctl --user -u blockchain-api.service --since "-120s" --no-pager 2>/dev/null | grep -Eic "panic|fatal" || true)
[ "$PANIC" -eq 0 ]; gate $? "no panic/fatal in api journal (count=$PANIC)"
WORKER_PID=$(systemctl --user show -p MainPID --value blockchain-worker.service)
WORKER_SHA=$(sha256sum /proc/$WORKER_PID/exe | cut -d' ' -f1)
echo "worker_pid=$WORKER_PID worker_sha=$WORKER_SHA"
[ "$WORKER_SHA" = "513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48" ]; gate $? "worker binary unchanged (not rebuilt this round)"

echo "=== RESULT PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -eq 0 ] || exit 1
exit 0
