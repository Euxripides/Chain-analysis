#!/usr/bin/env bash
# r10-deploy-api-strict.sh — R10 后端修复（requireExportScope job_id + 复用响应契约）的受控部署。
# 冻结后不再修改。逐步 rc 显式 gate；任一步失败立即非零退出，不产生假 PASS。
# 对照 deploy-binaries.sh 的已知缺陷（census GROUP BY 作用于拼接聚合、只有 set -u 不 gate cp/mv/restart）。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
EXPECT_SHA_FILE="$1"
NEW_BIN="$2"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
gate(){ if [ "$1" -eq 0 ]; then ok "$2"; else bad "$2"; fi; }

echo "=== P0 pre-state ==="
PID0=$(systemctl --user show -p MainPID --value blockchain-api.service)
SHA0=$(sha256sum /proc/$PID0/exe 2>/dev/null | cut -d' ' -f1)
EXPECT=$(cat "$EXPECT_SHA_FILE" | tr -d '[:space:]')
NEWSHA=$(sha256sum "$NEW_BIN" | cut -d' ' -f1)
echo "pid0=$PID0 deployed_sha0=$SHA0 new_bin_sha=$NEWSHA expected=$EXPECT"
[ "$NEWSHA" = "$EXPECT" ]; gate $? "new binary matches expected frozen sha"
[ "$SHA0" = "$NEWSHA" ]; if [ $? -eq 0 ]; then bad "new binary identical to running binary (no-op deploy?)"; else ok "new binary differs from running binary"; fi
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
PRE_JOBS=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)
PRE_SHARDS=$(printf "SELECT count(*) FROM download_shards;
" | pgq)
PRE_CKPT=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)
PRE_OUT=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)
echo "pre jobs=$PRE_JOBS shards=$PRE_SHARDS checkpoints=$PRE_CKPT outbox=$PRE_OUT"

echo "=== P1 backup + replace ==="
BAK="$R/backend/bin/api.bak-r10-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$R/backend/bin/api" "$BAK"; gate $? "backup old binary -> $BAK"
[ "$(sha256sum "$BAK" | cut -d' ' -f1)" = "$SHA0" ]; gate $? "backup sha equals pre-deploy running sha"
install -m 0755 "$NEW_BIN" "$R/backend/bin/api"; gate $? "install new binary"
[ "$(sha256sum "$R/backend/bin/api" | cut -d' ' -f1)" = "$EXPECT" ]; gate $? "installed file sha equals expected"

echo "=== P2 restart + running identity ==="
systemctl --user restart blockchain-api.service; gate $? "systemctl restart blockchain-api"
for i in $(seq 1 30); do
  systemctl --user is-active --quiet blockchain-api.service && break; sleep 1
done
PID1=$(systemctl --user show -p MainPID --value blockchain-api.service)
SHA1=$(sha256sum /proc/$PID1/exe 2>/dev/null | cut -d' ' -f1)
echo "pid1=$PID1 running_sha=$SHA1"
[ "$SHA1" = "$EXPECT" ]; gate $? "/proc/<pid>/exe sha equals expected (真实运行身份)"
[ "$PID1" != "$PID0" ]; gate $? "pid changed (真实重启)"

echo "=== P3 health + auth + business ==="
READY=$(curl -s -o /tmp/r10-ready.json -w "%{http_code}" http://127.0.0.1:8080/health/ready)
[ "$READY" = "200" ]; gate $? "health/ready 200"
grep -q '"postgres":"ok"' /tmp/r10-ready.json && grep -q '"nats":"ok"' /tmp/r10-ready.json && grep -q '"minio":"ok"' /tmp/r10-ready.json; gate $? "postgres/nats/minio all ok"
ANON=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/api/v1/download-jobs)
[ "$ANON" = "401" ]; gate $? "anonymous job list 401 (not 404)"
POST_JOBS=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)
POST_SHARDS=$(printf "SELECT count(*) FROM download_shards;
" | pgq)
POST_CKPT=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)
POST_OUT=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)
echo "post jobs=$POST_JOBS shards=$POST_SHARDS checkpoints=$POST_CKPT outbox=$POST_OUT"
[ "$POST_JOBS" -ge "$PRE_JOBS" ]; gate $? "jobs not decreased"
[ "$POST_SHARDS" -ge "$PRE_SHARDS" ]; gate $? "shards not decreased"
[ "$POST_CKPT" -ge "$PRE_CKPT" ]; gate $? "checkpoints not decreased"
[ "$POST_OUT" -ge "$PRE_OUT" ]; gate $? "outbox not decreased"
journalctl --user -u blockchain-api.service --since "-90s" --no-pager 2>/dev/null | grep -Eic "panic|fatal" > /tmp/r10-panic.txt || true
PANIC=$(cat /tmp/r10-panic.txt)
[ "$PANIC" -eq 0 ]; gate $? "no panic/fatal in api journal (count=$PANIC)"
echo "worker_unchanged_sha=$(sha256sum /proc/$(systemctl --user show -p MainPID --value blockchain-worker.service)/exe | cut -d' ' -f1)"

echo "=== RESULT PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -eq 0 ] || exit 1
exit 0
