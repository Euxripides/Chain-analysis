#!/usr/bin/env bash
# run-r10-export-excelize.sh — 服务端 excelize 真实 XLSX 导出端到端验收（app-v2 真实浏览器 + legacy 客户端导出/导入）。
# 冻结后不再修改（后续修正一律追加新脚本，不 hot-edit 本文件）。
#
# 链路：真实 HTTPS 8448 登录 → 导出中心真实 UI → POST /api/v2/exports（真实 Trace/Request ID）
#       → 服务端 excelize 生成 xlsx（zip 内 addresses/<addr>/<dataset>.xlsx）
#       → 60s 单次下载票据 → 浏览器真实落盘 → Node 解 ZIP（CRC32 校验）→ SheetJS 读回
#       → 本脚本再用同一份 JSON 与 PostgreSQL / ClickHouse / 审计 逐项对照。
#
# 红线：不 TRUNCATE/reset/clean；不删旧证据；只新增；不给缺失输入发绿。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1; }
chq(){ docker exec -i deploy-clickhouse-1 clickhouse-client --query "$1"; }

echo "=== P0 preflight ==="
PRE_MISSING=0
if [ ! -f /home/euripides/.s7jobscope-creds.env ]; then echo "PREFLIGHT_MISSING creds file"; PRE_MISSING=1; else
  set -a; . /home/euripides/.s7jobscope-creds.env; set +a
fi
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW; do
  eval "val=\$$v"
  if [ -z "${val:-}" ]; then echo "PREFLIGHT_MISSING $v"; PRE_MISSING=1; fi
done
JOB_NO="${R10_JOB_NO:-DL-20260913-0018}"
CHAIN="${R10_CHAIN:-bsc}"
ADDRESS="${R10_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
DATASET="${R10_DATASET:-transactions}"
JOB_ID="$(printf "SELECT id FROM download_jobs WHERE job_no='%s';
" "$JOB_NO" | pgq | tr -d '[:space:]')"
if [ -z "$JOB_ID" ]; then echo "PREFLIGHT_MISSING job $JOB_NO"; PRE_MISSING=1; fi
if [ "$PRE_MISSING" -ne 0 ]; then echo "PREFLIGHT_FAILED - refusing to run (no green from missing inputs)" | tee -a "$EV/run-meta.txt"; exit 3; fi
echo "preflight=OK job_no=$JOB_NO job_id=$JOB_ID chain=$CHAIN address=$ADDRESS dataset=$DATASET" | tee -a "$EV/run-meta.txt"

echo "=== P1 pre-state ==="
{
  echo "job_id=$JOB_ID job_no=$JOB_NO chain=$CHAIN address=$ADDRESS dataset=$DATASET"
  echo "jobs_before=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)"
  echo "shards_before=$(printf "SELECT count(*) FROM download_shards;
" | pgq)"
  echo "checkpoints_before=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)"
  echo "outbox_before=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)"
  echo "exports_before=$(printf "SELECT count(*) FROM exports;
" | pgq)"
  echo "download_subjects_before=$(printf "SELECT count(*) FROM download_subjects;
" | pgq)"
  echo "git_untracked_note=VM 工作目录非 git 仓库（工作树在 Windows），本文件不记录 git 状态"
} | tee "$EV/p1-pre-state.txt"

echo "=== P1b ClickHouse 期望行数（与导出的 WHERE/FINAL 完全同谓词） ==="
CH_TX="SELECT uniqExact(tx_hash) FROM blockchain.transactions FINAL WHERE chain='$CHAIN' AND (from_address='$ADDRESS' OR to_address='$ADDRESS')"
CH_TT="SELECT uniqExact((tx_hash, log_index, token_address)) FROM blockchain.token_transfers FINAL WHERE chain='$CHAIN' AND (from_address='$ADDRESS' OR to_address='$ADDRESS')"
{
  echo "expected_transactions_rows=$(chq "$CH_TX")"
  echo "expected_token_transfers_rows=$(chq "$CH_TT")"
  echo "total_transactions_rows=$(chq "SELECT count(*) FROM blockchain.transactions WHERE chain='$CHAIN' AND (from_address='$ADDRESS' OR to_address='$ADDRESS')")"
} | tee "$EV/p1b-ch-expected.txt"

echo "=== P2 real browser run ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R10_OUT="$EV" R10_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R10_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R10_JOB_ID="$JOB_ID" R10_JOB_NO="$JOB_NO" R10_ADDRESS="$ADDRESS" R10_CHAIN="$CHAIN" R10_DATASET="$DATASET"
node node_modules/@playwright/test/cli.js test --config playwright.r10.config.ts --reporter=list --reporter=json > "$EV/playwright.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 60 "$EV/playwright.log"

echo "=== P3 机器 JSON 汇总 + 矩阵 ==="
python3 "$R/scripts/s7/r10-crosscheck.py" "$EV" "$JOB_ID" "$JOB_NO" "$CHAIN" "$ADDRESS" | tee "$EV/r10-summary.json"
XRC=${PIPESTATUS[0]}
echo "crosscheck_rc=$XRC" | tee -a "$EV/run-meta.txt"

echo "=== P4 post-state（红线：无数据丢失） ==="
{
  echo "jobs_after=$(printf "SELECT count(*) FROM download_jobs;
" | pgq)"
  echo "shards_after=$(printf "SELECT count(*) FROM download_shards;
" | pgq)"
  echo "checkpoints_after=$(printf "SELECT count(*) FROM checkpoints;
" | pgq)"
  echo "outbox_after=$(printf "SELECT count(*) FROM outbox_events;
" | pgq)"
  echo "exports_after=$(printf "SELECT count(*) FROM exports;
" | pgq)"
} | tee "$EV/p4-post-state.txt"
diff <(sed "s/_before=/_=/" "$EV/p1-pre-state.txt" | grep -E "^(jobs|shards|checkpoints|outbox|exports)_=") <(sed "s/_after=/_=/" "$EV/p4-post-state.txt" | grep -E "^(jobs|shards|checkpoints|outbox|exports)_=") > "$EV/p4-state-diff.txt" 2>&1 || true
cat "$EV/p4-state-diff.txt"

echo "=== P5 MinIO 对象（无 mc，改为经鉴权下载 + sha 相等闭环，如实记录） ==="
echo "mc_installed=no" | tee "$EV/p5-object-check.txt"
echo "verification_chain=exports.object_ref/sha256 (MinIO PutObject 后服务端 objectSHA256) == 浏览器落盘文件 sha256（Node 计算）" | tee -a "$EV/p5-object-check.txt"

if [ "$RC" -eq 0 ] && [ "$XRC" -eq 0 ]; then echo "R10_VERDICT=PASS"; else echo "R10_VERDICT=FAIL playwright_rc=$RC crosscheck_rc=$XRC"; fi | tee -a "$EV/run-meta.txt"
exit $(( RC != 0 || XRC != 0 ))
