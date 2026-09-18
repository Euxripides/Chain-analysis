#!/usr/bin/env bash
# deploy-postverify-strict-v2.sh — 严格后验（修正 v1 的期望-manifest 解析缺陷）。
#
# v1（deploy-postverify-strict.sh, sha256 623d3b5b923a9d7e3f8951dc98c46740e77fee8d228eac22dfd1a3471f7938b4）
# 的真实缺陷：生成 expected 文件时写成 "expected <name> <sha>"，而 exp() 读取 $2 作为名字、$1 作为 sha，
# 于是 want 恒为字符串 "expected"，产生 **假 MISMATCH**。
# v1 未修改、其输出 postverify-strict-20260912T232430Z/ 原样保留；本脚本是其修正版（新文件，非热改）。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${1:-}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
OUT="${OUT:-$EV/postverify-strict-v2-$TS}"
PSQL=(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc)

mkdir -p "$OUT"
FAILED=0
q() { "${PSQL[@]}" "$1"; }

if [ -z "$EV" ] || [ ! -f "$EV/artifact-sha256.txt" ]; then
  echo "usage: $0 <evidence-dir containing frozen artifact-sha256.txt>"; exit 2
fi

echo "=== strict post-verify v2 $TS ==="
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence_dir=$EV"
echo "out_dir=$OUT"

# ---------- 1. 从封存产物清单解析期望 sha（格式: "<sha>  <name>"） ----------
: > "$OUT/expected-from-frozen-manifest.txt"
while read -r sha path; do
  [ -z "$sha" ] && continue
  b="$(basename "$path")"; b="${b%.build}"
  echo "$sha  $b" >> "$OUT/expected-from-frozen-manifest.txt"
done < "$EV/artifact-sha256.txt"
cat "$OUT/expected-from-frozen-manifest.txt"
exp() { awk -v k="$1" '$2==k {print $1; exit}' "$OUT/expected-from-frozen-manifest.txt"; }

# ---------- 2. 已安装文件 vs 封存期望 ----------
for b in api worker migrate; do
  want="$(exp "$b")"
  got="$(sha256sum "$PROJECT/backend/bin/$b" 2>/dev/null | cut -d' ' -f1)"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then echo "INSTALLED OK $b $got"
  else echo "INSTALLED MISMATCH $b want=$want got=$got"; FAILED=1; fi
done | tee "$OUT/installed-vs-frozen.txt"

# ---------- 3. 运行身份 vs 封存期望 ----------
for pair in "api:blockchain-api" "worker:blockchain-worker"; do
  b="${pair%%:*}"; u="${pair##*:}"
  want="$(exp "$b")"
  pid="$(systemctl --user show "$u.service" -p MainPID --value)"
  got="$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)"
  exe="$(readlink -f /proc/$pid/exe 2>/dev/null)"
  st="$(systemctl --user show "$u.service" -p ActiveState --value)"
  started="$(ps -o lstart= -p "$pid" 2>/dev/null)"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then verdict=OK; else verdict=MISMATCH; FAILED=1; fi
  echo "$u verdict=$verdict state=$st MainPID=$pid exe=$exe exe_sha=$got want=$want started=$started"
  echo "  ExecStart=$(systemctl --user show "$u.service" -p ExecStart --value)"
  echo "  EnvironmentFiles=$(systemctl --user show "$u.service" -p EnvironmentFiles --value)"
done | tee "$OUT/runtime-vs-frozen.txt"

# ---------- 4. 真实 (org, ws, status) 普查 ----------
q "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), status, count(*)
     FROM download_jobs GROUP BY organization_id, workspace_id, status
    ORDER BY organization_id, workspace_id, status" > "$OUT/census-org-ws-status.txt" 2>&1
rc=$?; echo "census_rc=$rc"; [ "$rc" -ne 0 ] && FAILED=1
cat "$OUT/census-org-ws-status.txt"

q "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), count(*)
     FROM download_jobs GROUP BY organization_id, workspace_id
    ORDER BY organization_id, workspace_id" > "$OUT/census-org-ws-total.txt" 2>&1
rc=$?; echo "census_total_rc=$rc"; [ "$rc" -ne 0 ] && FAILED=1
cat "$OUT/census-org-ws-total.txt"

q "SELECT status, count(*) FROM download_jobs GROUP BY status ORDER BY status" > "$OUT/census-status.txt" 2>&1
rc=$?; echo "census_status_rc=$rc"; [ "$rc" -ne 0 ] && FAILED=1
cat "$OUT/census-status.txt"

# ---------- 5. 逐 JobID 活动任务 + shard + checkpoint ----------
q "SELECT j.id::text, j.job_no, j.status, j.organization_id::text, COALESCE(j.workspace_id::text,'NULL'),
          to_char(j.updated_at,'YYYY-MM-DD\"T\"HH24:MI:SSOF'),
          (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id),
          (SELECT COALESCE(sum(s.downloaded_rows),0) FROM download_shards s WHERE s.job_id=j.id),
          (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id AND s.page_token IS NOT NULL AND s.page_token<>''),
          (SELECT COALESCE(sum(s.page_no),0) FROM download_shards s WHERE s.job_id=j.id)
     FROM download_jobs j
    WHERE j.status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')
    ORDER BY j.status, j.id" > "$OUT/active-jobs-perid.txt" 2>&1
rc=$?; echo "active_jobs_rc=$rc"; [ "$rc" -ne 0 ] && FAILED=1
echo "active_job_rows=$(wc -l < "$OUT/active-jobs-perid.txt")"

q "SELECT id::text, job_id::text, status, page_no, (page_token IS NOT NULL AND page_token<>''), downloaded_rows, updated_at
     FROM download_shards WHERE job_id IN
       (SELECT id FROM download_jobs WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED'))
    ORDER BY job_id, id" > "$OUT/active-shards-detail.txt" 2>&1
rc=$?; echo "active_shards_rc=$rc"; [ "$rc" -ne 0 ] && FAILED=1

for t in download_jobs download_shards checkpoints provider_usage audit_events_v2; do
  n="$(q "SELECT count(*) FROM $t" 2>&1)"; rc=$?
  echo "$t count=$n rc=$rc"
  [ "$rc" -ne 0 ] && FAILED=1
done | tee "$OUT/table-counts.txt"

cp -f "$EV/deploy-prestate.txt" "$OUT/original-prestate-with-real-sql-error.txt" 2>/dev/null

echo "=== strict post-verify v2 done FAILED=$FAILED ==="
echo "out_dir=$OUT"
exit "$FAILED"
