#!/usr/bin/env bash
# r9-deploy-strict-v2.sh — 受控部署（新文件；不修改任何已运行脚本）。
#
# 为什么不直接用 scripts/release/deploy-binaries.sh：该脚本虽已冻结，但按 0750 权威交接，
# 它的 census SQL（GROUP BY 1,2,3 作用在单一拼接聚合上）是错的、且只在 set -u 下运行，
# cp/mv/restart 的失败不会 gate，可能产生假成功。冻结只保证原失败可追溯，不等于可靠路径。
#
# 本脚本：
#   - 期望 sha 只从本轮冻结的 artifact-sha256.txt 读取，脚本内不硬编码任何产物 sha；
#   - 旧二进制先备份并把备份 sha 与部署前 sha 对比；
#   - 每一步 rc 显式 gate（cp / chmod / mv / systemctl restart 全部检查）；
#   - 运行身份以 /proc/<pid>/exe 的 sha256 为准，不接受“文件相同”；
#   - 数据库计数用 docker exec + SQL on stdin（argv 里不出现任何口令/DSN），表名用真实表；
#   - 前后各做一次业务普查并按列比较，用户任务不得丢失；
#   - 新路由存在性用匿名 401（而不是 404）证明，并做 404 负向对照；
#   - FAIL>0 时 exit 1；OUT 已存在则 fail-closed。
set -uo pipefail
export XDG_RUNTIME_DIR=/run/user/$(id -u)

if [ $# -lt 2 ]; then echo "usage: $0 <frozen-build-dir> <new-out-dir>"; exit 2; fi
BUILD="$1"
OUT="$2"
if [ ! -f "$BUILD/artifact-sha256.txt" ]; then echo "missing $BUILD/artifact-sha256.txt"; exit 2; fi
if [ -e "$OUT" ]; then echo "OUT_EXISTS_FAILCLOSED $OUT"; exit 9; fi
mkdir -p "$OUT" || exit 8

R=/home/euripides/projects/blockchain-platform
BIND="$R/backend/bin"
TS=$(date -u +%Y%m%dT%H%M%SZ)
FAIL=0; PASS=0
ok(){ PASS=$((PASS+1)); printf 'OK   %s\n' "$1"; }
bad(){ FAIL=$((FAIL+1)); printf 'FAIL %s\n' "$1"; }
say(){ printf '%s\n' "$*" | tee -a "$OUT/deploy.log"; }
exp(){ awk -v k="$1" 'index($2, "/" k ".build") > 0 || $2 == k {print $1; exit}' "$BUILD/artifact-sha256.txt"; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -tA; }
census(){ pgq <<'SQL'
SET ROLE blockchain_app;
SELECT 'jobs|'||status||'|'||count(*) FROM download_jobs GROUP BY status ORDER BY 1;
SQL
}
census_scope(){ pgq <<'SQL'
SET ROLE blockchain_app;
SELECT 'scope|'||organization_id::text||'|'||coalesce(workspace_id::text,'NULL')||'|'||status||'|'||count(*) FROM download_jobs GROUP BY organization_id, workspace_id, status ORDER BY 1;
SQL
}

say "=== r9 DEPLOY STRICT v2 START $(date -u +%FT%TZ) BUILD=$BUILD OUT=$OUT ==="
say "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"

say "--- P0 pre-state ---"
{ for u in blockchain-api blockchain-worker blockchain-mcp; do
    echo "$u active=$(systemctl --user is-active $u.service) MainPID=$(systemctl --user show $u.service -p MainPID --value)";
  done; } | tee "$OUT/p0-units.txt"
OLD_API_PID=$(systemctl --user show blockchain-api.service -p MainPID --value)
OLD_WORKER_PID=$(systemctl --user show blockchain-worker.service -p MainPID --value)
sha256sum "$BIND/api" "$BIND/worker" "$BIND/migrate" | tee "$OUT/p0-bin-sha.txt"
PRE_API_SHA=$(sha256sum "$BIND/api" | cut -d' ' -f1)
PRE_WORKER_SHA=$(sha256sum "$BIND/worker" | cut -d' ' -f1)
PRE_MIGRATE_SHA=$(sha256sum "$BIND/migrate" | cut -d' ' -f1)
census > "$OUT/p0-census-status.txt" 2>"$OUT/p0-census-status.err"; rc=$?
if [ $rc -eq 0 ] && [ -s "$OUT/p0-census-status.txt" ]; then ok "P0 job census read rc=0"; else bad "P0 job census rc=$rc"; fi
census_scope > "$OUT/p0-census-scope.txt" 2>&1
say "p0 census: $(tr '\n' ' ' < "$OUT/p0-census-status.txt")"
curl -s --max-time 15 http://127.0.0.1:8080/health/ready > "$OUT/p0-health-ready.json"
say "p0 ready body=$(head -c 200 "$OUT/p0-health-ready.json")"
curl -s -o /dev/null -w '%{http_code}' --max-time 15 http://127.0.0.1:8080/api/v1/download-jobs > "$OUT/p0-anon-jobs-code.txt"
if [ "$(cat "$OUT/p0-anon-jobs-code.txt")" = "401" ]; then ok "P0 anon /api/v1/download-jobs 401"; else bad "P0 anon code=$(cat "$OUT/p0-anon-jobs-code.txt")"; fi

say "--- P1 staged artifacts ---"
for b in api worker migrate; do
  want=$(exp "$b"); f="$BUILD/$b.build"
  if [ -z "$want" ]; then bad "P1 $b: no expected sha in frozen manifest"; continue; fi
  if [ ! -s "$f" ]; then bad "P1 $b.build missing"; continue; fi
  got=$(sha256sum "$f" | cut -d' ' -f1)
  if [ "$got" = "$want" ]; then ok "P1 $b.build sha256=$got matches frozen manifest"; else bad "P1 $b.build sha=$got want=$want"; fi
done
if [ "$FAIL" -gt 0 ]; then say "=== ABORT before install (P1 failed) ==="; echo "$FAIL" > "$OUT/deploy.failcount"; exit 1; fi

say "--- P2 preserve + install ---"
for b in api worker migrate; do
  want=$(exp "$b")
  if ! cp -p "$BIND/$b" "$BIND/$b.bak-r9-$TS"; then bad "P2 backup cp failed for $b"; continue; fi
  bsha=$(sha256sum "$BIND/$b.bak-r9-$TS" | cut -d' ' -f1)
  case "$b" in api) pre="$PRE_API_SHA";; worker) pre="$PRE_WORKER_SHA";; migrate) pre="$PRE_MIGRATE_SHA";; esac
  if [ "$bsha" = "$pre" ]; then ok "P2 $b backup == pre-deploy sha ($bsha)"; else bad "P2 $b backup sha=$bsha pre=$pre"; fi
  if ! cp "$BUILD/$b.build" "$BIND/$b.tmp-r9-$$"; then bad "P2 stage cp failed for $b"; continue; fi
  if ! chmod 755 "$BIND/$b.tmp-r9-$$"; then bad "P2 chmod failed for $b"; continue; fi
  if ! mv -f "$BIND/$b.tmp-r9-$$" "$BIND/$b"; then bad "P2 atomic mv failed for $b"; continue; fi
  got=$(sha256sum "$BIND/$b" | cut -d' ' -f1)
  if [ "$got" = "$want" ]; then ok "P2 installed $b sha256=$got"; else bad "P2 installed $b sha=$got want=$want"; fi
done
sha256sum "$BIND"/api.bak-r9-$TS "$BIND"/worker.bak-r9-$TS "$BIND"/migrate.bak-r9-$TS > "$OUT/p2-preserved-old-sha.txt" 2>&1
say "preserved old sha: $(tr '\n' ' ' < "$OUT/p2-preserved-old-sha.txt")"

say "--- P3 restart ---"
systemctl --user restart blockchain-api.service; rca=$?
systemctl --user restart blockchain-worker.service; rcw=$?
if [ $rca -eq 0 ]; then ok "P3 restart api rc=0"; else bad "P3 restart api rc=$rca"; fi
if [ $rcw -eq 0 ]; then ok "P3 restart worker rc=0"; else bad "P3 restart worker rc=$rcw"; fi
for i in $(seq 1 30); do
  a=$(systemctl --user is-active blockchain-api.service); w=$(systemctl --user is-active blockchain-worker.service)
  if [ "$a" = "active" ] && [ "$w" = "active" ]; then break; fi
  sleep 1
done
NEW_API_PID=$(systemctl --user show blockchain-api.service -p MainPID --value)
NEW_WORKER_PID=$(systemctl --user show blockchain-worker.service -p MainPID --value)
say "p3 new api pid=$NEW_API_PID worker pid=$NEW_WORKER_PID"
if [ "$NEW_API_PID" != "$OLD_API_PID" ] && [ "$NEW_API_PID" != "0" ]; then ok "P3 api MainPID changed $OLD_API_PID -> $NEW_API_PID"; else bad "P3 api MainPID unchanged ($NEW_API_PID)"; fi
if [ "$NEW_WORKER_PID" != "$OLD_WORKER_PID" ] && [ "$NEW_WORKER_PID" != "0" ]; then ok "P3 worker MainPID changed $OLD_WORKER_PID -> $NEW_WORKER_PID"; else bad "P3 worker MainPID unchanged ($NEW_WORKER_PID)"; fi
: > "$OUT/p3-running-identity.txt"
for pair in "api:$NEW_API_PID" "worker:$NEW_WORKER_PID"; do
  n=$(printf '%s' "$pair" | cut -d: -f1); pid=$(printf '%s' "$pair" | cut -d: -f2)
  want=$(exp "$n")
  exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null)
  got=$(sha256sum "$exe" 2>/dev/null | cut -d' ' -f1)
  if [ "$got" = "$want" ]; then ok "P3 running $n /proc/$pid/exe sha=$got"; else bad "P3 running $n sha=$got want=$want exe=$exe"; fi
  echo "$n pid=$pid exe=$exe sha=$got want=$want" >> "$OUT/p3-running-identity.txt"
done

say "--- P4 post health ---"
code=000
for i in $(seq 1 20); do
  code=$(curl -s --max-time 10 -o "$OUT/p4-health-ready.json" -w '%{http_code}' http://127.0.0.1:8080/health/ready)
  if [ "$code" = "200" ]; then break; fi
  sleep 2
done
echo "$code" > "$OUT/p4-health-ready-code.txt"
if [ "$code" = "200" ]; then ok "P4 /health/ready 200 (polled)"; else bad "P4 /health/ready $code"; fi
say "p4 ready body=$(head -c 300 "$OUT/p4-health-ready.json")"
curl -s --max-time 20 -o "$OUT/p4-health-live.json" -w '%{http_code}' http://127.0.0.1:8080/health/live > "$OUT/p4-health-live-code.txt"
if [ "$(cat "$OUT/p4-health-live-code.txt")" = "200" ]; then ok "P4 /health/live 200"; else bad "P4 /health/live $(cat "$OUT/p4-health-live-code.txt")"; fi

say "--- P5 auth boundary + new route presence ---"
curl -s -o "$OUT/p5-anon-jobs-body.json" -D "$OUT/p5-anon-jobs-hdr.txt" -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/api/v1/download-jobs > "$OUT/p5-anon-jobs-code.txt"
if [ "$(cat "$OUT/p5-anon-jobs-code.txt")" = "401" ]; then ok "P5 anon /api/v1/download-jobs 401"; else bad "P5 anon jobs code=$(cat "$OUT/p5-anon-jobs-code.txt")"; fi
grep -iE '^(x-request-id|x-trace-id)' "$OUT/p5-anon-jobs-hdr.txt" | tr -d '\r' > "$OUT/p5-anon-ids.txt" || true
if [ -s "$OUT/p5-anon-ids.txt" ]; then ok "P5 request/trace ids present"; else bad "P5 no request/trace ids"; fi
newcode=$(curl -s -o "$OUT/p5-validate-anon-body.json" -D "$OUT/p5-validate-anon-hdr.txt" -w '%{http_code}' --max-time 20 -H 'Content-Type: application/json' -d '{"chain":"bsc","items":[{"address":"0x0000000000000000000000000000000000000001"}]}' http://127.0.0.1:8080/api/v2/downloads/validate-addresses)
echo "$newcode" > "$OUT/p5-validate-anon-code.txt"
if [ "$newcode" = "401" ]; then ok "P5 new /api/v2/downloads/validate-addresses present and 401 for anon (not 404)"; else bad "P5 new route code=$newcode (404 means the route did not ship)"; fi
neg=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8080/api/v2/downloads/validate-addresses-does-not-exist)
echo "$neg" > "$OUT/p5-validate-negative-code.txt"
if [ "$neg" = "404" ]; then ok "P5 negative control: nonexistent v2 route is 404"; else bad "P5 negative control code=$neg expected 404"; fi

say "--- P6 business continuity ---"
pgq > "$OUT/p6-db-business.txt" 2>"$OUT/p6-db-business.err" <<'SQL'
SET ROLE blockchain_app;
SELECT 'jobs='||count(*) FROM download_jobs;
SELECT 'shards='||count(*) FROM download_shards;
SELECT 'checkpoints='||count(*) FROM checkpoints;
SELECT 'outbox_events='||count(*) FROM outbox_events;
SQL
rc=$?
cat "$OUT/p6-db-business.txt" | tee -a "$OUT/deploy.log"
if [ $rc -eq 0 ]; then ok "P6 business counts rc=0 (blockchain_app role, no DSN in argv)"; else bad "P6 business query rc=$rc: $(head -2 "$OUT/p6-db-business.err")"; fi
census > "$OUT/p6-census-status.txt" 2>&1
census_scope > "$OUT/p6-census-scope.txt" 2>&1
say "p6 census: $(tr '\n' ' ' < "$OUT/p6-census-status.txt")"
if diff -q "$OUT/p0-census-status.txt" "$OUT/p6-census-status.txt" >/dev/null 2>&1; then
  ok "P6 job census identical to pre-deploy"
else
  say "P6 NOTE census changed (legitimate job progress allowed; recorded verbatim)"
  diff "$OUT/p0-census-status.txt" "$OUT/p6-census-status.txt" > "$OUT/p6-census-status.diff" 2>&1 || true
  cat "$OUT/p6-census-status.diff" | tee -a "$OUT/deploy.log"
  ok "P6 census diff recorded verbatim (no silent normalization)"
fi
pre_total=$(awk -F'|' '{s+=$3} END{print s+0}' "$OUT/p0-census-status.txt")
post_total=$(awk -F'|' '{s+=$3} END{print s+0}' "$OUT/p6-census-status.txt")
if [ "$post_total" -ge "$pre_total" ]; then ok "P6 total jobs $pre_total -> $post_total (no loss)"; else bad "P6 total jobs decreased $pre_total -> $post_total"; fi
: > "$OUT/p6-table-counts.txt"
for t in download_jobs download_shards checkpoints; do
  cnt=$(printf 'SET ROLE blockchain_app;\nSELECT count(*) FROM %s;\n' "$t" | pgq)
  echo "$t=$cnt" >> "$OUT/p6-table-counts.txt"
done
say "p6 table counts: $(tr '\n' ' ' < "$OUT/p6-table-counts.txt")"

say "--- P7 journal ---"
journalctl --user -u blockchain-api.service --since "$(date -u -d '-6 min' +%FT%TZ)" --no-pager > "$OUT/p7-api-journal.txt" 2>&1 || true
journalctl --user -u blockchain-worker.service --since "$(date -u -d '-6 min' +%FT%TZ)" --no-pager > "$OUT/p7-worker-journal.txt" 2>&1 || true
for f in p7-api-journal p7-worker-journal; do
  n=0
  n=$(grep -ciE 'panic|fatal|password authentication failed' "$OUT/$f.txt" || true)
  if [ "$n" -eq 0 ]; then ok "P7 $f clean of panic/fatal/auth-failure"; else bad "P7 $f has $n panic/fatal/auth-failure lines"; fi
done

say "=== r9 DEPLOY STRICT v2 END $(date -u +%FT%TZ) FAIL=$FAIL PASS=$PASS ==="
echo "$FAIL" > "$OUT/deploy.failcount"
echo "$PASS" > "$OUT/deploy.passcount"
if [ "$FAIL" -gt 0 ]; then exit 1; fi
exit 0
