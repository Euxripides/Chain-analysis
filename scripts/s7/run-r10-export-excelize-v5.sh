#!/usr/bin/env bash
# run-r10-export-excelize-v5.sh — R10 v5：唯一、冻结、七类 crosscheck 缺陷已修的服务端 excelize 端到端验收。
#
# v4 第 2 轮（r10-export-excelize-v4-20260913T034232Z）：playwright_rc=0、uc-desktop 8/8 + uc-mobile R10-01 全 expected，
# crosscheck 143 项仅 1 项失败，且该失败是 crosscheck 自身缺陷 D7（矩阵用 "passed" 比 JSON reporter 的 "expected"）。
# v5 用 r10-crosscheck-v5.py（D7 已修），其余冻结语义与 v4 完全一致。
#
# v1/v2/v3 轮（run-r10-export-excelize*.sh、r10-crosscheck*.py、r10v2-*.spec.ts）全部原样保留，不 hot-edit。
# v3 轮已由根 11:06 判定 INVALIDATED_BY_MIDRUN_DEPLOY（证据目录内 0444 标记）；v4 是"修完 crosscheck 缺陷后"的
# 全新唯一测量。
#
# v3 -> v4 的更正：
#   1) crosscheck v4（audit_events_v2 / 非导出证据分派 / 增量只算真正新建 / mc 独立校验可用 /
#      R10-07 探针与契约 / R10-08 用有 export:create 的跨工作区身份）。
#   2) spec v3（r10v3-*.spec.ts）+ config v3（playwright.r10v3.config.ts）。
#   3) **冻结绑定**：本轮开始/结束各记录一次 api pid/exe/sha 与 worker sha，写 freeze.json；
#      测试运行期间**禁止任何部署/替换 bundle/替换 script**；api sha 必须等于 FREEZE_API_SHA，
#      且运行前后不得变化（mid-run deploy 必须被判为不成立，不能"看不见"）。
#   4) 跨工作区身份改用 P1 轮的追加夹具（只读身份 + 有 export:create 身份），
#      POST 必须带 Idempotency-Key（P1 轮真实 400 缺陷的回归）。
#
# 红线：不 TRUNCATE/reset/clean；不删旧证据/旧二进制/失败轮；只新增；不给缺失输入发绿；
#       测试运行期间不做任何部署。
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/r10-export-excelize-v5-$TS}"
FREEZE_API_SHA="${FREEZE_API_SHA:-4ec819add8efa61f7d5f25e8d499ce9bef712f17a1c36293ee08f458fb0e3978}"
R10C="${R10_CONSOLE:-$R/scripts/s7/r10-crosscheck-v5.py}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
MINIO_ENV="/tmp/r10v5-minio-restricted-$TS.env"
cleanup(){ rm -f "$MINIO_ENV"; }
trap cleanup EXIT
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)" | tee "$EV/run-meta.txt"
echo "crosscheck_sha256=$(sha256sum "$R10C" | cut -d' ' -f1)" | tee -a "$EV/run-meta.txt"
echo "evidence=$EV" | tee -a "$EV/run-meta.txt"
echo "expected_api_sha=$FREEZE_API_SHA" | tee -a "$EV/run-meta.txt"
echo "started_utc=$TS" | tee -a "$EV/run-meta.txt"

pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }
api_pid(){ pgrep -f "$R/backend/bin/api\$" | head -1; }
worker_pid(){ pgrep -f "$R/backend/bin/worker\$" | head -1; }

echo "=== P0a 冻结基线（测试运行期间不得变化） ==="
API_PID_BEFORE="$(api_pid)"; WORKER_PID_BEFORE="$(worker_pid)"
if [ -z "$API_PID_BEFORE" ] || [ -z "$WORKER_PID_BEFORE" ]; then echo "FATAL no running api/worker"; exit 8; fi
API_EXE_BEFORE="$(readlink -f "/proc/$API_PID_BEFORE/exe")"
API_SHA_BEFORE="$(sha256sum "/proc/$API_PID_BEFORE/exe" | cut -d' ' -f1)"
WORKER_SHA_BEFORE="$(sha256sum "/proc/$WORKER_PID_BEFORE/exe" | cut -d' ' -f1)"
echo "api_pid=$API_PID_BEFORE exe=$API_EXE_BEFORE sha=$API_SHA_BEFORE" | tee -a "$EV/run-meta.txt"
echo "worker_pid=$WORKER_PID_BEFORE sha=$WORKER_SHA_BEFORE" | tee -a "$EV/run-meta.txt"
if [ "$API_SHA_BEFORE" != "$FREEZE_API_SHA" ]; then
  echo "FREEZE_MISMATCH run api sha=$API_SHA_BEFORE expected=$FREEZE_API_SHA - refusing to run"
  echo "freeze_mismatch=1" | tee -a "$EV/run-meta.txt"; exit 7
fi
if [ "$API_EXE_BEFORE" != "$R/backend/bin/api" ]; then echo "FREEZE_BAD_EXE $API_EXE_BEFORE"; exit 7; fi

echo "=== P0b readiness（真实 API，不接受裸 200） ==="
READY_OK=0
for i in $(seq 1 30); do
  BODY="$(curl -s -m 5 http://127.0.0.1:8080/health/ready || true)"
  if printf '%s' "$BODY" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    sys.exit(1)
c=d.get("checks") or {}
sys.exit(0 if d.get("status")=="ready" and all(c.get(k)=="ok" for k in ("postgres","clickhouse","nats","minio")) else 1)' ; then READY_OK=1; break; fi
  sleep 1
done
echo "ready_attempts=$i ready_ok=$READY_OK body=$BODY" | tee -a "$EV/run-meta.txt"
if [ "$READY_OK" -ne 1 ]; then echo "READINESS_GATE_FAILED (8080/health/ready JSON 四依赖必须全 ok)"; exit 8; fi

echo "=== P0c preflight（正则白名单 + 作用域 membership，缺项即失败） ==="
PRE_MISSING=0
for f in /home/euripides/.s7jobscope-creds.env /home/euripides/.p1gate-inv-b.env /home/euripides/.p1gate-view-b.env; do
  [ -f "$f" ] || { echo "PREFLIGHT_MISSING file $f"; PRE_MISSING=1; }
done
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
for v in S7JS_INVESTIGATOR_EMAIL S7JS_INVESTIGATOR_PW S7JS_INVESTIGATOR_WS S7JS_ORG_ID; do
  eval "val=\$$v"; if [ -z "${val:-}" ]; then echo "PREFLIGHT_MISSING $v"; PRE_MISSING=1; fi
done
VIEW_B_EMAIL="$(grep -E '^S7JS_INVESTIGATOR_EMAIL=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
VIEW_B_PW="$(grep -E '^S7JS_INVESTIGATOR_PW=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
VIEW_B_WS="$(grep -E '^S7JS_INVESTIGATOR_WS=' /home/euripides/.p1gate-inv-b.env | cut -d= -f2-)"
RO_B_EMAIL="$(grep -E '^S7JS_VIEWER_EMAIL=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
RO_B_PW="$(grep -E '^S7JS_VIEWER_PW=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
RO_B_WS="$(grep -E '^S7JS_VIEWER_WS=' /home/euripides/.p1gate-view-b.env | cut -d= -f2-)"
for pair in "view_b_email:$VIEW_B_EMAIL" "view_b_pw:$VIEW_B_PW" "view_b_ws:$VIEW_B_WS" "ro_b_email:$RO_B_EMAIL" "ro_b_pw:$RO_B_PW" "ro_b_ws:$RO_B_WS"; do
  k="${pair%%:*}"; v="${pair#*:}"; if [ -z "$v" ]; then echo "PREFLIGHT_MISSING $k"; PRE_MISSING=1; fi
done
JOB_NO="${R10_JOB_NO:-DL-20260913-0018}"
CHAIN="${R10_CHAIN:-bsc}"
ADDRESS_IN="${R10_ADDRESS:-0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649}"
DATASET="${R10_DATASET:-transactions}"
echo "$CHAIN" | grep -Eq '^[a-z0-9_-]{2,20}$' || { echo "PREFLIGHT_BAD chain=$CHAIN"; PRE_MISSING=1; }
echo "$DATASET" | grep -Eq '^(transactions|token_transfers|nft_transfers|logs|balances|internal_transactions)$' || { echo "PREFLIGHT_BAD dataset=$DATASET"; PRE_MISSING=1; }
echo "$JOB_NO" | grep -Eq '^[A-Za-z0-9._-]{1,64}$' || { echo "PREFLIGHT_BAD job_no=$JOB_NO"; PRE_MISSING=1; }
if ! echo "$ADDRESS_IN" | grep -Eqi '^(0x[0-9a-f]{40}|T[1-9A-HJ-NP-Za-km-z]{33})$'; then echo "PREFLIGHT_BAD address"; PRE_MISSING=1; fi
if [ "$PRE_MISSING" -ne 0 ]; then echo "PREFLIGHT_FAILED - refusing to run (no green from missing/invalid inputs)" | tee -a "$EV/run-meta.txt"; exit 3; fi
if [ "$CHAIN" != "tron" ]; then ADDRESS="$(printf '%s' "$ADDRESS_IN" | tr 'A-Z' 'a-z')"; else ADDRESS="$ADDRESS_IN"; fi
JOB_ID="$(printf "SELECT id FROM download_jobs WHERE job_no='%s';\n" "$JOB_NO" | pgq | tr -d '[:space:]')"
if ! printf '%s' "$JOB_ID" | grep -Eq '^[0-9a-f-]{36}$'; then echo "PREFLIGHT_MISSING job $JOB_NO"; exit 3; fi

read -r JOB_ORG JOB_WS JOB_STATUS JOB_CHAIN JOB_DATASETS <<EOF
$(printf "SELECT organization_id, workspace_id, status, chain, array_to_string(datasets,',') FROM download_jobs WHERE id='%s';\n" "$JOB_ID" | pgq | tr '|' ' ')
EOF
for pair in "org:$JOB_ORG:$S7JS_ORG_ID" "ws:$JOB_WS:$S7JS_INVESTIGATOR_WS"; do
  kind="${pair%%:*}"; rest="${pair#*:}"; have="${rest%%:*}"; want="${rest#*:}"
  if [ "$have" != "$want" ]; then echo "PREFLIGHT_SCOPE_MISMATCH $kind have=$have want=$want"; exit 3; fi
done
if [ "$JOB_STATUS" != "COMPLETED" ]; then echo "PREFLIGHT_JOB_NOT_COMPLETED $JOB_STATUS"; exit 3; fi
case ",$JOB_DATASETS," in *",$DATASET,"*) : ;; *) echo "PREFLIGHT_DATASET_NOT_IN_JOB $DATASET vs $JOB_DATASETS"; exit 3 ;; esac
SUBJECTS="$(printf "SELECT subject FROM download_subjects WHERE job_id='%s';\n" "$JOB_ID" | pgq | tr -d '[:space:]')"
if ! printf '%s\n' "$SUBJECTS" | grep -qx "$ADDRESS"; then echo "PREFLIGHT_ADDRESS_NOT_IN_JOB $ADDRESS"; exit 3; fi
for pair in "viewer_ws:$VIEW_B_WS:$JOB_WS" "readonly_ws:$RO_B_WS:$JOB_WS"; do
  kind="${pair%%:*}"; rest="${pair#*:}"; have="${rest%%:*}"; want="${rest#*:}"
  if [ "$have" = "$want" ]; then echo "PREFLIGHT_CROSS_WS_NOT_DISTINCT $kind=$have"; exit 3; fi
done
for pair in "$VIEW_B_EMAIL:$VIEW_B_WS" "$RO_B_EMAIL:$RO_B_WS"; do
  em="${pair%%:*}"; ws="${pair#*:}"
  N="$(printf "SELECT count(*) FROM identities i JOIN workspace_memberships wm ON wm.identity_id=i.id AND wm.status='active' WHERE i.email='%s' AND wm.workspace_id='%s';\n" "$em" "$ws" | pgq | tr -d '[:space:]')"
  if [ "$N" != "1" ]; then echo "PREFLIGHT_MEMBERSHIP_MISSING $em ws=$ws count=$N"; exit 3; fi
done
# 权限真值来自 member_role_bindings -> role_permissions（不是 identity_permissions，该表不存在——v4 自查更正）
PERM="$(printf "SELECT count(*) FROM identities i JOIN member_role_bindings mrb ON mrb.identity_id=i.id AND mrb.revoked_at IS NULL JOIN role_permissions rp ON rp.role_id=mrb.role_id AND rp.permission_key='export:create' WHERE i.email='%s';\n" "$VIEW_B_EMAIL" | pgq | tr -d '[:space:]')"
if [ "${PERM:-0}" -lt 1 ]; then echo "PREFLIGHT_EXPORT_CREATE_MISSING $VIEW_B_EMAIL ($PERM)"; exit 3; fi
echo "preflight=OK job_no=$JOB_NO job_id=$JOB_ID org=$JOB_ORG ws=$JOB_WS chain=$JOB_CHAIN datasets=$JOB_DATASETS address=$ADDRESS dataset=$DATASET viewer_b_ws=$VIEW_B_WS readonly_b_ws=$RO_B_WS" | tee -a "$EV/run-meta.txt"

cat > "$EV/scope.json" <<EOF
{"job_no":"$JOB_NO","job_id":"$JOB_ID","org_id":"$JOB_ORG","ws_id":"$JOB_WS","chain":"$JOB_CHAIN","address":"$ADDRESS","dataset":"$DATASET","viewer_ws_id":"$VIEW_B_WS","readonly_ws_id":"$RO_B_WS","api_sha":"$FREEZE_API_SHA"}
EOF
chmod 600 "$EV/scope.json"

echo "=== P1 pre-state（PK 快照，供原 PK 子集核验） ==="
set -e
pkcol_at(){ case "$1" in checkpoints) echo shard_id ;; *) echo id ;; esac; }
for t in download_jobs download_shards checkpoints outbox_events exports; do
  printf "SELECT %s::text FROM %s ORDER BY %s::text;\n" "$(pkcol_at "$t")" "$t" "$(pkcol_at "$t")" | pgq > "$EV/pre-ids-$t.txt"
  printf "%s=%s\n" "$t" "$(wc -l < "$EV/pre-ids-$t.txt")" >> "$EV/p1-pre-state.txt"
done
set +e
cat "$EV/p1-pre-state.txt"

echo "=== P1b MinIO 独立校验准备（mc 从容器提取，凭据只经进程环境；MC_CONFIG_DIR 必须存在） ==="
mkdir -p /tmp/r10-mc /tmp/r10-mc-cfg
if [ ! -x /tmp/r10-mc/mc ]; then docker cp deploy-minio-1:/usr/bin/mc /tmp/r10-mc/mc >/dev/null && chmod 755 /tmp/r10-mc/mc; fi
/tmp/r10-mc/mc --version | head -1 | tee "$EV/p1b-minio-tool.txt"
umask 077
grep -E '^(MINIO_ROOT_USER|MINIO_ROOT_PASSWORD|MINIO_ENDPOINT|EXPORT_BUCKET|RAW_BUCKET)=' "$R/deploy/.env" > "$MINIO_ENV"
if ! grep -q '^RAW_BUCKET=' "$MINIO_ENV"; then echo 'RAW_BUCKET=blockchain-raw' >> "$MINIO_ENV"; fi
chmod 600 "$MINIO_ENV"
echo "minio_env_keys=$(cut -d= -f1 "$MINIO_ENV" | sort | tr '\n' ',')" | tee -a "$EV/p1b-minio-tool.txt"

echo "=== P2 real browser run（reporters 只来自 config） ==="
cd "$R/frontend" || exit 90
export E2E_BASE_URL="${E2E_BASE_URL:-https://127.0.0.1:8448}"
export E2E_CHROMIUM_PATH="${E2E_CHROMIUM_PATH:-/usr/bin/chromium-browser}"
export R10_OUT="$EV" R10_EMAIL="$S7JS_INVESTIGATOR_EMAIL" R10_PASSWORD="$S7JS_INVESTIGATOR_PW"
export R10_JOB_ID="$JOB_ID" R10_JOB_NO="$JOB_NO" R10_ADDRESS="$ADDRESS" R10_CHAIN="$CHAIN" R10_DATASET="$DATASET"
export R10_VIEWER_EMAIL="$VIEW_B_EMAIL" R10_VIEWER_PASSWORD="$VIEW_B_PW"
export R10_VIEWONLY_EMAIL="$RO_B_EMAIL" R10_VIEWONLY_PASSWORD="$RO_B_PW"
node node_modules/@playwright/test/cli.js test --config playwright.r10v3.config.ts > "$EV/playwright.console.log" 2>&1
RC=$?
echo "$RC" > "$EV/playwright.rc"
echo "playwright_rc=$RC" | tee -a "$EV/run-meta.txt"
tail -n 60 "$EV/playwright.console.log"

echo "=== P3 冻结复核 + 机器 report 实际校验 ==="
API_PID_AFTER="$(api_pid)"; WORKER_PID_AFTER="$(worker_pid)"
API_EXE_AFTER="$(readlink -f "/proc/$API_PID_AFTER/exe" 2>/dev/null || echo '')"
API_SHA_AFTER="$(sha256sum "/proc/$API_PID_AFTER/exe" 2>/dev/null | cut -d' ' -f1)"
WORKER_SHA_AFTER="$(sha256sum "/proc/$WORKER_PID_AFTER/exe" 2>/dev/null | cut -d' ' -f1)"
python3 - "$EV/freeze.json" <<PY
import json,sys
json.dump({
 "api_pid_before": "$API_PID_BEFORE", "api_pid_after": "$API_PID_AFTER",
 "api_exe_before": "$API_EXE_BEFORE", "api_exe_after": "$API_EXE_AFTER",
 "api_sha_before": "$API_SHA_BEFORE", "api_sha_after": "$API_SHA_AFTER",
 "worker_pid_before": "$WORKER_PID_BEFORE", "worker_pid_after": "$WORKER_PID_AFTER",
 "worker_sha_before": "$WORKER_SHA_BEFORE", "worker_sha_after": "$WORKER_SHA_AFTER",
 "expected_api_sha": "$FREEZE_API_SHA",
}, open(sys.argv[1],"w",encoding="utf-8"), ensure_ascii=False, indent=2)
PY
chmod 600 "$EV/freeze.json"
cat "$EV/freeze.json" | tee -a "$EV/run-meta.txt"
if [ "$API_SHA_AFTER" != "$API_SHA_BEFORE" ] || [ "$API_PID_AFTER" != "$API_PID_BEFORE" ]; then
  echo "FREEZE_VIOLATION_MID_RUN_DEPLOY api pid/sha changed during the run" | tee -a "$EV/run-meta.txt"; RC=1
fi

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

echo "=== P4 严格对照（DB / ClickHouse / audit_events_v2 / MinIO / 状态不变量 / 冻结） ==="
R10_MINIO_ENV="$MINIO_ENV" python3 "$R10C" "$EV" > "$EV/r10-summary.json" 2> "$EV/r10-crosscheck.err"
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
for obs in (data.get("observations") or []):
    if "bucket" in obs["observation"] or "delta" in obs["observation"]:
        print("OBS: %s -> %s" % (obs["observation"], json.dumps(obs["detail"], ensure_ascii=False)[:300]))
PY
[ -s "$EV/r10-crosscheck.err" ] && { echo "--- crosscheck stderr ---"; head -20 "$EV/r10-crosscheck.err"; }

if [ "$RC" -eq 0 ] && [ "$XRC" -eq 0 ]; then
  echo "R10_V5_VERDICT=PASS" | tee -a "$EV/run-meta.txt"
else
  echo "R10_V5_VERDICT=FAIL playwright_rc=$RC crosscheck_rc=$XRC report_ok=$REPORT_OK" | tee -a "$EV/run-meta.txt"
fi
if [ "$RC" -ne 0 ] || [ "$XRC" -ne 0 ]; then exit 1; fi
exit 0
