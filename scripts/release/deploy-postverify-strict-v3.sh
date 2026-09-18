#!/usr/bin/env bash
# deploy-postverify-strict-v3.sh — 严格后验（修正 v1/v2 的真实控制流缺陷，并加入**负向校准**）。
#
# 保留的失败事实（不热改、不改写原报告）：
#   v1 sha256 623d3b5b923a9d7e3f8951dc98c46740e77fee8d228eac22dfd1a3471f7938b4
#      缺陷：expected 清单写成 "expected <name> <sha>"，exp() 取 $1 作 sha → want 恒为字符串 "expected" → **假 MISMATCH**。
#      其输出 postverify-strict-20260912T232430Z/ 原样保留。
#   v2 sha256 acf484ac6b1a42c5ef7a5310c9140608e01eb62bb2115732d0d0a7d02f49cda2
#      缺陷：三处 for 循环写成 "done | tee"，循环体在 pipeline 子 shell 中执行，
#      FAILED=1 不回传主 shell → **MISMATCH 或 SQL 失败仍可能 exit 0（假 PASS 风险）**；
#      runtime 检查只比 sha，未把 ActiveState/SubState/MainPID==0 作为失败。
#      其输出 postverify-strict-v2-20260912T232446Z/ 原样保留（该次每行恰为真 OK，但其 FAILED=0 不能证明 guard 正确）。
#
# v3 关键修正：
#   1. 所有循环在**主 shell** 中执行（brace group / for 重定向到文件，再 cat），FAILED 正确聚合；
#   2. 期望 sha 必须**非空且唯一**，否则直接失败；实际文件必须存在且可读；
#   3. runtime 门槛：ActiveState=active 且 SubState=running 且 MainPID 为 >0 整数 且
#      /proc/<pid>/exe 指向期望路径 且 exe sha == 期望；
#   4. 所有 SQL 单独检查 rc；
#   5. **负向校准**：用一份私有 manifest 副本（api 期望 sha 故意改错）跑同一脚本，
#      必须真实非 0 且出现对应 MISMATCH；不修改任何生产文件/服务。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${1:-}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
MODE="${MODE:-positive}"          # positive | negative-calibration
EXPECT_FAIL="${EXPECT_FAIL:-0}"   # 1 = 本模式期望脚本失败
OUT="${OUT:-$EV/postverify-strict-v3-$MODE-$TS}"
PSQL=(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc)

mkdir -p "$OUT"
FAILED=0
say() { echo "$*" >> "$OUT/RUN.log"; echo "$*"; }
q() { "${PSQL[@]}" "$1"; }
sect() { echo "" >> "$OUT/RUN.log"; echo "### $*" >> "$OUT/RUN.log"; }

if [ -z "$EV" ] || [ ! -f "$EV/artifact-sha256.txt" ]; then
  echo "usage: $0 <evidence-dir containing frozen artifact-sha256.txt>"; exit 2
fi

say "=== strict post-verify v3 mode=$MODE expect_fail=$EXPECT_FAIL ts=$TS ==="
say "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
say "evidence_dir=$EV"
say "out_dir=$OUT"

# ---------- A. 期望清单：非空 + 唯一 ----------
sect "A expected manifest"
: > "$OUT/expected-from-frozen-manifest.txt"
while read -r sha path; do
  [ -z "$sha" ] && continue
  b="$(basename "$path")"; b="${b%.build}"
  echo "$sha  $b" >> "$OUT/expected-from-frozen-manifest.txt"
done < "$EV/artifact-sha256.txt"
cat "$OUT/expected-from-frozen-manifest.txt" >> "$OUT/RUN.log"

exp() { awk -v k="$1" '$2==k {print $1; exit}' "$OUT/expected-from-frozen-manifest.txt"; }
for b in api worker migrate; do
  w="$(exp "$b")"
  n="$(grep -c " $b$" "$OUT/expected-from-frozen-manifest.txt" 2>/dev/null || echo 0)"
  if [ -z "$w" ]; then say "EXPECTED MISSING $b"; FAILED=1; fi
  if [ "$n" != "1" ]; then say "EXPECTED NOT-UNIQUE $b count=$n"; FAILED=1; fi
done

# ---------- B. 已安装文件 vs 封存期望（主 shell 循环） ----------
sect "B installed vs frozen"
: > "$OUT/installed-vs-frozen.txt"
for b in api worker migrate; do
  want="$(exp "$b")"; f="$PROJECT/backend/bin/$b"
  if [ ! -r "$f" ]; then echo "INSTALLED MISSING $b path=$f" >> "$OUT/installed-vs-frozen.txt"; FAILED=1; continue; fi
  got="$(sha256sum "$f" 2>/dev/null | cut -d' ' -f1)"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then
    echo "INSTALLED OK $b $got" >> "$OUT/installed-vs-frozen.txt"
  else
    echo "INSTALLED MISMATCH $b want=$want got=$got" >> "$OUT/installed-vs-frozen.txt"; FAILED=1
  fi
done
cat "$OUT/installed-vs-frozen.txt" >> "$OUT/RUN.log"

# ---------- C. 运行身份 vs 封存期望（主 shell 循环，含状态门槛） ----------
sect "C runtime vs frozen"
: > "$OUT/runtime-vs-frozen.txt"
for pair in "api:blockchain-api" "worker:blockchain-worker"; do
  b="${pair%%:*}"; u="${pair##*:}"; bad=""
  want="$(exp "$b")"
  pid="$(systemctl --user show "$u.service" -p MainPID --value 2>/dev/null)"
  st="$(systemctl --user show "$u.service" -p ActiveState --value 2>/dev/null)"
  sub="$(systemctl --user show "$u.service" -p SubState --value 2>/dev/null)"
  exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null)"
  got="$(sha256sum "/proc/$pid/exe" 2>/dev/null | cut -d' ' -f1)"
  started="$(ps -o lstart= -p "$pid" 2>/dev/null)"
  case "$pid" in ''|*[!0-9]*) bad="$bad pid-not-numeric" ;; 0) bad="$bad pid-zero" ;; esac
  [ "$st" = "active" ] || bad="$bad active-state=$st"
  [ "$sub" = "running" ] || bad="$bad substate=$sub"
  [ "$exe" = "$PROJECT/backend/bin/$b" ] || bad="$bad exe-path=$exe"
  [ -n "$want" ] && [ "$want" = "$got" ] || bad="$bad sha want=$want got=$got"
  {
    if [ -z "$bad" ]; then echo "$u verdict=OK MainPID=$pid state=$st/$sub exe=$exe exe_sha=$got started=$started"
    else echo "$u verdict=MISMATCH reasons:$bad MainPID=$pid state=$st/$sub exe=$exe exe_sha=$got started=$started"; fi
    echo "  ExecStart=$(systemctl --user show "$u.service" -p ExecStart --value 2>/dev/null)"
    echo "  EnvironmentFiles=$(systemctl --user show "$u.service" -p EnvironmentFiles --value 2>/dev/null)"
  } >> "$OUT/runtime-vs-frozen.txt"
  [ -n "$bad" ] && FAILED=1
done
cat "$OUT/runtime-vs-frozen.txt" >> "$OUT/RUN.log"

# ---------- D. 普查（逐条 rc） ----------
sect "D census"
sqlc() { # sqlc <outfile> <sql>
  local f="$1"; shift
  q "$1" > "$f" 2>&1; local rc=$?
  if [ "$rc" -ne 0 ]; then echo "SQL FAIL rc=$rc file=$(basename "$f")" >> "$OUT/RUN.log"; FAILED=1; fi
  cat "$f" >> "$OUT/RUN.log"
  return 0
}
sqlc "$OUT/census-org-ws-status.txt" "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), status, count(*) FROM download_jobs GROUP BY organization_id, workspace_id, status ORDER BY organization_id, workspace_id, status"
sqlc "$OUT/census-org-ws-total.txt"  "SELECT organization_id::text, COALESCE(workspace_id::text,'NULL'), count(*) FROM download_jobs GROUP BY organization_id, workspace_id ORDER BY organization_id, workspace_id"
sqlc "$OUT/census-status.txt"        "SELECT status, count(*) FROM download_jobs GROUP BY status ORDER BY status"

# ---------- E. 活动任务逐 JobID（部署后 snapshot，含逐 shard cursor hash） ----------
sect "E active jobs post-deploy snapshot"
sqlc "$OUT/active-jobs-perid.txt" "SELECT j.id::text, j.job_no, j.status, j.organization_id::text, COALESCE(j.workspace_id::text,'NULL'), to_char(j.updated_at,'YYYY-MM-DD\"T\"HH24:MI:SSOF'), (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id), (SELECT COALESCE(sum(s.downloaded_rows),0) FROM download_shards s WHERE s.job_id=j.id), (SELECT count(*) FROM download_shards s WHERE s.job_id=j.id AND s.page_token IS NOT NULL AND s.page_token<>''), (SELECT COALESCE(sum(s.page_no),0) FROM download_shards s WHERE s.job_id=j.id) FROM download_jobs j WHERE j.status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE') ORDER BY j.status, j.id"
sqlc "$OUT/active-shards-cursorhash.txt" "SELECT s.id::text, s.job_id::text, s.status, s.page_no, (s.page_token IS NOT NULL AND s.page_token<>'') AS token_present, COALESCE(md5(s.page_token),'-') AS token_md5, COALESCE(s.page_state::text,'-') AS page_state, s.downloaded_rows, s.updated_at FROM download_shards s WHERE s.job_id IN (SELECT id FROM download_jobs WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')) ORDER BY s.job_id, s.id"
sqlc "$OUT/checkpoints-json.txt" "SELECT to_jsonb(c)::text FROM checkpoints c LIMIT 200"

# ---------- F. 表计数（逐条 rc，主 shell） ----------
sect "F table counts"
: > "$OUT/table-counts.txt"
for t in download_jobs download_shards checkpoints provider_usage audit_events_v2; do
  n="$(q "SELECT count(*) FROM $t" 2>&1)"; rc=$?
  if [ "$rc" -ne 0 ]; then echo "$t count=ERR rc=$rc detail=$n" >> "$OUT/table-counts.txt"; FAILED=1
  else echo "$t count=$n rc=0" >> "$OUT/table-counts.txt"; fi
done
cat "$OUT/table-counts.txt" >> "$OUT/RUN.log"

# ---------- G. 缺口声明 + 机器可读结果 ----------
cat > "$OUT/GAP-STATEMENT.txt" <<'GAP'
真实缺口（不假称已对照）：
1. 本次部署（2026-09-12T23:21:21Z）之前，pre-state 的 (org, ws, status) 逐组合普查 SQL 真实失败
   （ERROR: ORDER BY position 2 is not in select list），原始错误文本保留在该次运行的 deploy-prestate.txt。
   因此**部署前的逐组合基线不存在**。
2. 部署前**未采集逐 JobID / 逐 shard 的 page_token 指针基线**（本目录 active-shards-cursorhash.txt 为**部署后快照**）。
   故"重启未改变 cursor"这一结论**不成立**，不得反向断言。可支持的范围仅限：
   (a) 部署前后 aggregate status census 完全一致；
   (b) 活动任务的 updated_at 均早于部署时刻。
3. 下一次部署必须先正确采集逐 JobID/逐 shard 基线再重启。
GAP
cat "$OUT/GAP-STATEMENT.txt" >> "$OUT/RUN.log"

cat > "$OUT/RESULT.json" <<JSON
{
  "mode": "$MODE",
  "expect_fail": $EXPECT_FAIL,
  "failed": $FAILED,
  "verdict": "$([ "$FAILED" -eq 0 ] && echo PASS || echo FAIL)",
  "script_sha256": "$(sha256sum "$0" | cut -d' ' -f1)",
  "evidence_dir": "$EV",
  "frozen_manifest_sha256": "$(sha256sum "$EV/artifact-sha256.txt" | cut -d' ' -f1)",
  "checked_at_utc": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "expected": $(awk '{printf "%s\"%s\":\"%s\"", (NR>1?",":""), $2, $1}' "$OUT/expected-from-frozen-manifest.txt" | sed 's/^/{/; s/$/}/')
}
JSON
cat "$OUT/RESULT.json"

if [ "$MODE" = "negative-calibration" ]; then
  if [ "$FAILED" -ne 0 ]; then say "NEGATIVE CALIBRATION OK (guard failed as expected)"; exit 0
  else say "NEGATIVE CALIBRATION BROKEN (guard passed on deliberately wrong manifest)"; exit 4; fi
fi
say "=== strict post-verify v3 done FAILED=$FAILED ==="
exit "$FAILED"
