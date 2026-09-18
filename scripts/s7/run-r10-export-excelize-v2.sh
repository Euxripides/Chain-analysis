#!/usr/bin/env bash
# run-r10-export-excelize-v2.sh — R10 v2：按根 review 逐条更正后的服务端 excelize 端到端验收。
# v1（run-r10-export-excelize.sh）已实际运行且原样保留，不 hot-edit；本文件是后验更正版。
#
# v1 → v2 更正（根的 review）：
#   C1 不再给 Playwright CLI 传 --reporter（CLI 会覆盖 config，导致 playwright-report.json 根本没写）；
#      改为只用 config 的 reporter，并在运行后断言机器 report 存在/非空/可解析。
#   C2 全部输入走正则白名单并写入 scope.json；ClickHouse 一律 stdin，不把测试 env 拼进 SQL/argv。
#   C3 P4 不再要求 exports 前后相等（本轮本来就会新增）；改为"原 PK 不丢"真子集核验 + exports 精确增量，
#      且没有任何 "|| true"。
#   C4 期望行数改为按服务端 exportJobBounds 语义从 download_jobs 读真实 range（在 crosscheck v2 内）。
#   C5 增加独立 MinIO（S3/mc）对象校验，与"经 API 同路下载"分开记录；凭据只经进程环境，不进 argv/证据。
#   C6 preflight 增加 org/workspace/job membership 与该 job 的 download_subjects 覆盖校验。
#
# 红线：不 TRUNCATE/reset/clean；不删旧证据；只新增；不给缺失输入发绿。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v2-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
MINIO_ENV="/tmp/r10-minio-restricted-$TS.env"
cleanup(){ rm -f "$MINIO_ENV"; }
trap cleanup EXIT
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }

echo "=== P0 preflight（正则白名单 + 作用域 membership，缺项即失败） ==="
PRE_MISSING=0
if [ ! -f /home/euripides/.s7jobscope-creds.env ]; then echo "PREFLIGHT_MISSING creds file"; PRE_MISSING=1; else
  set -a; . /home/euripides/.s7jobscope-creds.env; set +a
fi
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS S7JS_ORG_ID S7JS_VIEWER_EMAIL S7JS_VIEWER_PW S7JS_VIEWER_WS; do
  eval "val=\$$v"
  if [ -z "${val:-}" ]; then echo "PREFLIGHT_MISSING $v"; PRE_MISSING=1; fi
done
JOB_NO="${R10_JOB_NO:-DL-20260913-0018}"
CHAIN="${R10_CHAIN:-bsc}"
ADDRESS_IN="${R10_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
DATASET="${R10_DATASET:-transactions}"
# C2：输入白名单。任何不合法输入直接拒绝，不进 SQL、不进浏览器。
echo "$CHAIN" | grep -Eq '^[a-z0-9_-]{2,20}$' || { echo "PREFLIGHT_BAD chain=$CHAIN"; PRE_MISSING=1; }
echo "$DATASET" | grep -Eq '^(transactions|token_transfers|nft_transfers|logs|balances|internal_transactions)$' || { echo "PREFLIGHT_BAD dataset=$DATASET"; PRE_MISSING=1; }
echo "$JOB_NO" | grep -Eq '^[A-Za-z0-9._-]{1,64}$' || { echo "PREFLIGHT_BAD job_no=$JOB_NO"; PRE_MISSING=1; }
if ! echo "$ADDRESS_IN" | grep -Eqi '^(0x[0-9a-f]{40}|T[1-9A-HJ-NP-Za-km-z]{33})$'; then echo "PREFLIGHT_BAD address"; PRE_MISSING=1; fi
if [ "$PRE_MISSING" -ne 0 ]; then echo "PREFLIGHT_FAILED - refusing to run (no green from missing/invalid inputs)" | tee -a "$EV/run-meta.txt"; exit 3; fi
# 非 tron 链按服务端行为小写化；随后必须与服务端归一化 subject 完全一致。
if [ "$CHAIN" != "tron" ]; then ADDRESS="$(printf '%s' "$ADDRESS_IN" | tr 'A-Z' 'a-z')"; else ADDRESS="$ADDRESS_IN"; fi
JOB_ID="$(printf "SELECT id FROM download_jobs WHERE job_no='%s';
" "$JOB_NO" | pgq | tr -d '[:space:]')"
if ! printf '%s' "$JOB_ID" | grep -Eq '^[0-9a-f-]{36}$'; then echo "PREFLIGHT_MISSING job $JOB_NO"; exit 3; fi

# C6：job 必须属于本测试用户真实 org/workspace，且该地址确实在该 job 的 subject 集合内。
read -r JOB_ORG JOB_WS JOB_STATUS JOB_CHAIN JOB_DATASETS <<EOF
$(printf "SELECT organization_id, workspace_id, status, chain, array_to_string(datasets,',') FROM download_jobs WHERE id='%s';
" "$JOB_ID" | pgq | tr '|' ' ')
EOF
for pair in "org:$JOB_ORG:$S7JS_ORG_ID" "ws:$JOB_WS:$S7JS_INVESTIGATOR_WS"; do
  kind="${pair%%:*}"; rest="${pair#*:}"; have="${rest%%:*}"; want="${rest#*:}"
  if [ "$have" != "$want" ]; then echo "PREFLIGHT_SCOPE_MISMATCH $kind have=$have want=$want"; exit 3; fi
done
if [ "$JOB_STATUS" != "COMPLETED" ]; then echo "PREFLIGHT_JOB_NOT_COMPLETED $JOB_STATUS"; exit 3; fi
case ",$JOB_DATASETS," in *",$DATASET,"*) : ;; *) echo "PREFLIGHT_DATASET_NOT_IN_JOB $DATASET vs $JOB_DATASETS"; exit 3 ;; esac
SUBJECTS="$(printf "SELECT subject FROM download_subjects WHERE job_id='%s';
" "$JOB_ID" | pgq | tr -d '[:space:]')"
if ! printf '%s
' "$SUBJECTS" | grep -qx "$ADDRESS"; then echo "PREFLIGHT_ADDRESS_NOT_IN_JOB $ADDRESS"; exit 3; fi
if [ "$S7JS_VIEWER_WS" = "$JOB_WS" ]; then echo "PREFLIGHT_VIEWER_WS_NOT_DISTINCT $S7JS_VIEWER_WS"; exit 3; fi
echo "preflight=OK job_no=$JOB_NO job_id=$JOB_ID org=$JOB_ORG ws=$JOB_WS chain=$JOB_CHAIN datasets=$JOB_DATASETS address=$ADDRESS dataset=$DATASET" | tee -a "$EV/run-meta.txt"

cat > "$EV/scope.json" <<EOF
{"job_no":"$JOB_NO","job_id":"$JOB_ID","org_id":"$JOB_ORG","ws_id":"$JOB_WS","chain":"$JOB_CHAIN","address":"$ADDRESS","dataset":"$DATASET"}
EOF
chmod 600 "$EV/scope.json"

echo "=== P1 pre-state（PK 快照，供 C3 原 PK 子集核验） ==="
set -e
for t in download_jobs download_shards checkpoints outbox_events exports; do
  printf "SELECT id::text FROM %s ORDER BY id::text;
" "$t" | pgq > "$EV/pre-ids-$t.txt"
  printf "%s=%s
" "$t" "$(wc -l < "$EV/pre-ids-$t.txt")" >> "$EV/p1-pre-state.txt"
done
set +e
cat "$EV/p1-pre-state.txt"

echo "=== P1b MinIO 独立校验准备（mc 从容器提取，凭据只经进程环境） ==="
mkdir -p /tmp/r10-mc
if [ ! -x /tmp/r10-mc/mc ]; then docker cp deploy-minio-1:/usr/bin/mc /tmp/r10-mc/mc >/dev/null && chmod 755 /tmp/r10-mc/mc; fi
/tmp/r10-mc/mc --version | head -1 | tee "$EV/p1b-minio-tool.txt"
umask 077
grep -E '^(MINIO_ROOT_USER|MINIO_ROOT_PASSWORD|MINIO_ENDPOINT|EXPORT_BUCKET)=' "$R/deploy/.env" > "$MINIO_ENV"
chmod 600 "$MINIO_ENV"
echo "minio_env_keys=$(cut -d= -f1 "$MINIO_ENV" | sort | tr '\n' ',')" | tee -a "$EV/p1b-minio-tool.txt"

echo "=== P2 real browser run（reporters 只来自 config，C1） ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R10_OUT="$EV" R10_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R10_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R10_JOB_ID="$JOB_ID" R10_JOB_NO="$JOB_NO" R10_ADDRESS="$ADDRESS" R10_CHAIN="$CHAIN" R10_DATASET="$DATASET"
export R10_VIEWER_EMAIL="$S7JS_VIEWER_EMAIL" R10_VIEWER_PASSWORD="$S7JS_VIEWER_PW"
node node_modules/@playwright/test/cli.js test --config playwright.r10v2.config.ts > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 60 "$EV/playwright.console.log"

echo "=== P3 机器 report 实际校验（C1：存在 + 非空 + 可解析 + 含 suites） ==="
REPORT_OK=0
if [ ! -s "$EV/playwright-report.json" ]; then
  echo "PLAYWRIGHT_REPORT_MISSING_OR_EMPTY $EV/playwright-report.json" | tee "$EV/p3-report-check.txt"; REPORT_OK=1
else
  python3 - "$EV/playwright-report.json" "$EV/p3-report-check.txt" <<'PY'
import json, sys
p, outp = sys.argv[1], sys.argv[2]
rep = json.load(open(p, encoding="utf-8"))
suites = rep.get("suites") or []
stats = rep.get("stats") or {}
lines = ["report_path=%s" % p, "bytes=%d" % len(open(p, "rb").read()), "suites=%d" % len(suites),
         "expected=%s unexpected=%s skipped=%s flaky=%s" % (stats.get("expected"), stats.get("unexpected"), stats.get("skipped"), stats.get("flaky"))]
open(outp, "w", encoding="utf-8").write("\n".join(lines) + "\n")
print("\n".join(lines))
sys.exit(0 if suites else 1)
PY
  REPORT_OK=$?
fi
if [ "$REPORT_OK" -ne 0 ]; then echo "report_check=FAIL" | tee -a "$EV/p3-report-check.txt"; RC=1; else echo "report_check=OK" | tee -a "$EV/p3-report-check.txt"; fi

echo "=== P4 严格对照（DB / ClickHouse / 审计 / MinIO / 状态不变量） ==="
R10_MINIO_ENV="$MINIO_ENV" python3 "$R/scripts/s7/r10-crosscheck-v2.py" "$EV" > "$EV/r10-summary.json" 2> "$EV/r10-crosscheck.err"
XRC=$?
echo "crosscheck_rc=$XRC" | tee -a "$EV/run-meta.txt"
python3 - "$EV/r10-summary.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
print("verdict=%s checks_total=%s checks_failed=%s" % (data.get("verdict"), data.get("checks_total"), data.get("checks_failed")))
for row in (data.get("failed") or [])[:40]:
    print("FAILED: %s -> %s" % (row["check"], json.dumps(row["detail"], ensure_ascii=False)[:300]))
for proj in ("uc-desktop", "uc-mobile"):
    rows = [r for r in (data.get("matrix") or []) if r["project"] == proj]
    if rows:
        print("%s: %s" % (proj, ", ".join("%s=%s" % (r["test"], r["actual"]) for r in rows)))
PY
[ -s "$EV/r10-crosscheck.err" ] && { echo "--- crosscheck stderr ---"; head -20 "$EV/r10-crosscheck.err"; }

if [ "$RC" -eq 0 ] && [ "$XRC" -eq 0 ]; then
  echo "R10_V2_VERDICT=PASS" | tee -a "$EV/run-meta.txt"
else
  echo "R10_V2_VERDICT=FAIL playwright_rc=$RC crosscheck_rc=$XRC report_ok=$REPORT_OK" | tee -a "$EV/run-meta.txt"
fi
if [ "$RC" -ne 0 ] || [ "$XRC" -ne 0 ]; then exit 1; fi
exit 0
