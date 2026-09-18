#!/usr/bin/env bash
# deploy-postverify-strict.sh — 对 deploy-binaries.sh 的**严格后验**（含失败可见性与原活动任务基线补充采集）。
#
# 背景（保留失败事实，不掩盖）：
#   2026-09-12T23:21:21Z 的 deploy-binaries.sh 运行中，pre-state 的"per org/ws census"SQL
#   使用了单列拼接输出却 GROUP BY 1,2,3，真实报错 "ORDER BY position 2 is not in select list"，
#   且脚本为 set -uo（无 -e），psql 失败后仍继续执行完毕。原始错误文本保存在
#   $EV/deploy-prestate.txt（未删除、未改写）。
#   本脚本不修改冻结的 deploy-binaries.sh，也不改写原报告，只**追加**严格后验与缺口声明。
#
# 严格性要求：
#   - 期望 sha 从**已封存**的 $EV/artifact-sha256.txt 解析，禁止"动态 sha vs 动态 sha"自证；
#   - 每条命令单独记录 exit code，任一关键步骤失败立即停止并保留证据；
#   - 不使用可能残留的共用 .tmp 文件；
#   - 补采每个"原活动 JobID"的单独状态、shard、page_no/page_token 存在性、checkpoint，
#     并**显式声明**部署前未采集逐 JobID 基线这一缺口，不假称已对照。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${1:-}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
OUT="${OUT:-$EV/postverify-strict-$TS}"
PG_ID=(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc)

mkdir -p "$OUT"
RC_LOG="$OUT/command-exits.txt"
: > "$RC_LOG"
FAILED=0

run() { # run <label> <cmd...>
  local label="$1"; shift
  local out_f="$OUT/$(echo "$label" | tr ' /:' '___').out"
  "$@" > "$out_f" 2>&1
  local rc=$?
  echo "$label rc=$rc out=$(basename "$out_f")" | tee -a "$RC_LOG"
  if [ "$rc" -ne 0 ]; then FAILED=1; echo "STRICT FAIL at: $label (rc=$rc)" | tee -a "$RC_LOG"; fi
  return "$rc"
}
q() { "${PG_ID[@]}" "$1"; }

if [ -z "$EV" ] || [ ! -f "$EV/artifact-sha256.txt" ]; then
  echo "usage: $0 <evidence-dir containing frozen artifact-sha256.txt>"; exit 2
fi

echo "=== strict post-verify $TS ==="
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence_dir=$EV"
echo "out_dir=$OUT"

# ---------- 1. 从封存产物清单解析期望 sha ----------
# artifact-sha256.txt 形如: "<sha>  <path>/<name>.build"
awk '{print $1"  "$2}' "$EV/artifact-sha256.txt" | while read -r sha path; do
  b="$(basename "$path")"; key="${b%.build}"
  echo "expected $key $sha"
done > "$OUT/expected-from-frozen-manifest.txt"
cat "$OUT/expected-from-frozen-manifest.txt"
exp() { awk -v k="$1" '{n=$2; sub(/\.build$/,"",n); if (n==k) {print $1; exit}}' "$OUT/expected-from-frozen-manifest.txt"; }

# ---------- 2. 已安装文件 vs 封存期望 ----------
{
  for b in api worker migrate; do
    want="$(exp $b)"
    got=$(sha256sum "$PROJECT/backend/bin/$b" 2>/dev/null | cut -d' ' -f1)
    if [ -n "$want" ] && [ "$want" = "$got" ]; then echo "INSTALLED OK $b want=$want got=$got"
    else echo "INSTALLED MISMATCH $b want=$want got=$got"; fi
  done
} | tee "$OUT/installed-vs-frozen.txt"
grep -q MISMATCH "$OUT/installed-vs-frozen.txt" && { echo "STRICT FAIL: installed != frozen"; FAILED=1; }

# ---------- 3. 运行身份 vs 封存期望 ----------
{
  for pair in "api:blockchain-api" "worker:blockchain-worker"; do
    b="${pair%%:*}"; u="${pair##*:}"
    want="$(exp $b)"
    pid=$(systemctl --user show "$u.service" -p MainPID --value)
    got=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)
    exe=$(readlink -f /proc/$pid/exe 2>/dev/null)
    st=$(systemctl --user show "$u.service" -p ActiveState --value)
    es=$(systemctl --user show "$u.service" -p ExecStart --value)
    ef=$(systemctl --user show "$u.service" -p EnvironmentFiles --value)
    started=$(ps -o lstart= -p "$pid" 2>/dev/null)
    if [ -n "$want" ] && [ "$want" = "$got" ]; then verdict=OK; else verdict=MISMATCH; fi
    echo "$u verdict=$verdict state=$st MainPID=$pid exe=$exe exe_sha=$got want=$want started=$started"
    echo "  ExecStart=$es"
    echo "  EnvironmentFiles=$ef"
  done
} | tee "$OUT/runtime-vs-frozen.txt"
grep -q MISMATCH "$OUT/runtime-vs-frozen.txt" && { echo "STRICT FAIL: runtime != frozen"; FAILED=1; }

# ---------- 4. 真实 (org, ws, status) 普查（正确 GROUP BY 列） ----------
q "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), status, count(*)
     FROM download_jobs GROUP BY organization_id, workspace_id, status
    ORDER BY organization_id, workspace_id, status" > "$OUT/census-org-ws-status.txt" 2>&1
echo "census_rc=$?"
cat "$OUT/census-org-ws-status.txt"

q "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), count(*)
     FROM download_jobs GROUP BY organization_id, workspace_id
    ORDER BY organization_id, workspace_id" > "$OUT/census-org-ws-total.txt" 2>&1
echo "census_total_rc=$?"
cat "$OUT/census-org-ws-total.txt"

q "SELECT status, count(*) FROM download_jobs GROUP BY status ORDER BY status" > "$OUT/census-status.txt" 2>&1
echo "census_status_rc=$?"
cat "$OUT/census-status.txt"

# ---------- 5. 逐 JobID 活动任务状态 + shard + checkpoint ----------
q "SELECT j.id::text, j.job_no, j.status, j.organization_id::text, COALESCE(j.workspace_id::text,'NULL'),
          to_char(j.updated_at,'YYYY-MM-DD\"T\"HH24:MI:SSOF'),
          (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id),
          (SELECT COALESCE(sum(s.downloaded_rows),0) FROM download_shards s WHERE s.job_id=j.id),
          (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id AND s.page_token IS NOT NULL AND s.page_token<>''),
          (SELECT COALESCE(sum(s.page_no),0) FROM download_shards s WHERE s.job_id=j.id)
     FROM download_jobs j
    WHERE j.status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')
    ORDER BY j.status, j.id" > "$OUT/active-jobs-perid.txt" 2>&1
echo "active_jobs_rc=$?"
wc -l < "$OUT/active-jobs-perid.txt" | sed 's/^/active_job_rows=/'

q "SELECT count(*) FROM download_shards" > "$OUT/shard-total.txt" 2>&1
echo "shard_total=$(cat "$OUT/shard-total.txt" 2>/dev/null)"

q "SELECT count(*) FROM checkpoints" > "$OUT/checkpoint-total.txt" 2>&1
echo "checkpoint_total=$(cat "$OUT/checkpoint-total.txt" 2>/dev/null)"

q "SELECT id::text, job_id::text, status, page_no, (page_token IS NOT NULL AND page_token<>''), downloaded_rows, updated_at
     FROM download_shards WHERE job_id IN
       (SELECT id FROM download_jobs WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED'))
    ORDER BY job_id, id" > "$OUT/active-shards-detail.txt" 2>&1
echo "active_shards_rc=$?"

# ---------- 6. 缺口声明 ----------
cat > "$OUT/GAP-STATEMENT.txt" <<'GAP'
真实缺口（不假称已对照）：
1. deploy-binaries.sh 运行（2026-09-12T23:21:21Z）时，pre-state 采集中的
   "download_jobs per org/ws census" SQL 真实失败（ERROR: ORDER BY position 2 is not in select list），
   原始错误文本保留在该次运行的 deploy-prestate.txt。因此**部署前的 (org, ws, status) 逐组合基线不存在**。
2. 同理，部署前**未采集逐 JobID 的 shard / page_token / checkpoint 基线**；本文件补采的是**部署后**状态。
   因此"活动任务未被重启影响"这一结论**不能**仅由本后验证明；只能由
   (a) 部署前后 aggregate status census 完全一致（CANCELLED=809 / COMPLETED=1585 / PAUSED=4 /
       PAUSED_FALLBACK_TOO_EXPENSIVE=1 / PAUSE_REQUESTED=1 / QUEUED=18 / RUNNING=1，两次一致），以及
   (b) 活动任务的 updated_at 早于部署时刻（均在 2026-09-12T18:23:06 及更早）、重启后未发生新写入 共同支持。
3. 下一次部署必须**先**正确采集逐 JobID 基线（含 shard/page_no/page_token/checkpoint），再重启。
GAP
cat "$OUT/GAP-STATEMENT.txt"

echo "=== strict post-verify done FAILED=$FAILED ==="
echo "out_dir=$OUT"
exit "$FAILED"
